package local

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/catalog"
	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/models"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
	"github.com/BrandonYaniz/yllmd/internal/providers"
)

const (
	defaultRunnerMaxTokens   = 128
	defaultRunnerTemperature = 0.8
	defaultRunnerTopP        = 0.95
	defaultRunnerTopK        = 40
	defaultRunnerMinP        = 0.05
	defaultRunnerPresence    = 0.0
	defaultRunnerRepeat      = 1.0
	runnerStartupTimeout     = 120 * time.Second
	runnerCancelTimeout      = 5 * time.Second
)

type RunnerProvider struct {
	cfg      config.Config
	registry models.Registry
	logger   *slog.Logger

	mu      sync.Mutex
	session *runnerSession
	timer   *time.Timer
	epoch   uint64
}

type runnerSession struct {
	modelName string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	frames    <-chan frameResult
	done      <-chan error
	stdinMu   sync.Mutex
	closeMu   sync.Mutex
	closed    bool
}

type runnerOptions struct {
	temperature     float64
	topP            float64
	maxTokens       int
	topK            int
	minP            float64
	presencePenalty float64
	repeatPenalty   float64
	seed            uint64
}

func NewRunnerProvider(cfg config.Config, logger *slog.Logger) *RunnerProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &RunnerProvider{cfg: cfg, registry: models.NewRegistry(cfg), logger: logger}
}

func (p *RunnerProvider) ID() string {
	return "local"
}

func (p *RunnerProvider) Generate(ctx context.Context, request providers.GenerateRequest) (<-chan protocol.Event, error) {
	modelNames := append([]string{request.Model}, request.FallbackModels...)
	var model models.LocalModel
	var startupErr error
	for index, name := range modelNames {
		candidate, err := p.registry.Resolve(name)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		p.stopCooldownLocked()
		_, err = p.sessionFor(ctx, candidate)
		p.mu.Unlock()
		if err != nil {
			startupErr = err
			continue
		}
		model = candidate
		if index > 0 {
			request.Fallback = true
			if request.FallbackFrom == "" {
				request.FallbackFrom = request.Model
			}
			request.Model = candidate.Name
		}
		startupErr = nil
		break
	}
	if startupErr != nil {
		return nil, startupErr
	}
	events := make(chan protocol.Event)
	go func() {
		defer close(events)
		if err := p.run(ctx, model, request, events); err != nil {
			sendProviderError(ctx, events, request.ID, err)
		}
	}()
	return events, nil
}

func (p *RunnerProvider) run(ctx context.Context, model models.LocalModel, request providers.GenerateRequest, events chan<- protocol.Event) error {
	prepared, err := prepareRunnerInput(model, request.Input)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.epoch++
	epoch := p.epoch
	p.stopCooldownLocked()

	options := effectiveRunnerOptions(request.Settings)
	session, err := p.sessionFor(ctx, model)
	if err != nil {
		return err
	}
	defer p.scheduleCooldownLocked(model.Name, epoch)

	if !sendRunnerEvent(ctx, events, protocol.Event{Type: "started", ID: request.ID, Provider: "local", Target: request.Target, Model: model.Name, Fallback: request.Fallback, FallbackFrom: request.FallbackFrom}) {
		return nil
	}

	generate := runnerGenerate{
		prompt:           prepared.prompt,
		tokenizationMode: prepared.tokenizationMode,
		maxTokens:        uint32(options.maxTokens),
		temperature:      options.temperature,
		topP:             options.topP,
		topK:             int32(options.topK),
		minP:             options.minP,
		presencePenalty:  options.presencePenalty,
		repeatPenalty:    options.repeatPenalty,
		seed:             options.seed,
		stops:            request.Settings.Stop,
	}
	if err := session.writeGenerate(generate); err != nil {
		p.discardSession(context.Background())
		return fmt.Errorf("send runner Generate: %w", err)
	}

	var completed strings.Builder
	for {
		frame, err := session.readFrame(ctx)
		if err != nil {
			if ctx.Err() != nil {
				if drainErr := session.cancelAndDrain(); drainErr == nil {
					return nil
				} else {
					p.logger.Debug("runner cancellation failed", "model", model.Name, "error", drainErr)
				}
			}
			p.discardSession(context.Background())
			return err
		}
		switch frame.tag {
		case runnerFrameChunk:
			completed.WriteString(frame.chunk)
			if request.Stream && !sendRunnerEvent(ctx, events, protocol.Event{Type: "delta", ID: request.ID, Text: frame.chunk}) {
				if drainErr := session.cancelAndDrain(); drainErr != nil {
					p.discardSession(context.Background())
				}
				return nil
			}
		case runnerFrameCompleted:
			usage := &protocol.Usage{
				InputTokens:  int(frame.completed.inputTokens),
				OutputTokens: int(frame.completed.outputTokens),
			}
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
			finishReason := runnerFinishReason(frame.completed.finishReason)
			if finishReason == "cancelled" {
				_ = sendRunnerEvent(ctx, events, protocol.Event{Type: "cancelled", ID: request.ID})
				return nil
			}
			_ = sendRunnerEvent(ctx, events, protocol.Event{
				Type:         "completed",
				ID:           request.ID,
				FinishReason: finishReason,
				Text:         completed.String(),
				Usage:        usage,
			})
			return nil
		case runnerFrameError:
			if frame.runnerError == nil {
				p.discardSession(context.Background())
				return errors.New("runner returned an empty Error frame")
			}
			if runnerErrorIsFatal(frame.runnerError.code) {
				p.discardSession(context.Background())
			}
			_ = sendRunnerEvent(ctx, events, protocol.Event{Type: "error", ID: request.ID, Code: frame.runnerError.code, Message: frame.runnerError.message})
			return nil
		default:
			p.discardSession(context.Background())
			return fmt.Errorf("unknown runner frame tag 0x%02x", frame.tag)
		}
	}
}

