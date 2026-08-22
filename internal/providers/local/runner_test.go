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
		Models: map[string]config.ModelConfig{
			"fast": {
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
	if completed.FinishReason != "eos" {
		t.Fatalf("finish reason = %q", completed.FinishReason)
	}
	if completed.Usage == nil || completed.Usage.InputTokens != 3 || completed.Usage.OutputTokens != 2 || completed.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %#v", completed.Usage)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read runner log: %v", err)
	}
	log := string(data)
	for _, want := range []string{"--model", "--ctx=1024", "--threads=2", "--gpu-layers=0", "generate mode=0", "max_tokens=128", "temperature=0.8", "top_p=0.95", "prompt hello"} {
		if !strings.Contains(log, want) {
			t.Fatalf("runner log missing %q:\n%s", want, log)
		}
	}
}

func TestRunnerPromptAppliesQwenChatTemplate(t *testing.T) {
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "qwen25-coder-1.5b-instruct"}}
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
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "qwen25-coder-1.5b-instruct"}}
	prompt, err := runnerPrompt(model, protocol.Input{Kind: "prompt", Prompt: "raw prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if prompt != "raw prompt" {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestRunnerPromptAppliesPhi4ChatTemplate(t *testing.T) {
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "phi4-mini-instruct"}}
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
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "gemma3-1b-it"}}
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
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "gemma3-1b-it"}}
	_, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "assistant", Content: "Wrong first role."},
	}})
	if err == nil || !strings.Contains(err.Error(), "must alternate") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunnerPromptAppliesLlama3InstructTemplate(t *testing.T) {
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "llama32-1b-instruct"}}
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
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "granite3.3-2b-instruct"}}
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

