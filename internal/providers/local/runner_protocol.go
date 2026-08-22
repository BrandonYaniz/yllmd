package local

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

const (
	runnerMaxFrameBytes         = 32 << 20
	runnerMaxPromptBytes        = 16 << 20
	runnerMaxStopCount          = 64
	runnerMaxStopBytes          = 64 << 10
	runnerTokenizationRaw       = byte(0)
	runnerTokenizationFormatted = byte(1)

	runnerMessageGenerate = byte(0x01)
	runnerMessageCancel   = byte(0x02)

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

type runnerCompletion struct {
	finishReason byte
	inputTokens  uint32
	outputTokens uint32
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
	payload.WriteByte(byte(len(request.stops)))
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
	payload.WriteString(request.prompt)
	if payload.Len() > runnerMaxFrameBytes {
		return nil, fmt.Errorf("runner Generate payload exceeds %d-byte limit", runnerMaxFrameBytes)
	}
	return payload.Bytes(), nil
}

func writeRunnerControl(writer io.Writer, messageType byte) error {
	if messageType != runnerMessageCancel {
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
		if len(payload) != 0 {
			return runnerFrame{}, fmt.Errorf("runner Ready length %d, want 0", len(payload))
		}
		return runnerFrame{tag: header[0]}, nil
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

func decodeRunnerChunk(payload []byte) (string, error) {
	chunk := string(payload)
	if !utf8.ValidString(chunk) {
		return "", errors.New("runner Chunk is not valid UTF-8")
	}
	return chunk, nil
}

func decodeRunnerCompleted(payload []byte) (runnerCompletion, error) {
	if len(payload) != 9 {
		return runnerCompletion{}, fmt.Errorf("runner Completed length %d, want 9", len(payload))
	}
	if payload[0] > 3 {
		return runnerCompletion{}, fmt.Errorf("unknown runner finish reason %d", payload[0])
	}
	return runnerCompletion{
		finishReason: payload[0],
		inputTokens:  binary.LittleEndian.Uint32(payload[1:5]),
		outputTokens: binary.LittleEndian.Uint32(payload[5:9]),
	}, nil
}

func decodeRunnerError(payload []byte) (runnerProtocolError, error) {
	if len(payload) < 2 {
		return runnerProtocolError{}, errors.New("truncated runner Error payload")
	}
	codeLength := int(binary.LittleEndian.Uint16(payload[:2]))
	if len(payload) < 2+codeLength {
		return runnerProtocolError{}, errors.New("truncated runner Error code")
	}
	code := string(payload[2 : 2+codeLength])
	message := string(payload[2+codeLength:])
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