func effectiveRunnerOptions(settings protocol.GenerationSettings) runnerOptions {
	options := runnerOptions{
		temperature:     defaultRunnerTemperature,
		topP:            defaultRunnerTopP,
		maxTokens:       defaultRunnerMaxTokens,
		topK:            defaultRunnerTopK,
		minP:            defaultRunnerMinP,
		presencePenalty: defaultRunnerPresence,
		repeatPenalty:   defaultRunnerRepeat,
		seed:            math.MaxUint64,
	}
	if settings.Temperature != nil {
		options.temperature = *settings.Temperature
	}
	if settings.TopP != nil {
		options.topP = *settings.TopP
	}
	if settings.MaxTokens != nil {
		options.maxTokens = *settings.MaxTokens
	}
	if settings.TopK != nil {
		options.topK = *settings.TopK
	}
	if settings.MinP != nil {
		options.minP = *settings.MinP
	}
	if settings.PresencePenalty != nil {
		options.presencePenalty = *settings.PresencePenalty
	}
	if settings.RepeatPenalty != nil {
		options.repeatPenalty = *settings.RepeatPenalty
	}
	if settings.Seed != nil {
		options.seed = *settings.Seed
	}
	return options
}

type preparedRunnerInput struct {
	prompt           string
	tokenizationMode byte
}

func prepareRunnerInput(model models.LocalModel, input protocol.Input) (preparedRunnerInput, error) {
	if input.Kind == "prompt" {
		return preparedRunnerInput{prompt: input.Prompt, tokenizationMode: runnerTokenizationRaw}, nil
	}
	template, err := catalogPromptTemplate(model.Config.CatalogID)
	if err != nil {
		return preparedRunnerInput{}, err
	}
	prompt, err := renderRunnerMessages(template, model.Config.CatalogID, input.Messages)
	if err != nil {
		return preparedRunnerInput{}, err
	}
	mode := runnerTokenizationRaw
	if template != "" {
		mode = runnerTokenizationFormatted
	}
	return preparedRunnerInput{prompt: prompt, tokenizationMode: mode}, nil
}

func runnerPrompt(model models.LocalModel, input protocol.Input) (string, error) {
	prepared, err := prepareRunnerInput(model, input)
	return prepared.prompt, err
}

func renderRunnerMessages(template, catalogID string, messages []protocol.Message) (string, error) {
	switch template {
	case "":
		return plainMessagesPrompt(messages), nil
	case "qwen2.5-chatml":
		return qwenChatMLPrompt(messages), nil
	case "phi4-chat":
		return phi4ChatPrompt(messages), nil
	case "gemma3-chat":
		return gemma3ChatPrompt(messages)
	case "llama3-instruct":
		return llama3InstructPrompt(messages), nil
	case "granite3-chat":
		return granite3ChatPrompt(messages, time.Now())
	case "mistral-nemo-instruct":
		return mistralNemoInstructPrompt(messages)
	case "qwen3-nonthinking-chatml":
		return qwen3NonThinkingPrompt(messages)
	default:
		return "", fmt.Errorf("catalog variant %q requires unsupported prompt template %q", catalogID, template)
	}
}

func catalogPromptTemplate(variantID string) (string, error) {
	if variantID == "" {
		return "", nil
	}
	modelCatalog, err := catalog.Load()
	if err != nil {
		return "", fmt.Errorf("load model catalog: %w", err)
	}
	_, variant, ok := modelCatalog.Variant(variantID)
	if !ok || variant.Artifact == nil {
		return "", nil
	}
	return variant.Artifact.PromptTemplate, nil
}

