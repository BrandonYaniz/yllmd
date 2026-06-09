package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDecodeGenerateRequest(t *testing.T) {
	req, err := DecodeRequest([]byte(`{"type":"generate","id":"req-1","provider":"local","model":"fast","input":{"kind":"prompt","prompt":"hello"},"settings":{"max_tokens":12}}`))
	if err != nil {
		t.Fatalf("DecodeRequest returned error: %v", err)
	}
	if req.Type != MessageGenerate {
		t.Fatalf("type = %q, want %q", req.Type, MessageGenerate)
	}
	if req.Input == nil || req.Input.Prompt != "hello" {
		t.Fatalf("unexpected input: %#v", req.Input)
	}
	if req.Settings.MaxTokens == nil || *req.Settings.MaxTokens != 12 {
		t.Fatalf("unexpected max tokens: %#v", req.Settings.MaxTokens)
	}
}

func TestDecodeRequiresID(t *testing.T) {
	if _, err := DecodeRequest([]byte(`{"type":"health"}`)); err == nil {
		t.Fatal("expected missing id error")
	}
}

func TestValidateGenerateAcceptsMessages(t *testing.T) {
	req := Request{
		Type: MessageGenerate,
		ID:   "req-1",
		Input: &Input{
			Kind: "messages",
			Messages: []Message{
				{Role: "system", Content: "Answer clearly."},
				{Role: "user", Content: "Hello."},
				{Role: "assistant", Content: "Hi."},
			},
		},
		Queue: QueueOptions{Policy: "wait", TimeoutMS: 1000},
	}
	if err := req.ValidateGenerate(); err != nil {
		t.Fatalf("ValidateGenerate returned error: %v", err)
	}
}

func TestValidateGenerateRejectsInvalidRequests(t *testing.T) {
	temperature := -1.0
	topP := 1.1
	maxTokens := 0
	tests := []struct {
		name string
		req  Request
	}{
		{
			name: "missing input",
			req:  Request{Type: MessageGenerate, ID: "req-1"},
		},
		{
			name: "empty prompt",
			req: Request{
				Type:  MessageGenerate,
				ID:    "req-1",
				Input: &Input{Kind: "prompt", Prompt: " "},
			},
		},
		{
			name: "empty messages",
			req: Request{
				Type:  MessageGenerate,
				ID:    "req-1",
				Input: &Input{Kind: "messages"},
			},
		},
		{
			name: "bad role",
			req: Request{
				Type: MessageGenerate,
				ID:   "req-1",
				Input: &Input{
					Kind:     "messages",
					Messages: []Message{{Role: "tool", Content: "reserved"}},
				},
			},
		},
		{
			name: "negative temperature",
			req: Request{
				Type:     MessageGenerate,
				ID:       "req-1",
				Input:    &Input{Kind: "prompt", Prompt: "hello"},
				Settings: GenerationSettings{Temperature: &temperature},
			},
		},
		{
			name: "bad top p",
			req: Request{
				Type:     MessageGenerate,
				ID:       "req-1",
				Input:    &Input{Kind: "prompt", Prompt: "hello"},
				Settings: GenerationSettings{TopP: &topP},
			},
		},
		{
			name: "bad max tokens",
			req: Request{
				Type:     MessageGenerate,
				ID:       "req-1",
				Input:    &Input{Kind: "prompt", Prompt: "hello"},
				Settings: GenerationSettings{MaxTokens: &maxTokens},
			},
		},
		{
			name: "bad queue policy",
			req: Request{
				Type:  MessageGenerate,
				ID:    "req-1",
				Input: &Input{Kind: "prompt", Prompt: "hello"},
				Queue: QueueOptions{Policy: "drop"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.req.ValidateGenerate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestWriteEvent(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEvent(&buf, Event{Type: "health", ID: "health-1", Status: "ok"})
	if err != nil {
		t.Fatalf("WriteEvent returned error: %v", err)
	}
	var event Event
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &event); err != nil {
		t.Fatalf("event is not JSON: %v", err)
	}
	if event.Type != "health" || event.Status != "ok" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestWriteModelsEvent(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEvent(&buf, Event{
		Type: "models",
		ID:   "models-1",
		Models: []ModelDescriptor{
			{
				ID:          ModelID{Provider: "local", Name: "fast"},
				Name:        "fast",
				DisplayName: "fast",
				Tier:        "fast",
				Resident:    true,
				Capabilities: ModelCapabilities{
					SupportsStreaming:        true,
					SupportsLocalPreparation: true,
					ContextWindow:            1024,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("WriteEvent returned error: %v", err)
	}
	var event Event
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &event); err != nil {
		t.Fatalf("event is not JSON: %v", err)
	}
	if len(event.Models) != 1 {
		t.Fatalf("model count = %d", len(event.Models))
	}
	if event.Models[0].ID.Provider != "local" || event.Models[0].ID.Name != "fast" {
		t.Fatalf("unexpected model id: %#v", event.Models[0].ID)
	}
}

func TestWriteStatusEvent(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEvent(&buf, Event{
		Type:   "status",
		ID:     "status-1",
		Status: "ok",
		Daemon: &DaemonStatus{
			Status:      "ok",
			Provider:    "local",
			LoadedModel: "fast",
			QueueDepth:  1,
			ModelCount:  3,
		},
	})
	if err != nil {
		t.Fatalf("WriteEvent returned error: %v", err)
	}
	var event Event
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &event); err != nil {
		t.Fatalf("event is not JSON: %v", err)
	}
	if event.Daemon == nil {
		t.Fatal("expected daemon status")
	}
	if event.Daemon.LoadedModel != "fast" || event.Daemon.ModelCount != 3 {
		t.Fatalf("unexpected daemon status: %#v", event.Daemon)
	}
}
