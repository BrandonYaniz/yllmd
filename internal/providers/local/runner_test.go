package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		if err := provider.Close(closeCtx); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}()
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

func TestRunnerProviderReusesSessionForSameModel(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	t.Setenv("YLLMD_FAKE_RUNNER_LOG", logPath)
	runnerPath := writeFakeRunner(t)
	cfg := runnerTestConfig(t, runnerPath)
	provider := NewRunnerProvider(cfg, nil)
	defer closeProvider(t, provider)

	drainGenerate(t, provider, "req-1", "first")
	drainGenerate(t, provider, "req-2", "second")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read runner log: %v", err)
	}
	log := string(data)
	if count := strings.Count(log, "configure "); count != 1 {
		t.Fatalf("configure count = %d, log:\n%s", count, log)
	}
	if count := strings.Count(log, "generate "); count != 2 {
		t.Fatalf("generate count = %d, log:\n%s", count, log)
	}
}

func TestRunnerProviderCooldownReloadsResidentModel(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	t.Setenv("YLLMD_FAKE_RUNNER_LOG", logPath)
	runnerPath := writeFakeRunner(t)
	cfg := runnerTestConfig(t, runnerPath)
	cfg.ModelLifecycle.IdleCooldown = 25 * time.Millisecond
	cfg.LocalModels["deep"] = config.LocalModelConfig{
		Tier:      "deep",
		ModelPath: filepath.Join(t.TempDir(), "deep.gguf"),
		Backend: config.LocalBackendConfig{
			Type:      "process",
			Command:   runnerPath,
			Transport: "stdio",
		},
		Runtime: config.LocalRuntimeSettings{
			ContextTokens: 2048,
			Threads:       4,
		},
	}
	provider := NewRunnerProvider(cfg, nil)
	defer closeProvider(t, provider)

	drainGenerateModel(t, provider, "req-deep", "deep", "use deep")
	waitForLog(t, logPath, "configure configure-idle-resident")
}

func writeFakeRunner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-runner.sh")
	script := `#!/bin/sh
log_event() {
  if [ -n "$YLLMD_FAKE_RUNNER_LOG" ]; then
    printf '%s\n' "$1" >> "$YLLMD_FAKE_RUNNER_LOG"
  fi
}
extract_id() {
  printf '%s\n' "$1" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p'
}
printf '%s\n' '{"type":"hello","protocol_version":1,"runner":"yllama-runner","capabilities":["generate","stream","cancel"]}'
while IFS= read -r line; do
  id=$(extract_id "$line")
  case "$line" in
    *'"type":"configure"'*)
      log_event "configure $id"
      printf '%s\n' "{\"type\":\"ready\",\"id\":\"$id\",\"model_path\":\"/tmp/model.gguf\",\"context_tokens\":1024}"
      ;;
    *'"type":"generate"'*)
      log_event "generate $id"
      printf '%s\n' "{\"type\":\"started\",\"id\":\"$id\"}"
      printf '%s\n' "{\"type\":\"completed\",\"id\":\"$id\",\"finish_reason\":\"stop\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5},\"text\":\"fake runner response\"}"
      ;;
    *'"type":"shutdown"'*)
      log_event "shutdown $id"
      exit 0
      ;;
    *'"type":"cancel"'*)
      log_event "cancel $id"
      printf '%s\n' "{\"type\":\"cancelled\",\"id\":\"$id\"}"
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake runner: %v", err)
	}
	return path
}

func runnerTestConfig(t *testing.T, runnerPath string) config.Config {
	t.Helper()
	return config.Config{
		Paths: config.PathsConfig{ModelDir: t.TempDir()},
		ModelLifecycle: config.ModelLifecycleConfig{
			ResidentModel: "fast",
			IdleCooldown:  time.Hour,
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
}

func closeProvider(t *testing.T, provider *RunnerProvider) {
	t.Helper()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := provider.Close(closeCtx); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func drainGenerate(t *testing.T, provider *RunnerProvider, id, prompt string) {
	t.Helper()
	drainGenerateModel(t, provider, id, "fast", prompt)
}

func drainGenerateModel(t *testing.T, provider *RunnerProvider, id, model, prompt string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, err := provider.Generate(ctx, providers.GenerateRequest{
		ID:     id,
		Model:  model,
		Stream: false,
		Input:  protocol.Input{Kind: "prompt", Prompt: prompt},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	var completed bool
	for event := range events {
		if event.Type == "error" {
			t.Fatalf("unexpected error event: %#v", event)
		}
		if event.Type == "completed" {
			completed = true
		}
	}
	if !completed {
		t.Fatal("expected completed event")
	}
}

func waitForLog(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("timed out waiting for %q in log:\n%s", want, string(data))
}