func plainMessagesPrompt(messages []protocol.Message) string {
	var prompt strings.Builder
	for _, message := range messages {
		if prompt.Len() > 0 {
			prompt.WriteByte('\n')
		}
		prompt.WriteString(message.Role)
		prompt.WriteString(": ")
		prompt.WriteString(message.Content)
	}
	return prompt.String()
}

func qwenChatMLPrompt(messages []protocol.Message) string {
	var prompt strings.Builder
	for _, message := range messages {
		prompt.WriteString("<|im_start|>")
		prompt.WriteString(message.Role)
		prompt.WriteByte('\n')
		prompt.WriteString(message.Content)
		prompt.WriteString("<|im_end|>\n")
	}
	prompt.WriteString("<|im_start|>assistant\n")
	return prompt.String()
}

func phi4ChatPrompt(messages []protocol.Message) string {
	var prompt strings.Builder
	for _, message := range messages {
		prompt.WriteString("<|")
		prompt.WriteString(message.Role)
		prompt.WriteString("|>")
		prompt.WriteString(message.Content)
		prompt.WriteString("<|end|>")
	}
	prompt.WriteString("<|assistant|>")
	return prompt.String()
}

func gemma3ChatPrompt(messages []protocol.Message) (string, error) {
	if len(messages) == 0 {
		return "", errors.New("Gemma 3 requires at least one message")
	}
	firstUserPrefix := ""
	if messages[0].Role == "system" {
		firstUserPrefix = strings.TrimSpace(messages[0].Content) + "\n\n"
		messages = messages[1:]
	}
	if len(messages) == 0 {
		return "", errors.New("Gemma 3 requires a user message after the system message")
	}
	var prompt strings.Builder
	prompt.WriteString("<bos>")
	for i, message := range messages {
		expectedRole := "user"
		outputRole := message.Role
		if i%2 == 1 {
			expectedRole = "assistant"
		}
		if message.Role != expectedRole {
			return "", fmt.Errorf("Gemma 3 messages must alternate user and assistant; message %d has role %q", i, message.Role)
		}
		if outputRole == "assistant" {
			outputRole = "model"
		}
		prompt.WriteString("<start_of_turn>")
		prompt.WriteString(outputRole)
		prompt.WriteByte('\n')
		if i == 0 {
			prompt.WriteString(firstUserPrefix)
		}
		prompt.WriteString(strings.TrimSpace(message.Content))
		prompt.WriteString("<end_of_turn>\n")
	}
	prompt.WriteString("<start_of_turn>model\n")
	return prompt.String(), nil
}

func llama3InstructPrompt(messages []protocol.Message) string {
	var prompt strings.Builder
	prompt.WriteString("<|begin_of_text|>")
	for _, message := range messages {
		prompt.WriteString("<|start_header_id|>")
		prompt.WriteString(message.Role)
		prompt.WriteString("<|end_header_id|>\n\n")
		prompt.WriteString(strings.TrimSpace(message.Content))
		prompt.WriteString("<|eot_id|>")
	}
	prompt.WriteString("<|start_header_id|>assistant<|end_header_id|>\n\n")
	return prompt.String()
}

func granite3ChatPrompt(messages []protocol.Message, now time.Time) (string, error) {
	if len(messages) == 0 {
		return "", errors.New("Granite 3 requires at least one message")
	}
	systemMessage := ""
	loopMessages := messages
	if messages[0].Role == "system" {
		systemMessage = messages[0].Content
		loopMessages = messages[1:]
	} else {
		systemMessage = " Knowledge Cutoff Date: April 2024.\n Today's Date: " +
			now.Format("January 02, 2006") +
			". You are Granite, developed by IBM. You are a helpful AI assistant."
	}

	var prompt strings.Builder
	prompt.WriteString("<|start_of_role|>system<|end_of_role|>")
	prompt.WriteString(systemMessage)
	prompt.WriteString("<|end_of_text|>\n")
	for _, message := range loopMessages {
		prompt.WriteString("<|start_of_role|>")
		prompt.WriteString(message.Role)
		prompt.WriteString("<|end_of_role|>")
		prompt.WriteString(message.Content)
		prompt.WriteString("<|end_of_text|>\n")
	}
	prompt.WriteString("<|start_of_role|>assistant<|end_of_role|>")
	return prompt.String(), nil
}

