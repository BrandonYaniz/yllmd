package local

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/models"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
	"github.com/BrandonYaniz/yllmd/internal/providers"
)

const runnerProtocolVersion = 1

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
	lines     <-chan lineResult
	done      <-chan error
	stdinMu   sync.Mutex
}

type runnerEvent struct {
	Type            string          `json:"type"`
	ID              string          `json:"id"`
	ProtocolVersion int             `json:"protocol_version"`
	Runner          string          `json:"runner"`
	Capabilities    []string        `json:"capabilities"`
	ModelPath       string          `json:"model_path"`
	ContextTokens   int             `json:"context_tokens"`
	Text            string          `json:"text"`
	FinishReason    string          `json:"finish_reason"`
	Usage           *protocol.Usage `json:"usage"`
	Code            string          `json:"code"`
	Message         string          `json:"message"`
}

type runnerConfigureCommand struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	ModelPath     string `json:"model_path"`
	ContextTokens int    `json:"context_tokens"`
	Threads       int    `json:"threads"`
}

type runnerGenerateCommand struct {
	Type     string                 `json:"type"`
	ID       string                 `json:"id"`
	Input    protocol.Input         `json:"input"`
	Settings runnerGenerateSettings `json:"settings"`
}

type runnerGenerateSettings struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Stream      bool     `json:"stream"`
	Stop        []string `json:"stop,omitempty"`
}

type runnerIDCommand struct {
	Type string `json:"type"`
	ID   string `json:"id"`
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
	model, err := p.registry.Resolve(request.Model)
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
	p.mu.Lock()
	defer p.mu.Unlock()
	p.epoch++
	epoch := p.epoch
	p.stopCooldownLocked()

	session, err := p.sessionFor(ctx, model, "configure-"+request.ID)
	if err != nil {
		return err
	}
	defer p.scheduleCooldownLocked(model.Name, epoch)

	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.write(runnerIDCommand{Type: "cancel", ID: request.ID})
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	if err := session.write(runnerGenerateCommand{
		Type:  "generate",
		ID:    request.ID,
		Input: request.Input,
		Settings: runnerGenerateSettings{
			Temperature: request.Settings.Temperature,
			TopP:        request.Settings.TopP,
			MaxTokens:   request.Settings.MaxTokens,
			Stream:      request.Stream,
			Stop:        request.Settings.Stop,
		},
	}); err != nil {
		p.discardSession(ctx)
		return fmt.Errorf("send runner generate: %w", err)
	}

	for {
		event, err := session.readEvent(ctx)
		if err != nil {
			p.discardSession(ctx)
			return err
		}
		switch event.Type {
		case "started":
			if !sendRunnerEvent(ctx, events, protocol.Event{Type: "started", ID: request.ID, Provider: "local", Model: model.Name}) {
				return nil
			}
		case "delta":
			if !sendRunnerEvent(ctx, events, protocol.Event{Type: "delta", ID: request.ID, Text: event.Text}) {
				return nil
			}
		case "completed":
			_ = sendRunnerEvent(ctx, events, protocol.Event{Type: "completed", ID: request.ID, FinishReason: event.FinishReason, Usage: event.Usage, Text: event.Text})
			return nil
		case "cancelled":
			_ = sendRunnerEvent(ctx, events, protocol.Event{Type: "cancelled", ID: request.ID})
			return nil
		case "error":
			_ = sendRunnerEvent(ctx, events, protocol.Event{Type: "error", ID: request.ID, Code: event.Code, Message: event.Message})
			return nil
		default:
			p.logger.Debug("ignoring unknown runner event", "type", event.Type)
		}
	}
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
		session, err := p.startSession(ctx, resident, "configure-idle-resident")
		if err != nil {
			p.logger.Debug("resident model reload after cooldown failed", "model", resident.Name, "error", err)
			return
		}
		p.session = session
	})
}

func (p *RunnerProvider) sessionFor(ctx context.Context, model models.LocalModel, configureID string) (*runnerSession, error) {
	if p.session != nil && p.session.modelName == model.Name {
		return p.session, nil
	}
	if p.session != nil {
		if err := p.session.close(ctx, "shutdown-switch-model"); err != nil {
			p.logger.Debug("runner shutdown during model switch failed", "model", p.session.modelName, "error", err)
		}
		p.session = nil
	}
	session, err := p.startSession(ctx, model, configureID)
	if err != nil {
		return nil, err
	}
	p.session = session
	return session, nil
}

