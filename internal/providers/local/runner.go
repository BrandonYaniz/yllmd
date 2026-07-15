package local

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	runnerFrameChunk byte = 0x01
	runnerFrameDone  byte = 0x02
	runnerFrameError byte = 0x03

	defaultRunnerMaxTokens   = 128
	defaultRunnerTemperature = 0.8
	defaultRunnerTopP        = 0.95
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
	options   runnerOptions
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	frames    <-chan frameResult
	done      <-chan error
	stdinMu   sync.Mutex
	closeMu   sync.Mutex
	closed    bool
}

type runnerOptions struct {
	temperature float64
	topP        float64
	maxTokens   int
}

type runnerFrame struct {
	tag     byte
	payload []byte
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
	model, err := p.registry.ResolveRequest(request.Model, request.ModelType, request.Level)
	if err != nil {
		return nil, err
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
	prompt, err := runnerPrompt(model, request.Input)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.epoch++
	epoch := p.epoch
	p.stopCooldownLocked()

	options := effectiveRunnerOptions(request.Settings)
	session, err := p.sessionFor(ctx, model, options)
	if err != nil {
		return err
	}
	defer p.scheduleCooldownLocked(model.Name, epoch)

	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.close(context.Background(), "cancel")
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	if !sendRunnerEvent(ctx, events, protocol.Event{Type: "started", ID: request.ID, Provider: "local", Model: model.Name}) {
		return nil
	}

	if err := session.writePrompt(prompt); err != nil {
		p.discardSession(ctx)
		return fmt.Errorf("send runner prompt: %w", err)
	}

	stopFilter := newRunnerStopFilter(request.Settings.Stop)
	var completed strings.Builder
	for {
		frame, err := session.readFrame(ctx)
		if err != nil {
			p.discardSession(ctx)
			return err
		}
		switch frame.tag {
		case runnerFrameChunk:
			text := string(frame.payload)
			emit, stopped := stopFilter.push(text)
			if emit != "" {
				completed.WriteString(emit)
				if request.Stream && !sendRunnerEvent(ctx, events, protocol.Event{Type: "delta", ID: request.ID, Text: emit}) {
					return nil
				}
			}
			if stopped {
				p.discardSession(context.Background())
				_ = sendRunnerEvent(ctx, events, protocol.Event{Type: "completed", ID: request.ID, FinishReason: "stop", Text: completed.String()})
				return nil
			}
		case runnerFrameDone:
			if emit := stopFilter.flush(); emit != "" {
				completed.WriteString(emit)
				if request.Stream && !sendRunnerEvent(ctx, events, protocol.Event{Type: "delta", ID: request.ID, Text: emit}) {
					return nil
				}
			}
			_ = sendRunnerEvent(ctx, events, protocol.Event{Type: "completed", ID: request.ID, FinishReason: "stop", Text: completed.String()})
			return nil
		case runnerFrameError:
			_ = sendRunnerEvent(ctx, events, protocol.Event{Type: "error", ID: request.ID, Code: "runner_error", Message: string(frame.payload)})
			return nil
		default:
			p.discardSession(ctx)
			return fmt.Errorf("unknown runner frame tag 0x%02x", frame.tag)
		}
	}
}

func effectiveRunnerOptions(settings protocol.GenerationSettings) runnerOptions {
	options := runnerOptions{
		temperature: defaultRunnerTemperature,
		topP:        defaultRunnerTopP,
		maxTokens:   defaultRunnerMaxTokens,
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
	return options
}

func runnerPrompt(model models.LocalModel, input protocol.Input) (string, error) {
	if input.Kind == "prompt" {
		return input.Prompt, nil
	}
	template, err := catalogPromptTemplate(model.Config.CatalogID)
	if err != nil {
		return "", err
	}
	switch template {
	case "":
		return plainMessagesPrompt(input.Messages), nil
	case "qwen2.5-chatml":
		return qwenChatMLPrompt(input.Messages), nil
	case "phi4-chat":
		return phi4ChatPrompt(input.Messages), nil
	case "gemma3-chat":
		return gemma3ChatPrompt(input.Messages)
	case "llama3-instruct":
		return llama3InstructPrompt(input.Messages), nil
	default:
		return "", fmt.Errorf("catalog variant %q requires unsupported prompt template %q", model.Config.CatalogID, template)
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

type runnerStopFilter struct {
	stops      []string
	maxKeep    int
	pending    string
	terminated bool
}

func newRunnerStopFilter(stops []string) *runnerStopFilter {
	filter := &runnerStopFilter{stops: stops}
	for _, stop := range stops {
		if len(stop) > filter.maxKeep {
			filter.maxKeep = len(stop) - 1
		}
	}
	return filter
}

func (f *runnerStopFilter) push(text string) (string, bool) {
	if len(f.stops) == 0 || f.terminated {
		return text, f.terminated
	}
	f.pending += text
	if index := f.firstStopIndex(); index >= 0 {
		f.terminated = true
		emit := f.pending[:index]
		f.pending = ""
		return emit, true
	}
	if len(f.pending) <= f.maxKeep {
		return "", false
	}
	emitLen := len(f.pending) - f.maxKeep
	emit := f.pending[:emitLen]
	f.pending = f.pending[emitLen:]
	return emit, false
}

func (f *runnerStopFilter) flush() string {
	if len(f.stops) == 0 || f.terminated {
		return ""
	}
	emit := f.pending
	f.pending = ""
	return emit
}

func (f *runnerStopFilter) firstStopIndex() int {
	first := -1
	for _, stop := range f.stops {
		index := strings.Index(f.pending, stop)
		if index >= 0 && (first == -1 || index < first) {
			first = index
		}
	}
	return first
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
		session, err := p.startSession(ctx, resident, runnerOptions{
			temperature: defaultRunnerTemperature,
			topP:        defaultRunnerTopP,
			maxTokens:   defaultRunnerMaxTokens,
		})
		if err != nil {
			p.logger.Debug("resident model reload after cooldown failed", "model", resident.Name, "error", err)
			return
		}
		p.session = session
	})
}

func (p *RunnerProvider) sessionFor(ctx context.Context, model models.LocalModel, options runnerOptions) (*runnerSession, error) {
	if p.session != nil && p.session.modelName == model.Name && p.session.options == options {
		return p.session, nil
	}
	if p.session != nil {
		if err := p.session.close(ctx, "shutdown-switch-model"); err != nil {
			p.logger.Debug("runner shutdown during model switch failed", "model", p.session.modelName, "error", err)
		}
		p.session = nil
	}
	session, err := p.startSession(ctx, model, options)
	if err != nil {
		return nil, err
	}
	p.session = session
	return session, nil
}

func (p *RunnerProvider) startSession(ctx context.Context, model models.LocalModel, options runnerOptions) (*runnerSession, error) {
	cmd := exec.Command(model.Config.Backend.Command,
		"--model", model.ModelPath,
		"--ctx", strconv.Itoa(model.Config.Runtime.ContextTokens),
		"--threads", strconv.Itoa(model.Config.Runtime.Threads),
		"--max-tokens", strconv.Itoa(options.maxTokens),
		"--temperature", strconv.FormatFloat(options.temperature, 'f', -1, 64),
		"--top-p", strconv.FormatFloat(options.topP, 'f', -1, 64),
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
		options:   options,
		cmd:       cmd,
		stdin:     stdin,
		frames:    frames,
		done:      done,
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

func (s *runnerSession) writePrompt(prompt string) error {
	return writeRunnerPrompt(&s.stdinMu, s.stdin, prompt)
}

func (s *runnerSession) readFrame(ctx context.Context) (runnerFrame, error) {
	return readRunnerFrame(ctx, s.frames)
}

func (s *runnerSession) close(ctx context.Context, _ string) error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	_ = s.stdin.Close()
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
	frames := make(chan frameResult, 1)
	go func() {
		defer close(frames)
		for {
			frame, err := readBinaryRunnerFrame(stdout)
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

func readBinaryRunnerFrame(stdout io.Reader) (runnerFrame, error) {
	var tag [1]byte
	if _, err := io.ReadFull(stdout, tag[:]); err != nil {
		return runnerFrame{}, err
	}
	switch tag[0] {
	case runnerFrameChunk:
		payload, err := readSizedPayload(stdout, 4)
		if err != nil {
			return runnerFrame{}, err
		}
		return runnerFrame{tag: tag[0], payload: payload}, nil
	case runnerFrameDone:
		return runnerFrame{tag: tag[0]}, nil
	case runnerFrameError:
		payload, err := readSizedPayload(stdout, 2)
		if err != nil {
			return runnerFrame{}, err
		}
		return runnerFrame{tag: tag[0], payload: payload}, nil
	default:
		return runnerFrame{}, fmt.Errorf("unknown runner frame tag 0x%02x", tag[0])
	}
}

func readSizedPayload(stdout io.Reader, lengthBytes int) ([]byte, error) {
	header := make([]byte, lengthBytes)
	if _, err := io.ReadFull(stdout, header); err != nil {
		return nil, err
	}
	var length uint32
	switch lengthBytes {
	case 2:
		length = uint32(binary.LittleEndian.Uint16(header))
	case 4:
		length = binary.LittleEndian.Uint32(header)
	default:
		return nil, fmt.Errorf("unsupported runner payload length size %d", lengthBytes)
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(stdout, payload); err != nil {
			return nil, err
		}
	}
	return payload, nil
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

func writeRunnerPrompt(mu *sync.Mutex, stdin io.Writer, prompt string) error {
	if uint64(len(prompt)) > uint64(^uint32(0)) {
		return fmt.Errorf("runner prompt too large")
	}
	var frame bytes.Buffer
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(prompt)))
	frame.Write(length[:])
	frame.WriteString(prompt)
	mu.Lock()
	defer mu.Unlock()
	_, err := stdin.Write(frame.Bytes())
	return err
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
