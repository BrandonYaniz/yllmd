package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
	"github.com/BrandonYaniz/yllmd/internal/providers"
)

func TestCancelMissingRequest(t *testing.T) {
	server := NewServer(testConfig(), nil, nil)
	if server.cancel("missing") {
		t.Fatal("expected missing cancel to return false")
	}
}

func TestCancelQueuedRequest(t *testing.T) {
	server := NewServer(testConfig(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	server.queued["req-1"] = &generateJob{ctx: ctx, cancel: cancel}

	if !server.cancel("req-1") {
		t.Fatal("expected queued cancel to return true")
	}
	if _, ok := server.queued["req-1"]; ok {
		t.Fatal("expected queued request to be removed")
	}
	if ctx.Err() == nil {
		t.Fatal("expected queued request context to be cancelled")
	}
}

func TestCancelActiveRequest(t *testing.T) {
	server := NewServer(testConfig(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	server.active["req-1"] = cancel

	if !server.cancel("req-1") {
		t.Fatal("expected active cancel to return true")
	}
	if ctx.Err() == nil {
		t.Fatal("expected active request context to be cancelled")
	}
}

func TestDaemonStatus(t *testing.T) {
	cfg := testConfig()
	cfg.Routing.DefaultProvider = "local"
	cfg.ModelLifecycle.ResidentModel = "fast"
	cfg.LocalModels = map[string]config.LocalModelConfig{
		"fast": {
			Tier:     "fast",
			Resident: true,
			Backend:  config.LocalBackendConfig{Type: "process", Command: "/bin/runner", Transport: "stdio"},
			Runtime:  config.LocalRuntimeSettings{ContextTokens: 1024, Threads: 2},
		},
	}
	server := NewServer(cfg, nil, nil)
	status := server.daemonStatus()
	if status.Status != "ok" {
		t.Fatalf("status = %q", status.Status)
	}
	if status.Provider != "local" {
		t.Fatalf("provider = %q", status.Provider)
	}
	if status.LoadedModel != "fast" {
		t.Fatalf("loaded model = %q", status.LoadedModel)
	}
	if status.ModelCount != 1 {
		t.Fatalf("model count = %d", status.ModelCount)
	}
}

func TestProvidersRequest(t *testing.T) {
	server := NewServer(testConfig(), nil, nil)
	client := newMemoryClient()

	server.handleRequest(client, protocol.Request{Type: protocol.MessageProviders, ID: "providers-1"})

	event := readMemoryEvent(t, client)
	if event.Type != "providers" || event.Provider != "local" {
		t.Fatalf("unexpected providers event: %#v", event)
	}
}

func TestInstallModel(t *testing.T) {
	cfg := modelTestConfig(t)
	sourcePath, checksum := writeDaemonSourceModel(t, []byte("model bytes"))
	provider := &countingProvider{}
	server := NewServer(cfg, provider, nil)
	client := newMemoryClient()

	server.handleModels(client, protocol.Request{
		Type:    protocol.MessageModels,
		ID:      "models-1",
		Action:  "install",
		Model:   "fast",
		Version: "v1",
		File:    sourcePath,
		SHA256:  checksum,
	})

	event := readMemoryEvent(t, client)
	if event.Type != "installed" {
		t.Fatalf("event type = %q, message = %q", event.Type, event.Message)
	}
	if event.Model != "fast" || event.Version != "v1" {
		t.Fatalf("unexpected install event: %#v", event)
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.ModelDir, "fast", "current", "model.gguf")); err != nil {
		t.Fatalf("expected activated model: %v", err)
	}
	if provider.closeCount != 1 {
		t.Fatalf("provider close count = %d", provider.closeCount)
	}
}

func TestInstallModelWithoutActivationDoesNotReloadProvider(t *testing.T) {
	cfg := modelTestConfig(t)
	sourcePath, checksum := writeDaemonSourceModel(t, []byte("model bytes"))
	provider := &countingProvider{}
	server := NewServer(cfg, provider, nil)
	client := newMemoryClient()
	activate := false

	server.handleModels(client, protocol.Request{
		Type:     protocol.MessageModels,
		ID:       "models-1",
		Action:   "install",
		Model:    "fast",
		Version:  "v1",
		File:     sourcePath,
		SHA256:   checksum,
		Activate: &activate,
	})

	event := readMemoryEvent(t, client)
	if event.Type != "installed" {
		t.Fatalf("event type = %q, message = %q", event.Type, event.Message)
	}
	if provider.closeCount != 0 {
		t.Fatalf("provider close count = %d", provider.closeCount)
	}
}

func TestInstallModelActivationRequiresIdleDaemon(t *testing.T) {
	cfg := modelTestConfig(t)
	sourcePath, checksum := writeDaemonSourceModel(t, []byte("model bytes"))
	server := NewServer(cfg, &countingProvider{}, nil)
	client := newMemoryClient()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.active["req-1"] = cancel

	server.handleModels(client, protocol.Request{
		Type:    protocol.MessageModels,
		ID:      "models-1",
		Action:  "install",
		Model:   "fast",
		Version: "v1",
		File:    sourcePath,
		SHA256:  checksum,
	})

	event := readMemoryEvent(t, client)
	if event.Type != "error" || event.Code != "daemon_busy" {
		t.Fatalf("unexpected event: %#v", event)
	}
	if ctx.Err() != nil {
		t.Fatal("install should not cancel active request")
	}
}

func TestInstallModelWithoutActivationAllowedWhenBusy(t *testing.T) {
	cfg := modelTestConfig(t)
	sourcePath, checksum := writeDaemonSourceModel(t, []byte("model bytes"))
	server := NewServer(cfg, &countingProvider{}, nil)
	client := newMemoryClient()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.active["req-1"] = cancel
	activate := false

	server.handleModels(client, protocol.Request{
		Type:     protocol.MessageModels,
		ID:       "models-1",
		Action:   "install",
		Model:    "fast",
		Version:  "v1",
		File:     sourcePath,
		SHA256:   checksum,
		Activate: &activate,
	})

	event := readMemoryEvent(t, client)
	if event.Type != "installed" {
		t.Fatalf("unexpected event: %#v", event)
	}
	if ctx.Err() != nil {
		t.Fatal("install should not cancel active request")
	}
}

func TestInstallModelRejectsInvalidRequest(t *testing.T) {
	server := NewServer(modelTestConfig(t), nil, nil)
	client := newMemoryClient()

	server.handleModels(client, protocol.Request{
		Type:   protocol.MessageModels,
		ID:     "models-1",
		Action: "install",
		Model:  "fast",
	})

	event := readMemoryEvent(t, client)
	if event.Type != "error" || event.Code != "invalid_request" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestActivateModel(t *testing.T) {
	cfg := modelTestConfig(t)
	firstPath, firstChecksum := writeDaemonSourceModel(t, []byte("first"))
	secondPath, secondChecksum := writeDaemonSourceModel(t, []byte("second"))
	provider := &countingProvider{}
	server := NewServer(cfg, provider, nil)
	server.handleModels(newMemoryClient(), protocol.Request{Type: protocol.MessageModels, ID: "install-1", Action: "install", Model: "fast", Version: "v1", File: firstPath, SHA256: firstChecksum})
	activate := false
	server.handleModels(newMemoryClient(), protocol.Request{Type: protocol.MessageModels, ID: "install-2", Action: "install", Model: "fast", Version: "v2", File: secondPath, SHA256: secondChecksum, Activate: &activate})

	client := newMemoryClient()
	server.handleModels(client, protocol.Request{Type: protocol.MessageModels, ID: "activate-1", Action: "activate", Model: "fast", Version: "v2"})

	event := readMemoryEvent(t, client)
	if event.Type != "activated" {
		t.Fatalf("event type = %q, message = %q", event.Type, event.Message)
	}
	if event.Model != "fast" || event.Version != "v2" {
		t.Fatalf("unexpected activate event: %#v", event)
	}
	data, err := os.ReadFile(filepath.Join(cfg.Paths.ModelDir, "fast", "current", "model.gguf"))
	if err != nil {
		t.Fatalf("read current model: %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("current model content = %q", data)
	}
	if provider.closeCount != 2 {
		t.Fatalf("provider close count = %d", provider.closeCount)
	}
}

func TestActivateModelRequiresIdleDaemon(t *testing.T) {
	cfg := modelTestConfig(t)
	sourcePath, checksum := writeDaemonSourceModel(t, []byte("model bytes"))
	server := NewServer(cfg, &countingProvider{}, nil)
	activate := false
	server.handleModels(newMemoryClient(), protocol.Request{Type: protocol.MessageModels, ID: "install-1", Action: "install", Model: "fast", Version: "v1", File: sourcePath, SHA256: checksum, Activate: &activate})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.active["req-1"] = cancel
	client := newMemoryClient()

	server.handleModels(client, protocol.Request{Type: protocol.MessageModels, ID: "activate-1", Action: "activate", Model: "fast", Version: "v1"})

	event := readMemoryEvent(t, client)
	if event.Type != "error" || event.Code != "daemon_busy" {
		t.Fatalf("unexpected event: %#v", event)
	}
	if ctx.Err() != nil {
		t.Fatal("activate should not cancel active request")
	}
}

func TestListModelVersions(t *testing.T) {
	cfg := modelTestConfig(t)
	firstPath, firstChecksum := writeDaemonSourceModel(t, []byte("first"))
	secondPath, secondChecksum := writeDaemonSourceModel(t, []byte("second"))
	server := NewServer(cfg, nil, nil)
	server.handleModels(newMemoryClient(), protocol.Request{Type: protocol.MessageModels, ID: "install-1", Action: "install", Model: "fast", Version: "v1", File: firstPath, SHA256: firstChecksum})
	activate := false
	server.handleModels(newMemoryClient(), protocol.Request{Type: protocol.MessageModels, ID: "install-2", Action: "install", Model: "fast", Version: "v2", File: secondPath, SHA256: secondChecksum, Activate: &activate})
	client := newMemoryClient()

	server.handleModels(client, protocol.Request{Type: protocol.MessageModels, ID: "versions-1", Action: "versions", Model: "fast"})

	event := readMemoryEvent(t, client)
	if event.Type != "versions" || event.Model != "fast" {
		t.Fatalf("unexpected event: %#v", event)
	}
	if len(event.Versions) != 2 {
		t.Fatalf("version count = %d", len(event.Versions))
	}
	if event.Versions[0].Version != "v1" || !event.Versions[0].Active || event.Versions[0].SHA256 != firstChecksum {
		t.Fatalf("unexpected first version: %#v", event.Versions[0])
	}
	if event.Versions[1].Version != "v2" || event.Versions[1].Active || event.Versions[1].SHA256 != secondChecksum {
		t.Fatalf("unexpected second version: %#v", event.Versions[1])
	}
}

func TestRollbackModel(t *testing.T) {
	cfg := modelTestConfig(t)
	firstPath, firstChecksum := writeDaemonSourceModel(t, []byte("first"))
	secondPath, secondChecksum := writeDaemonSourceModel(t, []byte("second"))
	provider := &countingProvider{}
	server := NewServer(cfg, provider, nil)

	server.handleModels(newMemoryClient(), protocol.Request{Type: protocol.MessageModels, ID: "install-1", Action: "install", Model: "fast", Version: "v1", File: firstPath, SHA256: firstChecksum})
	server.handleModels(newMemoryClient(), protocol.Request{Type: protocol.MessageModels, ID: "install-2", Action: "install", Model: "fast", Version: "v2", File: secondPath, SHA256: secondChecksum})

	client := newMemoryClient()
	server.handleModels(client, protocol.Request{Type: protocol.MessageModels, ID: "rollback-1", Action: "rollback", Model: "fast"})

	event := readMemoryEvent(t, client)
	if event.Type != "rolled_back" {
		t.Fatalf("event type = %q, message = %q", event.Type, event.Message)
	}
	if event.Version != "v1" {
		t.Fatalf("rollback version = %q", event.Version)
	}
	data, err := os.ReadFile(filepath.Join(cfg.Paths.ModelDir, "fast", "current", "model.gguf"))
	if err != nil {
		t.Fatalf("read current model: %v", err)
	}
	if string(data) != "first" {
		t.Fatalf("current model content = %q", data)
	}
	if provider.closeCount != 3 {
		t.Fatalf("provider close count = %d", provider.closeCount)
	}
}

func TestRollbackModelRequiresHistory(t *testing.T) {
	server := NewServer(modelTestConfig(t), nil, nil)
	client := newMemoryClient()

	server.handleModels(client, protocol.Request{Type: protocol.MessageModels, ID: "rollback-1", Action: "rollback", Model: "fast"})

	event := readMemoryEvent(t, client)
	if event.Type != "error" || event.Code != "rollback_failed" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestRollbackModelRequiresIdleDaemon(t *testing.T) {
	cfg := modelTestConfig(t)
	firstPath, firstChecksum := writeDaemonSourceModel(t, []byte("first"))
	secondPath, secondChecksum := writeDaemonSourceModel(t, []byte("second"))
	server := NewServer(cfg, &countingProvider{}, nil)
	server.handleModels(newMemoryClient(), protocol.Request{Type: protocol.MessageModels, ID: "install-1", Action: "install", Model: "fast", Version: "v1", File: firstPath, SHA256: firstChecksum})
	server.handleModels(newMemoryClient(), protocol.Request{Type: protocol.MessageModels, ID: "install-2", Action: "install", Model: "fast", Version: "v2", File: secondPath, SHA256: secondChecksum})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.queued["req-1"] = &generateJob{ctx: ctx, cancel: cancel}
	client := newMemoryClient()

	server.handleModels(client, protocol.Request{Type: protocol.MessageModels, ID: "rollback-1", Action: "rollback", Model: "fast"})

	event := readMemoryEvent(t, client)
	if event.Type != "error" || event.Code != "daemon_busy" {
		t.Fatalf("unexpected event: %#v", event)
	}
	active, err := os.ReadFile(filepath.Join(cfg.Paths.ModelDir, "fast", "current", "model.gguf"))
	if err != nil {
		t.Fatalf("read current model: %v", err)
	}
	if string(active) != "second" {
		t.Fatalf("current model content = %q", active)
	}
}

func TestModelDescriptorsIncludeActiveVersion(t *testing.T) {
	cfg := modelTestConfig(t)
	sourcePath, checksum := writeDaemonSourceModel(t, []byte("model bytes"))
	server := NewServer(cfg, nil, nil)
	server.handleModels(newMemoryClient(), protocol.Request{Type: protocol.MessageModels, ID: "install-1", Action: "install", Model: "fast", Version: "v1", File: sourcePath, SHA256: checksum})

	descriptors := server.modelDescriptors()
	if len(descriptors) != 1 {
		t.Fatalf("descriptor count = %d", len(descriptors))
	}
	if descriptors[0].ProviderMetadata["active_version"] != "v1" {
		t.Fatalf("active version = %q", descriptors[0].ProviderMetadata["active_version"])
	}
}

func TestRunJobWritesCompletedTextOutput(t *testing.T) {
	provider := &scriptedProvider{events: []protocol.Event{
		{Type: "started", ID: "req-1"},
		{Type: "completed", ID: "req-1", Text: "full response"},
	}}
	server := NewServer(modelTestConfig(t), provider, nil)
	stream := false
	client := newMemoryClient()

	server.runJob(&generateJob{
		request: protocol.Request{
			Type:   protocol.MessageGenerate,
			ID:     "req-1",
			Model:  "fast",
			Stream: &stream,
			Input:  &protocol.Input{Kind: "prompt", Prompt: "hello"},
			Settings: protocol.GenerationSettings{Output: &protocol.Output{
				Format:   "text",
				Delivery: "complete",
			}},
		},
		client: client,
		ctx:    context.Background(),
		cancel: func() {},
	})

	conn := client.conn.(*memoryConn)
	if got := conn.buf.String(); got != "full response" {
		t.Fatalf("raw output = %q", got)
	}
	if provider.stream {
		t.Fatal("expected compact provider request")
	}
}

func TestRunJobWritesStreamingTextOutput(t *testing.T) {
	provider := &scriptedProvider{events: []protocol.Event{
		{Type: "started", ID: "req-1"},
		{Type: "delta", ID: "req-1", Text: "hello"},
		{Type: "delta", ID: "req-1", Text: " world"},
		{Type: "completed", ID: "req-1"},
	}}
	server := NewServer(modelTestConfig(t), provider, nil)
	stream := true
	client := newMemoryClient()

	server.runJob(&generateJob{
		request: protocol.Request{
			Type:   protocol.MessageGenerate,
			ID:     "req-1",
			Model:  "fast",
			Stream: &stream,
			Input:  &protocol.Input{Kind: "prompt", Prompt: "hello"},
			Settings: protocol.GenerationSettings{Output: &protocol.Output{
				Format:   "text",
				Delivery: "stream",
			}},
		},
		client: client,
		ctx:    context.Background(),
		cancel: func() {},
	})

	conn := client.conn.(*memoryConn)
	if got := conn.buf.String(); got != "hello world" {
		t.Fatalf("raw output = %q", got)
	}
	if !provider.stream {
		t.Fatal("expected streaming provider request")
	}
}

func TestChgrpSocketSkipsEmptyGroup(t *testing.T) {
	if err := chgrpSocket(filepath.Join(t.TempDir(), "missing.sock"), ""); err != nil {
		t.Fatalf("chgrpSocket returned error: %v", err)
	}
}

func TestChgrpSocketRejectsUnknownGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "socket")
	if err := os.WriteFile(path, []byte("socket placeholder"), 0o600); err != nil {
		t.Fatalf("write placeholder: %v", err)
	}
	if err := chgrpSocket(path, "yllmd-test-group-does-not-exist"); err == nil {
		t.Fatal("expected unknown group error")
	}
}

func testConfig() config.Config {
	return config.Config{
		Queue:          config.QueueConfig{MaxDepth: 1},
		ModelLifecycle: config.ModelLifecycleConfig{IdleCooldown: time.Minute},
	}
}

func modelTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := testConfig()
	cfg.Paths.ModelDir = t.TempDir()
	cfg.ModelLifecycle.ResidentModel = "fast"
	cfg.Routing.DefaultProvider = "local"
	cfg.LocalModels = map[string]config.LocalModelConfig{
		"fast": {
			CatalogID: "fast-catalog",
			Tier:      "fast",
			Resident:  true,
			Backend:   config.LocalBackendConfig{Type: "process", Command: "/bin/runner", Transport: "stdio"},
			Runtime:   config.LocalRuntimeSettings{ContextTokens: 1024, Threads: 2},
		},
	}
	return cfg
}

func writeDaemonSourceModel(t *testing.T, content []byte) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.gguf")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write source model: %v", err)
	}
	sum := sha256.Sum256(content)
	return path, hex.EncodeToString(sum[:])
}