func mistralNemoInstructPrompt(messages []protocol.Message) (string, error) {
	if len(messages) == 0 {
		return "", errors.New("Mistral Nemo requires at least one message")
	}
	systemMessage := ""
	if messages[0].Role == "system" {
		systemMessage = messages[0].Content
		messages = messages[1:]
	}
	if len(messages) == 0 {
		return "", errors.New("Mistral Nemo requires a user message after the system message")
	}

	var prompt strings.Builder
	prompt.WriteString("<s>")
	for i, message := range messages {
		expectedRole := "user"
		if i%2 == 1 {
			expectedRole = "assistant"
		}
		if message.Role != expectedRole {
			return "", fmt.Errorf("Mistral Nemo messages must alternate user and assistant; message %d has role %q", i, message.Role)
		}
		if message.Role == "user" {
			prompt.WriteString("[INST]")
			if systemMessage != "" && i == len(messages)-1 {
				prompt.WriteString(systemMessage)
				prompt.WriteString("\n\n")
			}
			prompt.WriteString(message.Content)
			prompt.WriteString("[/INST]")
		} else {
			prompt.WriteString(message.Content)
			prompt.WriteString("</s>")
		}
	}
	return prompt.String(), nil
}

func qwen3NonThinkingPrompt(messages []protocol.Message) (string, error) {
	if len(messages) == 0 {
		return "", errors.New("Qwen 3 requires at least one message")
	}

	var prompt strings.Builder
	start := 0
	if messages[0].Role == "system" {
		prompt.WriteString("<|im_start|>system\n")
		prompt.WriteString(messages[0].Content)
		prompt.WriteString("<|im_end|>\n")
		start = 1
	}
	if start == len(messages) {
		return "", errors.New("Qwen 3 requires a user message after the system message")
	}
	for i := start; i < len(messages); i++ {
		message := messages[i]
		expectedRole := "user"
		if (i-start)%2 == 1 {
			expectedRole = "assistant"
		}
		if message.Role != expectedRole {
			return "", fmt.Errorf("Qwen 3 messages must alternate user and assistant; message %d has role %q", i-start, message.Role)
		}
		prompt.WriteString("<|im_start|>")
		prompt.WriteString(message.Role)
		prompt.WriteByte('\n')
		prompt.WriteString(message.Content)
		prompt.WriteString("<|im_end|>\n")
	}
	prompt.WriteString("<|im_start|>assistant\n<think>\n\n</think>\n\n")
	return prompt.String(), nil
}

func (p *RunnerProvider) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopCooldownLocked()
	if p.session == nil {
		return nil
	}
	err := p.session.close(ctx, "shutdown-provider")
	p.session = nil
	return err
}

func (p *RunnerProvider) stopCooldownLocked() {
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
}