func TestRunnerPromptAppliesMistralNemoTemplate(t *testing.T) {
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "mistral-nemo-12b-instruct"}}
	prompt, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: "First question."},
		{Role: "assistant", Content: "First answer."},
		{Role: "user", Content: "Second question."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "<s>[INST]First question.[/INST]First answer.</s>" +
		"[INST]Be concise.\n\nSecond question.[/INST]"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestRunnerPromptRejectsInvalidMistralNemoRoleOrder(t *testing.T) {
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "mistral-nemo-12b-instruct"}}
	_, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "assistant", Content: "Wrong first role."},
	}})
	if err == nil || !strings.Contains(err.Error(), "must alternate") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunnerPromptAppliesQwen3NonThinkingTemplate(t *testing.T) {
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "qwen3-1.7b"}}
	prompt, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: "First question."},
		{Role: "assistant", Content: "First answer."},
		{Role: "user", Content: "Second question."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "<|im_start|>system\nBe concise.<|im_end|>\n" +
		"<|im_start|>user\nFirst question.<|im_end|>\n" +
		"<|im_start|>assistant\nFirst answer.<|im_end|>\n" +
		"<|im_start|>user\nSecond question.<|im_end|>\n" +
		"<|im_start|>assistant\n<think>\n\n</think>\n\n"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestRunnerPromptRejectsInvalidQwen3RoleOrder(t *testing.T) {
	model := models.LocalModel{Config: config.ModelConfig{CatalogID: "qwen3-1.7b"}}
	_, err := runnerPrompt(model, protocol.Input{Kind: "messages", Messages: []protocol.Message{
		{Role: "assistant", Content: "Wrong first role."},
	}})
	if err == nil || !strings.Contains(err.Error(), "must alternate") {
		t.Fatalf("error = %v", err)
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

func TestRunnerProviderCloseUsesStdinEOF(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	t.Setenv("YLLMD_FAKE_RUNNER_LOG", logPath)
	runnerPath := writeFakeRunner(t)
	provider := NewRunnerProvider(runnerTestConfig(t, runnerPath), nil)

	drainGenerate(t, provider, "req-close", "close cleanly")
	closeProvider(t, provider)

	log := string(mustReadFile(t, logPath))
	if !strings.Contains(log, "eof") {
		t.Fatalf("runner did not observe stdin EOF:\n%s", log)
	}
	if strings.Contains(log, "unexpected message") {
		t.Fatalf("provider sent an unsupported shutdown message:\n%s", log)
	}
}

func TestRunnerProviderFallsBackOnlyWhenPrimaryCannotStart(t *testing.T) {
	runnerPath := writeFakeRunner(t)
	cfg := runnerTestConfig(t, runnerPath)
	primary := cfg.Models["fast"]
	primary.Backend.Command = filepath.Join(t.TempDir(), "missing-runner")
	cfg.Models["primary"] = primary
	provider := NewRunnerProvider(cfg, nil)
	defer closeProvider(t, provider)
	target := &protocol.ModelTarget{Group: "writing", Profile: "draft"}
	events, err := provider.Generate(context.Background(), providers.GenerateRequest{
		ID: "fallback-1", Model: "primary", FallbackModels: []string{"fast"}, Target: target,
		Input: protocol.Input{Kind: "prompt", Prompt: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var started protocol.Event
	for event := range events {
		if event.Type == "started" {
			started = event
		}
	}
	if started.Model != "fast" || !started.Fallback || started.FallbackFrom != "primary" || started.Target == nil || *started.Target != *target {
		t.Fatalf("started = %#v", started)
	}
}

func TestRunnerProviderCooldownReloadsResidentModel(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	t.Setenv("YLLMD_FAKE_RUNNER_LOG", logPath)
	runnerPath := writeFakeRunner(t)
	cfg := runnerTestConfig(t, runnerPath)
	cfg.ModelLifecycle.IdleCooldown = 25 * time.Millisecond
	cfg.Models["deep"] = config.ModelConfig{
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

func TestRunnerProviderReusesModelForDifferentGenerationSettings(t *testing.T) {
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
	if count := strings.Count(log, "start "); count != 1 {
		t.Fatalf("start count = %d, log:\n%s", count, log)
	}
	if !strings.Contains(log, "max_tokens=64") {
		t.Fatalf("runner did not receive requested max tokens:\n%s", log)
	}
}

func TestRunnerProviderPassesAllGenerationSettings(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	t.Setenv("YLLMD_FAKE_RUNNER_LOG", logPath)
	runnerPath := writeFakeRunner(t)
	provider := NewRunnerProvider(runnerTestConfig(t, runnerPath), nil)
	defer closeProvider(t, provider)

	temperature, topP, minP := 0.2, 0.8, 0.1
	presence, repeat := -0.25, 1.15
	maxTokens, topK := 77, 12
	seed := uint64(99)
	drainGenerateWithSettings(t, provider, "req-settings", "settings", protocol.GenerationSettings{
		Temperature: &temperature, TopP: &topP, MaxTokens: &maxTokens,
		TopK: &topK, MinP: &minP, PresencePenalty: &presence,
		RepeatPenalty: &repeat, Seed: &seed, Stop: []string{"one", "two"},
	})

	log := string(mustReadFile(t, logPath))
	for _, want := range []string{"max_tokens=77", "temperature=0.2", "top_p=0.8", "top_k=12", "min_p=0.1", "presence_penalty=-0.25", "repeat_penalty=1.15", "seed=99", "stops=one|two"} {
		if !strings.Contains(log, want) {
			t.Fatalf("runner log missing %q:\n%s", want, log)
		}
	}
}

func TestRunnerProviderUsesFormattedTokenizationForCatalogTemplate(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	t.Setenv("YLLMD_FAKE_RUNNER_LOG", logPath)
	runnerPath := writeFakeRunner(t)
	cfg := runnerTestConfig(t, runnerPath)
	model := cfg.Models["fast"]
	model.CatalogID = "qwen25-coder-1.5b-instruct"
	cfg.Models["fast"] = model
	provider := NewRunnerProvider(cfg, nil)
	defer closeProvider(t, provider)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, err := provider.Generate(ctx, providers.GenerateRequest{
		ID: "req-chat", Model: "fast", Input: protocol.Input{Kind: "messages", Messages: []protocol.Message{{Role: "user", Content: "hello"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	log := string(mustReadFile(t, logPath))
	if !strings.Contains(log, "generate mode=1") {
		t.Fatalf("formatted tokenization mode not used:\n%s", log)
	}
}

func TestRunnerProviderCancellationKeepsSessionReusable(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	t.Setenv("YLLMD_FAKE_RUNNER_LOG", logPath)
	runnerPath := writeFakeRunner(t)
	provider := NewRunnerProvider(runnerTestConfig(t, runnerPath), nil)
	defer closeProvider(t, provider)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := provider.Generate(ctx, providers.GenerateRequest{
		ID: "req-cancel", Model: "fast", Stream: true,
		Input: protocol.Input{Kind: "prompt", Prompt: "cancel test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()
	waitForLog(t, logPath, "prompt cancel test")
	cancel()
	<-done
	waitForLog(t, logPath, "cancel")

	drainGenerate(t, provider, "req-after-cancel", "still resident")
	log := string(mustReadFile(t, logPath))
	if count := strings.Count(log, "start "); count != 1 {
		t.Fatalf("start count = %d, log:\n%s", count, log)
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
	if completed.FinishReason != "stop" {
		t.Fatalf("finish reason = %q", completed.FinishReason)
	}
}

func writeFakeRunner(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "fake_runner.go")
	binary := filepath.Join(dir, "fake-runner")
	program := `package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
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
	logEvent("start %s --model %s --ctx=%s --threads=%s --gpu-layers=%s",
		filepath.Base(flagValue(args, "--model")),
		flagValue(args, "--model"),
		flagValue(args, "--ctx"),
		flagValue(args, "--threads"),
		flagValue(args, "--gpu-layers"))
	writeReady()

	for {
		kind, payload, err := readFrame()
		if err != nil {
			if err == io.EOF {
				logEvent("eof")
			}
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				logEvent("read_error %v", err)
			}
			return
		}
		switch kind {
		case 1:
			request, err := decodeGenerate(payload)
			if err != nil {
				writeError("malformed_generate", err.Error())
				continue
			}
			logEvent("generate mode=%d max_tokens=%d temperature=%g top_p=%g top_k=%d min_p=%g presence_penalty=%g repeat_penalty=%g seed=%d stops=%s prompt=%s",
				request.mode, request.maxTokens, request.temperature, request.topP,
				request.topK, request.minP, request.presence, request.repeat,
				request.seed, strings.Join(request.stops, "|"), strings.TrimSpace(request.prompt))
			logEvent("prompt %s", request.prompt)
			if strings.Contains(request.prompt, "cancel test") {
				writeChunk("partial")
				cancelKind, _, err := readFrame()
				if err != nil || cancelKind != 2 {
					writeError("cancel_failed", "expected Cancel")
					return
				}
				logEvent("cancel")
				writeCompleted(3, 3, 1)
				continue
			}
			output := response
			reason := byte(0)
			for _, stop := range request.stops {
				if index := strings.Index(output, stop); index >= 0 {
					output = output[:index]
					reason = 2
					break
				}
			}
			if output != "" {
				writeChunk(output)
			}
			writeCompleted(reason, 3, 2)
		case 2:
			continue
		default:
			logEvent("unexpected message type=%d", kind)
		}
	}
}

type generateRequest struct {
	mode byte
	maxTokens uint32
	temperature, topP, minP, presence, repeat float64
	topK int32
	seed uint64
	stops []string
	prompt string
}

func decodeGenerate(payload []byte) (generateRequest, error) {
	var request generateRequest
	if len(payload) < 58 {
		return request, fmt.Errorf("short payload")
	}
	request.mode = payload[0]
	stopCount := int(payload[1])
	request.maxTokens = binary.LittleEndian.Uint32(payload[2:6])
	request.temperature = math.Float64frombits(binary.LittleEndian.Uint64(payload[6:14]))
	request.topP = math.Float64frombits(binary.LittleEndian.Uint64(payload[14:22]))
	request.topK = int32(binary.LittleEndian.Uint32(payload[22:26]))
	request.minP = math.Float64frombits(binary.LittleEndian.Uint64(payload[26:34]))
	request.presence = math.Float64frombits(binary.LittleEndian.Uint64(payload[34:42]))
	request.repeat = math.Float64frombits(binary.LittleEndian.Uint64(payload[42:50]))
	request.seed = binary.LittleEndian.Uint64(payload[50:58])
	offset := 58
	for i := 0; i < stopCount; i++ {
		if offset+4 > len(payload) { return request, fmt.Errorf("short stop length") }
		length := int(binary.LittleEndian.Uint32(payload[offset:offset+4])); offset += 4
		if offset+length > len(payload) { return request, fmt.Errorf("short stop") }
		request.stops = append(request.stops, string(payload[offset:offset+length])); offset += length
	}
	request.prompt = string(payload[offset:])
	return request, nil
}

func readFrame() (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(os.Stdin, header[:]); err != nil { return 0, nil, err }
	length := binary.LittleEndian.Uint32(header[1:])
	payload := make([]byte, length)
	_, err := io.ReadFull(os.Stdin, payload)
	return header[0], payload, err
}

func writeFrame(kind byte, payload []byte) {
	var header [5]byte
	header[0] = kind
	binary.LittleEndian.PutUint32(header[1:], uint32(len(payload)))
	os.Stdout.Write(header[:]); os.Stdout.Write(payload)
}

func writeReady() {
	writeFrame(0x10, nil)
}

func writeChunk(text string) {
	writeFrame(1, []byte(text))
}

func writeCompleted(reason byte, inputTokens, outputTokens uint32) {
	var payload bytes.Buffer
	payload.WriteByte(reason)
	binary.Write(&payload, binary.LittleEndian, inputTokens)
	binary.Write(&payload, binary.LittleEndian, outputTokens)
	writeFrame(4, payload.Bytes())
}

func writeError(code, message string) {
	var payload bytes.Buffer
	binary.Write(&payload, binary.LittleEndian, uint16(len(code))); payload.WriteString(code)
	payload.WriteString(message)
	writeFrame(3, payload.Bytes())
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
		Models: map[string]config.ModelConfig{
			"fast": {
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

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