func newMemoryClient() *clientConn {
	return &clientConn{conn: &memoryConn{}}
}

func readMemoryEvent(t *testing.T, client *clientConn) protocol.Event {
	t.Helper()
	conn, ok := client.conn.(*memoryConn)
	if !ok {
		t.Fatal("client is not using memory conn")
	}
	var event protocol.Event
	if err := json.Unmarshal(bytes.TrimSpace(conn.buf.Bytes()), &event); err != nil {
		t.Fatalf("decode event: %v; raw=%q", err, conn.buf.String())
	}
	return event
}

type memoryConn struct {
	buf bytes.Buffer
}

func (c *memoryConn) Read(_ []byte) (int, error) {
	return 0, os.ErrClosed
}

func (c *memoryConn) Write(p []byte) (int, error) {
	return c.buf.Write(p)
}

func (c *memoryConn) Close() error {
	return nil
}

func (c *memoryConn) LocalAddr() net.Addr {
	return dummyAddr("local")
}

func (c *memoryConn) RemoteAddr() net.Addr {
	return dummyAddr("remote")
}

func (c *memoryConn) SetDeadline(time.Time) error {
	return nil
}

func (c *memoryConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *memoryConn) SetWriteDeadline(time.Time) error {
	return nil
}

type dummyAddr string

func (a dummyAddr) Network() string {
	return string(a)
}

func (a dummyAddr) String() string {
	return string(a)
}

type countingProvider struct {
	closeCount int
}

func (p *countingProvider) ID() string {
	return "local"
}

func (p *countingProvider) Generate(context.Context, providers.GenerateRequest) (<-chan protocol.Event, error) {
	events := make(chan protocol.Event)
	close(events)
	return events, nil
}

func (p *countingProvider) Close(context.Context) error {
	p.closeCount++
	return nil
}

type scriptedProvider struct {
	events []protocol.Event
	stream bool
}

func (p *scriptedProvider) ID() string {
	return "local"
}

func (p *scriptedProvider) Generate(ctx context.Context, request providers.GenerateRequest) (<-chan protocol.Event, error) {
	p.stream = request.Stream
	events := make(chan protocol.Event, len(p.events))
	for _, event := range p.events {
		events <- event
	}
	close(events)
	return events, nil
}
