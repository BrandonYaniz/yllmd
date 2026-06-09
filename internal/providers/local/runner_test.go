package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
	"github.com/BrandonYaniz/yllmd/internal/providers"
)

func TestRunnerProviderGenerateCompact(t *testing.T) {
	runnerPath := writeFakeRunner(t)
	cfg := config.Config{
		Paths: config.PathsConfig{ModelDir: t.TempDir()},
		ModelLifecycle: config.ModelLifecycleConfig{
			ResidentModel: "fast",
		},
		LocalModels: map[string]config.LocalModelConfig{
			"fast": {
				Tier:      "fast",
				ModelPath: filepath.Join(t.TempDir(), "model.gguf"),
				Backend: config.LocalBackendConfig{
					Type:      "process",
					Command:   runnerPath,
					Transport: "stdio",
				},
				Runtime: config.LocalRuntimeSettings{
					ContextTokens: 1024,
					Threads:       2,
				},
			},
		},
	}
	provider := NewRunnerProvider(cfg, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, err := provider.Generate(ctx, providers.GenerateRequest{
		ID:     "req-1",
		Model:  "fast",
		Stream: false,
		Input:  protocol.Input{Kind: "prompt", Prompt: "hello"},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	var sawStarted bool
	var completed protocol.Event
	for event := range events {
		switch event.Type {
		case "started":
			sawStarted = true
		case "completed":
			completed = event
		case "error":
			t.Fatalf("unexpected error event: %#v", event)
		}
	}
	if !sawStarted {
		t.Fatal("expected started event")
	}
	if completed.Type != "completed" {
		t.Fatalf("expected completed event, got %#v", completed)
	}
	if completed.Text != "fake runner response" {
		t.Fatalf("completed text = %q", completed.Text)
	}
	if completed.Usage == nil || completed.Usage.OutputTokens != 3 {
		t.Fatalf("unexpected usage: %#v", completed.Usage)
	}
}

func writeFakeRunner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-runner.sh")
	script := `#!/bin/sh
printf '%s\n' '{"type":"hello","protocol_version":1,"runner":"yllama-runner","capabilities":["generate","stream","cancel"]}'
while IFS= read -r line; do
  case "$line" in
    *'"type":"configure"'*)
      printf '%s\n' '{"type":"ready","id":"configure-req-1","model_path":"/tmp/model.gguf","context_tokens":1024}'
      ;;
    *'"type":"generate"'*)
      printf '%s\n' '{"type":"started","id":"req-1"}'
      printf '%s\n' '{"type":"completed","id":"req-1","finish_reason":"stop","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"text":"fake runner response"}'
      ;;
    *'"type":"shutdown"'*)
      exit 0
      ;;
    *'"type":"cancel"'*)
      printf '%s\n' '{"type":"cancelled","id":"req-1"}'
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake runner: %v", err)
	}
	return path
}