func (p *RunnerProvider) startSession(ctx context.Context, model models.LocalModel, configureID string) (*runnerSession, error) {
	cmd := exec.Command(model.Config.Backend.Command)
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

	var stdinMu sync.Mutex
	lines := readRunnerLines(stdout)
	go p.logRunnerStderr(stderr, model.Name)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	session := &runnerSession{
		modelName: model.Name,
		cmd:       cmd,
		stdin:     stdin,
		lines:     lines,
		done:      done,
		stdinMu:   stdinMu,
	}

	hello, err := session.readEvent(ctx)
	if err != nil {
		_ = session.close(context.Background(), "shutdown-start-failed")
		return nil, err
	}
	if err := validateHello(hello); err != nil {
		_ = session.close(context.Background(), "shutdown-invalid-hello")
		return nil, err
	}

	if err := session.write(runnerConfigureCommand{
		Type:          "configure",
		ID:            configureID,
		ModelPath:     model.ModelPath,
		ContextTokens: model.Config.Runtime.ContextTokens,
		Threads:       model.Config.Runtime.Threads,
	}); err != nil {
		_ = session.close(context.Background(), "shutdown-configure-failed")
		return nil, fmt.Errorf("send runner configure: %w", err)
	}
	if err := waitForReady(ctx, session, configureID); err != nil {
		_ = session.close(context.Background(), "shutdown-not-ready")
		return nil, err
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

func (s *runnerSession) write(command any) error {
	return writeRunnerCommand(&s.stdinMu, s.stdin, command)
}

func (s *runnerSession) readEvent(ctx context.Context) (runnerEvent, error) {
	return readRunnerEvent(ctx, s.lines)
}

func (s *runnerSession) close(ctx context.Context, id string) error {
	_ = s.write(runnerIDCommand{Type: "shutdown", ID: id})
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

func readRunnerLines(stdout io.Reader) <-chan lineResult {
	lines := make(chan lineResult)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			lines <- lineResult{line: line}
		}
		if err := scanner.Err(); err != nil {
			lines <- lineResult{err: err}
		}
	}()
	return lines
}

type lineResult struct {
	line []byte
	err  error
}

func readRunnerEvent(ctx context.Context, lines <-chan lineResult) (runnerEvent, error) {
	select {
	case <-ctx.Done():
		return runnerEvent{}, ctx.Err()
	case result, ok := <-lines:
		if !ok {
			return runnerEvent{}, errors.New("runner stdout closed")
		}
		if result.err != nil {
			return runnerEvent{}, result.err
		}
		var event runnerEvent
		if err := json.Unmarshal(result.line, &event); err != nil {
			return runnerEvent{}, fmt.Errorf("decode runner event: %w", err)
		}
		return event, nil
	}
}

func validateHello(event runnerEvent) error {
	if event.Type != "hello" {
		return fmt.Errorf("expected runner hello event, got %q", event.Type)
	}
	if event.ProtocolVersion != runnerProtocolVersion {
		return fmt.Errorf("unsupported runner protocol version %d", event.ProtocolVersion)
	}
	if event.Runner != "" && event.Runner != "yllama-runner" {
		return fmt.Errorf("unsupported runner %q", event.Runner)
	}
	for _, required := range []string{"generate", "stream", "cancel"} {
		if !hasCapability(event.Capabilities, required) {
			return fmt.Errorf("runner missing required capability %q", required)
		}
	}
	return nil
}

func hasCapability(capabilities []string, required string) bool {
	for _, capability := range capabilities {
		if capability == required {
			return true
		}
	}
	return false
}

type runnerEventReader interface {
	readEvent(ctx context.Context) (runnerEvent, error)
}

func waitForReady(ctx context.Context, reader runnerEventReader, id string) error {
	for {
		event, err := reader.readEvent(ctx)
		if err != nil {
			return err
		}
		switch event.Type {
		case "ready":
			if event.ID == id {
				return nil
			}
		case "error":
			return fmt.Errorf("runner configure failed: %s: %s", event.Code, event.Message)
		}
	}
}

func writeRunnerCommand(mu *sync.Mutex, stdin io.Writer, command any) error {
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	mu.Lock()
	defer mu.Unlock()
	_, err = stdin.Write(data)
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
