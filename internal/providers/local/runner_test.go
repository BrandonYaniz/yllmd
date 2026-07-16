package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/models"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
	"github.com/BrandonYaniz/yllmd/internal/providers"
)

func TestRunnerProviderGenerateCompact(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	t.Setenv("YLLMD_FAKE_RUNNER_LOG", logPath)
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
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read runner log: %v", err)
	}
	log := string(data)
	for _, want := range []string{"--model", "--ctx=1024", "--threads=2", "--max-tokens=128", "--temperature=0.8", "--top-p=0.95", "prompt hello"} {
		if !strings.Contains(log, want) {
			t.Fatalf("runner log missing %q:\n%s", want, log)
		}
	}
}

func TestRunnerPromptAppliesQwenChatTemplate(t *testing.T) {
	model := models.LocalModel{Config: config.LocalModelConfig{CatalogID: "qwen25-coder-1.5b-instruct"}}
	prompt, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: "Write hello world."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "<|im_start|>system\nBe concise.<|im_end|>\n<|im_start|>user\nWrite hello world.<|im_end|>\n<|im_start|>assistant\n"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestRunnerPromptLeavesDirectPromptUnchanged(t *testing.T) {
	model := models.LocalModel{Config: config.LocalModelConfig{CatalogID: "qwen25-coder-1.5b-instruct"}}
	prompt, err := runnerPrompt(model, protocol.Input{Kind: "prompt", Prompt: "raw prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if prompt != "raw prompt" {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestRunnerPromptAppliesPhi4ChatTemplate(t *testing.T) {
	model := models.LocalModel{Config: config.LocalModelConfig{CatalogID: "phi4-mini-instruct"}}
	prompt, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: "Hello."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "<|system|>Be concise.<|end|><|user|>Hello.<|end|><|assistant|>"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestRunnerPromptAppliesGemma3ChatTemplate(t *testing.T) {
	model := models.LocalModel{Config: config.LocalModelConfig{CatalogID: "gemma3-1b-it"}}
	prompt, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: "First question."},
		{Role: "assistant", Content: "First answer."},
		{Role: "user", Content: "Second question."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "<bos><start_of_turn>user\nBe concise.\n\nFirst question.<end_of_turn>\n" +
		"<start_of_turn>model\nFirst answer.<end_of_turn>\n" +
		"<start_of_turn>user\nSecond question.<end_of_turn>\n<start_of_turn>model\n"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestRunnerPromptRejectsInvalidGemma3RoleOrder(t *testing.T) {
	model := models.LocalModel{Config: config.LocalModelConfig{CatalogID: "gemma3-1b-it"}}
	_, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "assistant", Content: "Wrong first role."},
	}})
	if err == nil || !strings.Contains(err.Error(), "must alternate") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunnerPromptAppliesLlama3InstructTemplate(t *testing.T) {
	model := models.LocalModel{Config: config.LocalModelConfig{CatalogID: "llama32-1b-instruct"}}
	prompt, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: "Hello."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "<|begin_of_text|><|start_header_id|>system<|end_header_id|>\n\nBe concise.<|eot_id|>" +
		"<|start_header_id|>user<|end_header_id|>\n\nHello.<|eot_id|>" +
		"<|start_header_id|>assistant<|end_header_id|>\n\n"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestRunnerPromptAppliesGranite3ChatTemplate(t *testing.T) {
	model := models.LocalModel{Config: config.LocalModelConfig{CatalogID: "granite3.3-2b-instruct"}}
	prompt, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: "Hello."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "<|start_of_role|>system<|end_of_role|>Be concise.<|end_of_text|>\n" +
		"<|start_of_role|>user<|end_of_role|>Hello.<|end_of_text|>\n" +
		"<|start_of_role|>assistant<|end_of_role|>"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestGranite3ChatPromptAddsDefaultSystemMessage(t *testing.T) {
	prompt, err := granite3ChatPrompt([]protocol.Message{{Role: "user", Content: "Hello."}}, time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	want := "<|start_of_role|>system<|end_of_role|> Knowledge Cutoff Date: April 2024.\n" +
		" Today's Date: July 15, 2026. You are Granite, developed by IBM. You are a helpful AI assistant.<|end_of_text|>\n" +
		"<|start_of_role|>user<|end_of_role|>Hello.<|end_of_text|>\n" +
		"<|start_of_role|>assistant<|end_of_role|>"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
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
	if count := strings.Count(log, "start "); count != 1 {
		t.Fatalf("start count = %d, log:\n%s", count, log)
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
	waitForLog(t, logPath, "start model.gguf")
}

func TestRunnerProviderRestartsForDifferentGenerationSettings(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	t.Setenv("YLLMD_FAKE_RUNNER_LOG", logPath)
	runnerPath := writeFakeRunner(t)
	cfg := runnerTestConfig(t, runnerPath)
	provider := NewRunnerProvider(cfg, nil)
	defer closeProvider(t, provider)

	drainGenerateWithSettings(t, provider, "req-1", "first", protocol.GenerationSettings{})
	maxTokens := 64
	drainGenerateWithSettings(t, provider, "req-2", "second", protocol.GenerationSettings{MaxTokens: &maxTokens})

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read runner log: %v", err)
	}
	log := string(data)
	if count := strings.Count(log, "start "); count != 2 {
		t.Fatalf("start count = %d, log:\n%s", count, log)
	}
	if !strings.Contains(log, "--max-tokens=64") {
		t.Fatalf("runner did not restart with requested max tokens:\n%s", log)
	}
}

func TestRunnerProviderAppliesStopSequences(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	t.Setenv("YLLMD_FAKE_RUNNER_LOG", logPath)
	t.Setenv("YLLMD_FAKE_RUNNER_RESPONSE", "before STOP after")
	runnerPath := writeFakeRunner(t)
	cfg := runnerTestConfig(t, runnerPath)
	provider := NewRunnerProvider(cfg, nil)
	defer closeProvider(t, provider)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, err := provider.Generate(ctx, providers.GenerateRequest{
		ID:       "req-stop",
		Model:    "fast",
		Stream:   true,
		Input:    protocol.Input{Kind: "prompt", Prompt: "stop test"},
		Settings: protocol.GenerationSettings{Stop: []string{"STOP"}},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	var completed protocol.Event
	var streamed strings.Builder
	for event := range events {
		switch event.Type {
		case "delta":
			streamed.WriteString(event.Text)
		case "completed":
			completed = event
		case "error":
			t.Fatalf("unexpected error event: %#v", event)
		}
	}
	if completed.Text != "before " {
		t.Fatalf("completed text = %q", completed.Text)
	}
	if streamed.String() != "before " {
		t.Fatalf("streamed text = %q", streamed.String())
	}
}

func writeFakeRunner(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "fake_runner.go")
	binary := filepath.Join(dir, "fake-runner")
	program := `package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func logEvent(format string, args ...any) {
	path := os.Getenv("YLLMD_FAKE_RUNNER_LOG")
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, format+"\n", args...)
}

func flagValue(args []string, name string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func main() {
	args := os.Args[1:]
	response := os.Getenv("YLLMD_FAKE_RUNNER_RESPONSE")
	if response == "" {
		response = "fake runner response"
	}
	logEvent("start %s --model %s --ctx=%s --threads=%s --max-tokens=%s --temperature=%s --top-p=%s",
		filepath.Base(flagValue(args, "--model")),
		flagValue(args, "--model"),
		flagValue(args, "--ctx"),
		flagValue(args, "--threads"),
		flagValue(args, "--max-tokens"),
		flagValue(args, "--temperature"),
		flagValue(args, "--top-p"))

	for {
		var lengthBytes [4]byte
		if _, err := io.ReadFull(os.Stdin, lengthBytes[:]); err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				logEvent("read_error %v", err)
			}
			return
		}
		length := binary.LittleEndian.Uint32(lengthBytes[:])
		prompt := make([]byte, length)
		if _, err := io.ReadFull(os.Stdin, prompt); err != nil {
			logEvent("read_error %v", err)
			return
		}
		logEvent("generate %s", strings.TrimSpace(string(prompt)))
		logEvent("prompt %s", string(prompt))
		writeChunk(response)
		os.Stdout.Write([]byte{0x02})
	}
}

func writeChunk(text string) {
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(text)))
	os.Stdout.Write([]byte{0x01})
	os.Stdout.Write(length[:])
	os.Stdout.Write([]byte(text))
}
`
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatalf("write fake runner source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", binary, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake runner: %v\n%s", err, output)
	}
	return binary
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
	drainGenerateWithSettings(t, provider, id, prompt, protocol.GenerationSettings{})
}

func drainGenerateModel(t *testing.T, provider *RunnerProvider, id, model, prompt string) {
	t.Helper()
	drainGenerateModelWithSettings(t, provider, id, model, prompt, protocol.GenerationSettings{})
}

func drainGenerateWithSettings(t *testing.T, provider *RunnerProvider, id, prompt string, settings protocol.GenerationSettings) {
	t.Helper()
	drainGenerateModelWithSettings(t, provider, id, "fast", prompt, settings)
}

func drainGenerateModelWithSettings(t *testing.T, provider *RunnerProvider, id, model, prompt string, settings protocol.GenerationSettings) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, err := provider.Generate(ctx, providers.GenerateRequest{
		ID:       id,
		Model:    model,
		Stream:   false,
		Input:    protocol.Input{Kind: "prompt", Prompt: prompt},
		Settings: settings,
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
