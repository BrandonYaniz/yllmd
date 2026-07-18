package local

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	runnerProtocolVersion       = 2
	runnerProtocolMinimum       = "26.07.16.01-Release"
	runnerRequiredCapabilities  = uint64(0x7f)
	runnerMaxFrameBytes         = 32 << 20
	runnerMaxPromptBytes        = 16 << 20
	runnerMaxStopCount          = 64
	runnerMaxStopBytes          = 64 << 10
	runnerTokenizationRaw       = byte(0)
	runnerTokenizationFormatted = byte(1)

	runnerMessageGenerate = byte(0x01)
	runnerMessageCancel   = byte(0x02)
	runnerMessageShutdown = byte(0x03)

	runnerFrameChunk     = byte(0x01)
	runnerFrameError     = byte(0x03)
	runnerFrameCompleted = byte(0x04)
	runnerFrameReady     = byte(0x10)
)

type runnerGenerate struct {
	prompt           string
	tokenizationMode byte
	maxTokens        uint32
	temperature      float64
	topP             float64
	topK             int32
	minP             float64
	presencePenalty  float64
	repeatPenalty    float64
	seed             uint64
	stops            []string
}

type runnerReady struct {
	protocol     uint16
	version      string
	contextSize  uint32
	capabilities uint64
}

type runnerCompletion struct {
	finishReason           byte
	inputTokens            uint32
	outputTokens           uint32
	promptMicroseconds     uint64
	generationMicroseconds uint64
}

type runnerProtocolError struct {
	code    string
	message string
}

func (e runnerProtocolError) Error() string {
	if e.message == "" {
		return e.code
	}
	if e.code == "" {
		return e.message
	}
	return e.code + ": " + e.message
}

type runnerFrame struct {
	tag         byte
	chunk       string
	ready       runnerReady
	completed   runnerCompletion
	runnerError *runnerProtocolError
}

func writeRunnerGenerate(writer io.Writer, request runnerGenerate) error {
	payload, err := encodeRunnerGenerate(request)
	if err != nil {
		return err
	}
	return writeRunnerEnvelope(writer, runnerMessageGenerate, payload)
}

func encodeRunnerGenerate(request runnerGenerate) ([]byte, error) {
	if request.tokenizationMode != runnerTokenizationRaw && request.tokenizationMode != runnerTokenizationFormatted {
		return nil, fmt.Errorf("invalid runner tokenization mode %d", request.tokenizationMode)
	}
	if !utf8.ValidString(request.prompt) {
		return nil, errors.New("runner prompt must be valid UTF-8")
	}
	if len(request.prompt) > runnerMaxPromptBytes {
		return nil, fmt.Errorf("runner prompt exceeds %d-byte limit", runnerMaxPromptBytes)
	}
	if len(request.stops) > runnerMaxStopCount {
		return nil, fmt.Errorf("runner stop count exceeds %d", runnerMaxStopCount)
	}
	totalStopBytes := 0
	for i, stop := range request.stops {
		if stop == "" || !utf8.ValidString(stop) {
			return nil, fmt.Errorf("runner stop %d must be nonempty valid UTF-8", i)
		}
		totalStopBytes += len(stop)
		if len(stop) > runnerMaxStopBytes || totalStopBytes > runnerMaxStopBytes {
			return nil, fmt.Errorf("runner stop strings exceed %d-byte limit", runnerMaxStopBytes)
		}
	}

	var payload bytes.Buffer
	payload.Grow(64 + totalStopBytes + len(request.prompt))
	payload.WriteByte(request.tokenizationMode)
	payload.WriteByte(0)
	writeUint16(&payload, uint16(len(request.stops)))
	writeUint32(&payload, request.maxTokens)
	writeFloat64(&payload, request.temperature)
	writeFloat64(&payload, request.topP)
	writeUint32(&payload, uint32(request.topK))
	writeFloat64(&payload, request.minP)
	writeFloat64(&payload, request.presencePenalty)
	writeFloat64(&payload, request.repeatPenalty)
	writeUint64(&payload, request.seed)
	for _, stop := range request.stops {
		writeUint32(&payload, uint32(len(stop)))
		payload.WriteString(stop)
	}
	writeUint32(&payload, uint32(len(request.prompt)))
	payload.WriteString(request.prompt)
	if payload.Len() > runnerMaxFrameBytes {
		return nil, fmt.Errorf("runner Generate payload exceeds %d-byte limit", runnerMaxFrameBytes)
	}
	return payload.Bytes(), nil
}

