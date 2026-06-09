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

func TestInstallModel(t *testing.T) {
	cfg := modelTestConfig(t)
	sourcePath, checksum := writeDaemonSourceModel(t, []byte("model bytes"))
	server := NewServer(cfg, nil, nil)
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