func (p *RunnerProvider) scheduleCooldownLocked(modelName string, epoch uint64) {
	if p.cfg.ModelLifecycle.IdleCooldown <= 0 || modelName == p.cfg.ModelLifecycle.ResidentModel {
		return
	}
	p.stopCooldownLocked()
	cooldown := p.cfg.ModelLifecycle.IdleCooldown
	p.timer = time.AfterFunc(cooldown, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.epoch != epoch {
			return
		}
		if p.session == nil || p.session.modelName == p.cfg.ModelLifecycle.ResidentModel {
			return
		}
		resident, err := p.registry.Resident()
		if err != nil {
			p.logger.Debug("resident model unavailable during cooldown", "error", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.session.close(ctx, "shutdown-idle-cooldown"); err != nil {
			p.logger.Debug("runner shutdown during idle cooldown failed", "model", p.session.modelName, "error", err)
		}
		p.session = nil
		session, err := p.startSession(ctx, resident)
		if err != nil {
			p.logger.Debug("resident model reload after cooldown failed", "model", resident.Name, "error", err)
			return
		}
		p.session = session
	})
}

func (p *RunnerProvider) sessionFor(ctx context.Context, model models.LocalModel) (*runnerSession, error) {
	if p.session != nil && p.session.modelName == model.Name {
		return p.session, nil
	}
	if p.session != nil {
		if err := p.session.close(ctx, "shutdown-switch-model"); err != nil {
			p.logger.Debug("runner shutdown during model switch failed", "model", p.session.modelName, "error", err)
		}
		p.session = nil
	}
	session, err := p.startSession(ctx, model)
	if err != nil {
		return nil, err
	}
	p.session = session
	return session, nil
}

func (p *RunnerProvider) startSession(ctx context.Context, model models.LocalModel) (*runnerSession, error) {
	cmd := exec.Command(model.Config.Backend.Command,
		"--model", model.ModelPath,
		"--ctx", strconv.Itoa(model.Config.Runtime.ContextTokens),
		"--threads", strconv.Itoa(model.Config.Runtime.Threads),
		"--gpu-layers", strconv.Itoa(model.Config.Runtime.GPULayers),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open runner stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open runner stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("open runner stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start runner: %w", err)
	}

	frames := readRunnerFrames(stdout)
	go p.logRunnerStderr(stderr, model.Name)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	session := &runnerSession{
		modelName: model.Name,
		cmd:       cmd,
		stdin:     stdin,
		frames:    frames,
		done:      done,
	}
	startupCtx, startupCancel := context.WithTimeout(ctx, runnerStartupTimeout)
	defer startupCancel()
	frame, err := session.readFrame(startupCtx)
	if err != nil {
		_ = session.close(context.Background(), "startup-failed")
		return nil, fmt.Errorf("wait for runner Ready: %w", err)
	}
	if frame.tag == runnerFrameError && frame.runnerError != nil {
		_ = session.close(context.Background(), "startup-error")
		return nil, fmt.Errorf("runner startup: %w", *frame.runnerError)
	}
	if frame.tag != runnerFrameReady {
		_ = session.close(context.Background(), "startup-invalid")
		return nil, fmt.Errorf("runner first frame 0x%02x is not Ready", frame.tag)
	}
	return session, nil
}

func (p *RunnerProvider) discardSession(ctx context.Context) {
	if p.session == nil {
		return
	}
	_ = p.session.close(ctx, "shutdown-discard")
	p.session = nil
}

func (s *runnerSession) writeGenerate(request runnerGenerate) error {
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	return writeRunnerGenerate(s.stdin, request)
}

func (s *runnerSession) readFrame(ctx context.Context) (runnerFrame, error) {
	return readRunnerFrame(ctx, s.frames)
}

func (s *runnerSession) cancelAndDrain() error {
	s.stdinMu.Lock()
	err := writeRunnerControl(s.stdin, runnerMessageCancel)
	s.stdinMu.Unlock()
	if err != nil {
		return fmt.Errorf("send runner Cancel: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerCancelTimeout)
	defer cancel()
	for {
		frame, err := s.readFrame(ctx)
		if err != nil {
			return fmt.Errorf("wait for runner cancellation: %w", err)
		}
		switch frame.tag {
		case runnerFrameChunk:
			continue
		case runnerFrameCompleted:
			// A late Cancel is silently ignored by the runner while idle. Since it
			// precedes any future Generate on stdin, the session remains reusable.
			return nil
		case runnerFrameError:
			if frame.runnerError != nil {
				return fmt.Errorf("runner cancellation: %w", *frame.runnerError)
			}
			return errors.New("runner cancellation returned empty Error")
		default:
			return fmt.Errorf("unexpected runner frame 0x%02x during cancellation", frame.tag)
		}
	}
}

func (s *runnerSession) close(ctx context.Context, _ string) error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	s.stdinMu.Lock()
	_ = s.stdin.Close()
	s.stdinMu.Unlock()
	select {
	case err := <-s.done:
		return err
	case <-ctx.Done():
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		return ctx.Err()
	case <-time.After(500 * time.Millisecond):
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		<-s.done
		return nil
	}
}

func readRunnerFrames(stdout io.Reader) <-chan frameResult {
	frames := make(chan frameResult, 4)
	go func() {
		defer close(frames)
		for {
			frame, err := readRunnerProtocolFrame(stdout)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					frames <- frameResult{err: err}
				}
				return
			}
			frames <- frameResult{frame: frame}
		}
	}()
	return frames
}

type frameResult struct {
	frame runnerFrame
	err   error
}

func readRunnerFrame(ctx context.Context, frames <-chan frameResult) (runnerFrame, error) {
	select {
	case <-ctx.Done():
		return runnerFrame{}, ctx.Err()
	case result, ok := <-frames:
		if !ok {
			return runnerFrame{}, errors.New("runner stdout closed")
		}
		if result.err != nil {
			return runnerFrame{}, result.err
		}
		return result.frame, nil
	}
}

func sendRunnerEvent(ctx context.Context, events chan<- protocol.Event, event protocol.Event) bool {
	select {
	case <-ctx.Done():
		return false
	case events <- event:
		return true
	}
}

func sendProviderError(ctx context.Context, events chan<- protocol.Event, id string, err error) {
	select {
	case <-ctx.Done():
	case events <- protocol.Event{Type: "error", ID: id, Code: "runner_failed", Message: err.Error()}:
	}
}

func (p *RunnerProvider) logRunnerStderr(stderr io.Reader, modelName string) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			p.logger.Debug("runner stderr", "model", modelName, "line", line)
		}
	}
}
