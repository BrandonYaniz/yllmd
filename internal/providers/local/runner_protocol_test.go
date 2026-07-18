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
	if payload[0] != runnerTokenizationFormatted || payload[1] != 0 {
		t.Fatalf("mode/reserved = %v", payload[:2])
	}
	if got := binary.LittleEndian.Uint16(payload[2:4]); got != 2 {
		t.Fatalf("stop count = %d", got)
	}
	if got := binary.LittleEndian.Uint32(payload[4:8]); got != 321 {
		t.Fatalf("max tokens = %d", got)
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(payload[8:16])); got != 0.25 {
		t.Fatalf("temperature = %g", got)
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(payload[16:24])); got != 0.9 {
		t.Fatalf("top_p = %g", got)
	}
	if got := int32(binary.LittleEndian.Uint32(payload[24:28])); got != 17 {
		t.Fatalf("top_k = %d", got)
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(payload[28:36])); got != 0.05 {
		t.Fatalf("min_p = %g", got)
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(payload[36:44])); got != -0.2 {
		t.Fatalf("presence penalty = %g", got)
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(payload[44:52])); got != 1.1 {
		t.Fatalf("repeat penalty = %g", got)
	}
	if got := binary.LittleEndian.Uint64(payload[52:60]); got != 42 {
		t.Fatalf("seed = %d", got)
	}
	offset := 60
	for _, want := range []string{"END", "STOP", "hello"} {
		length := int(binary.LittleEndian.Uint32(payload[offset : offset+4]))
		offset += 4
		if got := string(payload[offset : offset+length]); got != want {
			t.Fatalf("string = %q, want %q", got, want)
		}
		offset += length
	}
	if offset != len(payload) {
		t.Fatalf("decoded %d of %d bytes", offset, len(payload))
	}
}

func TestReadRunnerProtocolFrames(t *testing.T) {
	var stream bytes.Buffer
	version := runnerProtocolMinimum
	var ready bytes.Buffer
	writeUint16(&ready, runnerProtocolVersion)
	writeUint16(&ready, uint16(len(version)))
	ready.WriteString(version)
	writeUint32(&ready, 4096)
	writeUint64(&ready, runnerRequiredCapabilities)
	if err := writeRunnerEnvelope(&stream, runnerFrameReady, ready.Bytes()); err != nil {
		t.Fatal(err)
	}
	var chunk bytes.Buffer
	writeUint32(&chunk, 2)
	chunk.WriteString("ok")
	if err := writeRunnerEnvelope(&stream, runnerFrameChunk, chunk.Bytes()); err != nil {
		t.Fatal(err)
	}
	var completed bytes.Buffer
	completed.WriteByte(2)
	writeUint32(&completed, 11)
	writeUint32(&completed, 7)
	writeUint64(&completed, 100)
	writeUint64(&completed, 200)
	if err := writeRunnerEnvelope(&stream, runnerFrameCompleted, completed.Bytes()); err != nil {
		t.Fatal(err)
	}
	var protocolError bytes.Buffer
	writeUint16(&protocolError, uint16(len("busy")))
	protocolError.WriteString("busy")
	writeUint16(&protocolError, uint16(len("try later")))
	protocolError.WriteString("try later")
	if err := writeRunnerEnvelope(&stream, runnerFrameError, protocolError.Bytes()); err != nil {
		t.Fatal(err)
	}

	frame, err := readRunnerProtocolFrame(&stream)
	if err != nil || frame.ready.version != version || frame.ready.contextSize != 4096 {
		t.Fatalf("Ready = %#v, %v", frame, err)
	}
	frame, err = readRunnerProtocolFrame(&stream)
	if err != nil || frame.chunk != "ok" {
		t.Fatalf("Chunk = %#v, %v", frame, err)
	}
	frame, err = readRunnerProtocolFrame(&stream)
	if err != nil || frame.completed.finishReason != 2 || frame.completed.inputTokens != 11 || frame.completed.outputTokens != 7 || frame.completed.promptMicroseconds != 100 || frame.completed.generationMicroseconds != 200 {
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
	if _, err := decodeRunnerChunk([]byte{2, 0, 0, 0, 'x'}); err == nil {
		t.Fatal("expected length error")
	}
	if _, err := decodeRunnerChunk([]byte{1, 0, 0, 0, 0xff}); err == nil {
		t.Fatal("expected UTF-8 error")
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

func TestValidateRunnerReady(t *testing.T) {
	valid := runnerReady{protocol: 2, version: runnerProtocolMinimum, contextSize: 4096, capabilities: runnerRequiredCapabilities}
	if err := validateRunnerReady(valid, 4096); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		ready runnerReady
		want  string
	}{
		{"protocol", runnerReady{protocol: 1, version: valid.version, contextSize: 4096, capabilities: valid.capabilities}, "protocol"},
		{"capabilities", runnerReady{protocol: 2, version: valid.version, contextSize: 4096}, "capabilities"},
		{"context", runnerReady{protocol: 2, version: valid.version, contextSize: 2048, capabilities: valid.capabilities}, "smaller"},
		{"version", runnerReady{protocol: 2, version: "26.07.15.99-Release", contextSize: 4096, capabilities: valid.capabilities}, "older"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateRunnerReady(test.ready, 4096); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCompareRunnerVersions(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"26.07.16.01-Release", "26.07.16.01", 0},
		{"26.07.17.01-Release", "26.07.16.99-Release", 1},
		{"25.12.31.99", "26.01.01.01", -1},
	} {
		got, err := compareRunnerVersions(test.left, test.right)
		if err != nil || got != test.want {
			t.Fatalf("compare(%q, %q) = %d, %v; want %d", test.left, test.right, got, err, test.want)
		}
	}
	if _, err := compareRunnerVersions("v2", runnerProtocolMinimum); err == nil {
		t.Fatal("expected malformed version error")
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
