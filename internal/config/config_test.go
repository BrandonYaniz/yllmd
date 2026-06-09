package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExampleConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.SocketPath == "" {
		t.Fatal("expected socket path")
	}
	if cfg.Queue.DefaultTimeout == 0 {
		t.Fatal("expected parsed queue timeout")
	}
	if cfg.ModelLifecycle.IdleCooldown == 0 {
		t.Fatal("expected parsed idle cooldown")
	}
	if cfg.Routing.DefaultProvider != "local" {
		t.Fatalf("expected local default provider, got %q", cfg.Routing.DefaultProvider)
	}
}

func TestRejectsRemoteDefaultProviderForV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	data = []byte(strings.ReplaceAll(string(data), "default_provider: local", "default_provider: openai"))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected remote default provider to be rejected")
	}
}