func writeRunnerControl(writer io.Writer, messageType byte) error {
	if messageType != runnerMessageCancel && messageType != runnerMessageShutdown {
		return fmt.Errorf("unsupported runner control message 0x%02x", messageType)
	}
	return writeRunnerEnvelope(writer, messageType, nil)
}

func writeRunnerEnvelope(writer io.Writer, messageType byte, payload []byte) error {
	if len(payload) > runnerMaxFrameBytes {
		return fmt.Errorf("runner payload exceeds %d-byte limit", runnerMaxFrameBytes)
	}
	var header [5]byte
	header[0] = messageType
	binary.LittleEndian.PutUint32(header[1:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func readRunnerProtocolFrame(reader io.Reader) (runnerFrame, error) {
	var header [5]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return runnerFrame{}, err
	}
	length := binary.LittleEndian.Uint32(header[1:])
	if length > runnerMaxFrameBytes {
		return runnerFrame{}, fmt.Errorf("runner frame length %d exceeds %d-byte limit", length, runnerMaxFrameBytes)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return runnerFrame{}, err
	}
	switch header[0] {
	case runnerFrameReady:
		ready, err := decodeRunnerReady(payload)
		return runnerFrame{tag: header[0], ready: ready}, err
	case runnerFrameChunk:
		chunk, err := decodeRunnerChunk(payload)
		return runnerFrame{tag: header[0], chunk: chunk}, err
	case runnerFrameCompleted:
		completed, err := decodeRunnerCompleted(payload)
		return runnerFrame{tag: header[0], completed: completed}, err
	case runnerFrameError:
		runnerError, err := decodeRunnerError(payload)
		return runnerFrame{tag: header[0], runnerError: &runnerError}, err
	default:
		return runnerFrame{}, fmt.Errorf("unknown runner frame tag 0x%02x", header[0])
	}
}

func decodeRunnerReady(payload []byte) (runnerReady, error) {
	if len(payload) < 16 {
		return runnerReady{}, errors.New("truncated runner Ready payload")
	}
	protocolVersion := binary.LittleEndian.Uint16(payload[0:2])
	versionLength := int(binary.LittleEndian.Uint16(payload[2:4]))
	expected := 4 + versionLength + 4 + 8
	if len(payload) != expected {
		return runnerReady{}, fmt.Errorf("runner Ready length %d, want %d", len(payload), expected)
	}
	version := string(payload[4 : 4+versionLength])
	if version == "" || !utf8.ValidString(version) {
		return runnerReady{}, errors.New("runner Ready contains invalid version")
	}
	offset := 4 + versionLength
	return runnerReady{
		protocol:     protocolVersion,
		version:      version,
		contextSize:  binary.LittleEndian.Uint32(payload[offset : offset+4]),
		capabilities: binary.LittleEndian.Uint64(payload[offset+4 : offset+12]),
	}, nil
}

func decodeRunnerChunk(payload []byte) (string, error) {
	if len(payload) < 4 {
		return "", errors.New("truncated runner Chunk payload")
	}
	length := int(binary.LittleEndian.Uint32(payload[:4]))
	if length != len(payload)-4 {
		return "", fmt.Errorf("runner Chunk length %d, want %d", length, len(payload)-4)
	}
	chunk := string(payload[4:])
	if !utf8.ValidString(chunk) {
		return "", errors.New("runner Chunk is not valid UTF-8")
	}
	return chunk, nil
}

func decodeRunnerCompleted(payload []byte) (runnerCompletion, error) {
	if len(payload) != 25 {
		return runnerCompletion{}, fmt.Errorf("runner Completed length %d, want 25", len(payload))
	}
	if payload[0] > 3 {
		return runnerCompletion{}, fmt.Errorf("unknown runner finish reason %d", payload[0])
	}
	return runnerCompletion{
		finishReason:           payload[0],
		inputTokens:            binary.LittleEndian.Uint32(payload[1:5]),
		outputTokens:           binary.LittleEndian.Uint32(payload[5:9]),
		promptMicroseconds:     binary.LittleEndian.Uint64(payload[9:17]),
		generationMicroseconds: binary.LittleEndian.Uint64(payload[17:25]),
	}, nil
}

func decodeRunnerError(payload []byte) (runnerProtocolError, error) {
	if len(payload) < 4 {
		return runnerProtocolError{}, errors.New("truncated runner Error payload")
	}
	codeLength := int(binary.LittleEndian.Uint16(payload[:2]))
	if len(payload) < 2+codeLength+2 {
		return runnerProtocolError{}, errors.New("truncated runner Error code")
	}
	code := string(payload[2 : 2+codeLength])
	offset := 2 + codeLength
	messageLength := int(binary.LittleEndian.Uint16(payload[offset : offset+2]))
	offset += 2
	if len(payload) != offset+messageLength {
		return runnerProtocolError{}, errors.New("invalid runner Error message length")
	}
	message := string(payload[offset:])
	if code == "" || !utf8.ValidString(code) || !utf8.ValidString(message) {
		return runnerProtocolError{}, errors.New("runner Error contains invalid text")
	}
	return runnerProtocolError{code: code, message: message}, nil
}

func runnerFinishReason(reason byte) string {
	switch reason {
	case 0:
		return "eos"
	case 1:
		return "length"
	case 2:
		return "stop"
	case 3:
		return "cancelled"
	default:
		return "unknown"
	}
}

func runnerErrorIsFatal(code string) bool {
	switch code {
	case "malformed_frame", "frame_too_large", "decode_failed", "detokenize_failed", "invalid_backend_utf8", "not_configured":
		return true
	default:
		return false
	}
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func writeUint16(buffer *bytes.Buffer, value uint16) {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	buffer.Write(encoded[:])
}

func writeUint32(buffer *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	buffer.Write(encoded[:])
}

func writeUint64(buffer *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	buffer.Write(encoded[:])
}

func writeFloat64(buffer *bytes.Buffer, value float64) {
	writeUint64(buffer, math.Float64bits(value))
}

func compareRunnerVersions(left, right string) (int, error) {
	leftParts, err := parseRunnerVersion(left)
	if err != nil {
		return 0, err
	}
	rightParts, err := parseRunnerVersion(right)
	if err != nil {
		return 0, err
	}
	for i := range leftParts {
		if leftParts[i] < rightParts[i] {
			return -1, nil
		}
		if leftParts[i] > rightParts[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func parseRunnerVersion(value string) ([4]int, error) {
	var result [4]int
	base := strings.TrimSuffix(value, "-Release")
	if base == value && strings.Contains(value, "-") {
		return result, fmt.Errorf("runner version %q has unsupported suffix", value)
	}
	parts := strings.Split(base, ".")
	if len(parts) != len(result) {
		return result, fmt.Errorf("runner version %q must use YY.MM.DD.NN[-Release]", value)
	}
	for i, part := range parts {
		if len(part) != 2 || part[0] < '0' || part[0] > '9' || part[1] < '0' || part[1] > '9' {
			return result, fmt.Errorf("runner version %q must use YY.MM.DD.NN[-Release]", value)
		}
		result[i] = int(part[0]-'0')*10 + int(part[1]-'0')
	}
	return result, nil
}
