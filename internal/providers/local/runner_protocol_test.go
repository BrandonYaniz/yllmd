package local

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

func TestEncodeRunnerGenerateLayout(t *testing.T) {
	payload, err := encodeRunnerGenerate(runnerGenerate{
		prompt:           "hello",
		tokenizationMode: runnerTokenizationFormatted,
		maxTokens:        321,
		temperature:      0.25,
		topP:             0.9,
		topK:             17,
		minP:             0.05,
		presencePenalty:  -0.2,
		repeatPenalty:    1.1,
		seed:             42,
		stops:            []string{"END", "STOP"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload[0] != runnerTokenizationFormatted || payload[1] != 2 {
		t.Fatalf("mode/stop count = %v", payload[:2])
	}
	if got := binary.LittleEndian.Uint32(payload[2:6]); got != 321 {
		t.Fatalf("max tokens = %d", got)
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(payload[6:14])); got != 0.25 {
		t.Fatalf("temperature = %g", got)
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(payload[14:22])); got != 0.9 {
		t.Fatalf("top_p = %g", got)
	}
	if got := int32(binary.LittleEndian.Uint32(payload[22:26])); got != 17 {
		t.Fatalf("top_k = %d", got)
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(payload[26:34])); got != 0.05 {
		t.Fatalf("min_p = %g", got)
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(payload[34:42])); got != -0.2 {
		t.Fatalf("presence penalty = %g", got)
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(payload[42:50])); got != 1.1 {
		t.Fatalf("repeat penalty = %g", got)
	}
	if got := binary.LittleEndian.Uint64(payload[50:58]); got != 42 {
		t.Fatalf("seed = %d", got)
	}
	offset := 58
	for _, want := range []string{"END", "STOP"} {
		length := int(binary.LittleEndian.Uint32(payload[offset : offset+4]))
		offset += 4
		if got := string(payload[offset : offset+length]); got != want {
			t.Fatalf("string = %q, want %q", got, want)
		}
		offset += length
	}
	if got := string(payload[offset:]); got != "hello" {
		t.Fatalf("prompt = %q, want hello", got)
	}
	if offset+len("hello") != len(payload) {
		t.Fatalf("decoded %d of %d bytes", offset+len("hello"), len(payload))
	}
}

func TestReadRunnerProtocolFrames(t *testing.T) {
	var stream bytes.Buffer
	if err := writeRunnerEnvelope(&stream, runnerFrameReady, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerEnvelope(&stream, runnerFrameChunk, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	var completed bytes.Buffer
	completed.WriteByte(2)
	writeUint32(&completed, 11)
	writeUint32(&completed, 7)
	if err := writeRunnerEnvelope(&stream, runnerFrameCompleted, completed.Bytes()); err != nil {
		t.Fatal(err)
	}
	var protocolError bytes.Buffer
	writeUint16(&protocolError, uint16(len("busy")))
	protocolError.WriteString("busy")
	protocolError.WriteString("try later")
	if err := writeRunnerEnvelope(&stream, runnerFrameError, protocolError.Bytes()); err != nil {
		t.Fatal(err)
	}

	frame, err := readRunnerProtocolFrame(&stream)
	if err != nil || frame.tag != runnerFrameReady {
		t.Fatalf("Ready = %#v, %v", frame, err)
	}
	frame, err = readRunnerProtocolFrame(&stream)
	if err != nil || frame.chunk != "ok" {
		t.Fatalf("Chunk = %#v, %v", frame, err)
	}
	frame, err = readRunnerProtocolFrame(&stream)
	if err != nil || frame.completed.finishReason != 2 || frame.completed.inputTokens != 11 || frame.completed.outputTokens != 7 {
		t.Fatalf("Completed = %#v, %v", frame, err)
	}
	frame, err = readRunnerProtocolFrame(&stream)
	if err != nil || frame.runnerError == nil || frame.runnerError.code != "busy" || frame.runnerError.message != "try later" {
		t.Fatalf("Error = %#v, %v", frame, err)
	}
}

func TestReadRunnerProtocolFrameRejectsOversizeBeforePayloadRead(t *testing.T) {
	header := []byte{runnerFrameChunk, 1, 0, 0, 2}
	_, err := readRunnerProtocolFrame(bytes.NewReader(header))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRunnerChunkRejectsInvalidData(t *testing.T) {
	if _, err := decodeRunnerChunk([]byte{0xff}); err == nil {
		t.Fatal("expected UTF-8 error")
	}
}

func TestReadRunnerReadyRejectsPayload(t *testing.T) {
	var stream bytes.Buffer
	if err := writeRunnerEnvelope(&stream, runnerFrameReady, []byte{0}); err != nil {
		t.Fatal(err)
	}
	if _, err := readRunnerProtocolFrame(&stream); err == nil || !strings.Contains(err.Error(), "want 0") {
		t.Fatalf("error = %v", err)
	}
}

func TestWriteRunnerEnvelopeHandlesShortWrites(t *testing.T) {
	writer := &oneByteWriter{}
	if err := writeRunnerEnvelope(writer, runnerMessageCancel, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	want := []byte{runnerMessageCancel, 3, 0, 0, 0, 'a', 'b', 'c'}
	if !bytes.Equal(writer.data, want) {
		t.Fatalf("envelope = %v, want %v", writer.data, want)
	}

	err := writeAll(zeroWriter{}, []byte("x"))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v", err)
	}
}

type oneByteWriter struct{ data []byte }

func (w *oneByteWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	w.data = append(w.data, data[0])
	return 1, nil
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }
