package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/config"
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

func testConfig() config.Config {
	return config.Config{
		Queue:          config.QueueConfig{MaxDepth: 1},
		ModelLifecycle: config.ModelLifecycleConfig{IdleCooldown: time.Minute},
	}
}
