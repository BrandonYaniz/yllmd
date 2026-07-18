package protocol

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestDecodeGenerateRequest(t *testing.T) {
	req, err := DecodeRequest([]byte(`{"type":"generate","id":"req-1","provider":"local","model_type":"code","level":"balanced","input":{"kind":"prompt","prompt":"hello"},"settings":{"max_tokens":12}}`))
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
	if req.ModelType != "code" || req.Level != "balanced" {
		t.Fatalf("unexpected routing fields: model_type=%q level=%q", req.ModelType, req.Level)
	}
}

func TestValidateGenerateAcceptsRunnerSamplingSettings(t *testing.T) {
	temperature, topP, minP := 0.2, 0.9, 0.05
	presence, repeat := -0.5, 1.1
	maxTokens, topK := 512, 40
	seed := uint64(42)
	req := Request{
		Type:  MessageGenerate,
		ID:    "req-settings",
		Input: &Input{Kind: "prompt", Prompt: "hello"},
		Settings: GenerationSettings{
			Temperature: &temperature, TopP: &topP, MaxTokens: &maxTokens,
			TopK: &topK, MinP: &minP, PresencePenalty: &presence,
			RepeatPenalty: &repeat, Seed: &seed, Stop: []string{"END"},
		},
	}
	if err := req.ValidateGenerate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGenerateRejectsRunnerSamplingBounds(t *testing.T) {
	nan := math.NaN()
	topK := -1
	minP := 1.1
	presence := 2.1
	repeat := 0.0
	seed := uint64(math.MaxUint32) + 1
	tests := []struct {
		name     string
		settings GenerationSettings
	}{
		{"nan temperature", GenerationSettings{Temperature: &nan}},
		{"negative top k", GenerationSettings{TopK: &topK}},
		{"large min p", GenerationSettings{MinP: &minP}},
		{"large presence penalty", GenerationSettings{PresencePenalty: &presence}},
		{"zero repeat penalty", GenerationSettings{RepeatPenalty: &repeat}},
		{"nonportable seed", GenerationSettings{Seed: &seed}},
		{"too many stops", GenerationSettings{Stop: make([]string, maximumGenerationStops+1)}},
		{"oversized stop", GenerationSettings{Stop: []string{strings.Repeat("x", maximumGenerationStopBytes+1)}}},
		{"invalid stop utf8", GenerationSettings{Stop: []string{string([]byte{0xff})}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := Request{Type: MessageGenerate, ID: "req", Input: &Input{Kind: "prompt", Prompt: "hello"}, Settings: test.settings}
			if err := req.ValidateGenerate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDecodeRequiresID(t *testing.T) {
	if _, err := DecodeRequest([]byte(`{"type":"health"}`)); err == nil {
		t.Fatal("expected missing id error")
	}
}

func TestValidateModelDownload(t *testing.T) {
	req := Request{Type: MessageModels, ID: "models-1", Action: "download", Model: "qwen25-coder-7b-instruct"}
	if err := req.ValidateModelDownload(); err != nil {
		t.Fatal(err)
	}
	req.Model = ""
	if err := req.ValidateModelDownload(); err == nil {
		t.Fatal("expected missing catalog variant error")
	}
}

func TestValidateModelUpdate(t *testing.T) {
	req := Request{Type: MessageModels, ID: "models-1", Action: "update", Model: "qwen-coder"}
	if err := req.ValidateModelUpdate(); err != nil {
		t.Fatalf("ValidateModelUpdate returned error: %v", err)
	}
}

func TestValidateModelDelete(t *testing.T) {
	req := Request{Type: MessageModels, ID: "models-1", Action: "delete", Model: "qwen25-coder-7b-instruct"}
	if err := req.ValidateModelDelete(); err != nil {
		t.Fatal(err)
	}
	req.Model = ""
	if err := req.ValidateModelDelete(); err == nil {
		t.Fatal("expected missing model error")
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
		Queue:    QueueOptions{Policy: "wait", TimeoutMS: 1000},
		Settings: GenerationSettings{Output: &Output{Format: "text", Delivery: "complete"}},
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
		{
			name: "bad legacy output format",
			req: Request{
				Type:         MessageGenerate,
				ID:           "req-1",
				Input:        &Input{Kind: "prompt", Prompt: "hello"},
				OutputFormat: "xml",
			},
		},
		{
			name: "bad settings output format",
			req: Request{
				Type:     MessageGenerate,
				ID:       "req-1",
				Input:    &Input{Kind: "prompt", Prompt: "hello"},
				Settings: GenerationSettings{Output: &Output{Format: "xml", Delivery: "stream"}},
			},
		},
		{
			name: "bad settings output delivery",
			req: Request{
				Type:     MessageGenerate,
				ID:       "req-1",
				Input:    &Input{Kind: "prompt", Prompt: "hello"},
				Settings: GenerationSettings{Output: &Output{Format: "json", Delivery: "later"}},
			},
		},
		{
			name: "bad model type",
			req: Request{
				Type:      MessageGenerate,
				ID:        "req-1",
				ModelType: "image",
				Input:     &Input{Kind: "prompt", Prompt: "hello"},
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

func TestValidateModelInstall(t *testing.T) {
	req := Request{
		Type:    MessageModels,
		ID:      "models-1",
		Action:  "install",
		Model:   "fast",
		Version: "v1",
		File:    "/tmp/model.gguf",
		SHA256:  "abc",
	}
	if err := req.ValidateModelInstall(); err != nil {
		t.Fatalf("ValidateModelInstall returned error: %v", err)
	}
}

func TestValidateModelInstallRejectsMissingFields(t *testing.T) {
	req := Request{Type: MessageModels, ID: "models-1", Action: "install", Model: "fast"}
	if err := req.ValidateModelInstall(); err == nil {
		t.Fatal("expected missing fields error")
	}
}

func TestValidateModelRollback(t *testing.T) {
	req := Request{Type: MessageModels, ID: "models-1", Action: "rollback", Model: "fast"}
	if err := req.ValidateModelRollback(); err != nil {
		t.Fatalf("ValidateModelRollback returned error: %v", err)
	}
}

func TestValidateModelRollbackRejectsMissingModel(t *testing.T) {
	req := Request{Type: MessageModels, ID: "models-1", Action: "rollback"}
	if err := req.ValidateModelRollback(); err == nil {
		t.Fatal("expected missing model error")
	}
}

func TestValidateModelActivate(t *testing.T) {
	req := Request{Type: MessageModels, ID: "models-1", Action: "activate", Model: "fast", Version: "v1"}
	if err := req.ValidateModelActivate(); err != nil {
		t.Fatalf("ValidateModelActivate returned error: %v", err)
	}
}

func TestValidateModelActivateRejectsMissingFields(t *testing.T) {
	req := Request{Type: MessageModels, ID: "models-1", Action: "activate", Model: "fast"}
	if err := req.ValidateModelActivate(); err == nil {
		t.Fatal("expected missing version error")
	}
}

func TestValidateModelVersions(t *testing.T) {
	req := Request{Type: MessageModels, ID: "models-1", Action: "versions", Model: "fast"}
	if err := req.ValidateModelVersions(); err != nil {
		t.Fatalf("ValidateModelVersions returned error: %v", err)
	}
}

func TestValidateModelVersionsRejectsMissingModel(t *testing.T) {
	req := Request{Type: MessageModels, ID: "models-1", Action: "versions"}
	if err := req.ValidateModelVersions(); err == nil {
		t.Fatal("expected missing model error")
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

func TestWriteVersionsEvent(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEvent(&buf, Event{
		Type:  "versions",
		ID:    "models-1",
		Model: "fast",
		Versions: []ModelVersion{
			{Version: "v1", Active: true, SHA256: "abc"},
		},
	})
	if err != nil {
		t.Fatalf("WriteEvent returned error: %v", err)
	}
	var event Event
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &event); err != nil {
		t.Fatalf("event is not JSON: %v", err)
	}
	if len(event.Versions) != 1 || event.Versions[0].Version != "v1" || !event.Versions[0].Active {
		t.Fatalf("unexpected versions event: %#v", event)
	}
}

func TestWriteAcceptedLicensesEvent(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEvent(&buf, Event{Type: "licenses", AcceptedLicenses: []AcceptedLicense{{
		FamilyID: "google-gemma", LicenseName: "Gemma Terms", TermsURL: "https://example.com/terms",
		CatalogVersion: "2026.07.4-draft", AcceptedAt: "2026-07-15T00:00:00Z",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if len(event.AcceptedLicenses) != 1 || event.AcceptedLicenses[0].FamilyID != "google-gemma" {
		t.Fatalf("event = %#v", event)
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
				ModelType:   "llm",
				Level:       "fast",
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
