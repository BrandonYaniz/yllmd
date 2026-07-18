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
	data := readExampleConfig(t)
	data = []byte(strings.ReplaceAll(string(data), "default_provider: local", "default_provider: openai"))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected remote default provider to be rejected")
	}
}

func TestRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := readExampleConfig(t)
	data = []byte(strings.Replace(string(data), "transport: stdio", "transport: stdio\n      url: http://127.0.0.1:8080", 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown backend field to be rejected")
	}
}

func TestRejectsUnsupportedUpdatePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := readExampleConfig(t)
	data = []byte(strings.ReplaceAll(string(data), "default_policy: notify", "default_policy: sometimes"))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unsupported update policy to be rejected")
	}
}

func TestRejectsUnsupportedModelType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := readExampleConfig(t)
	data = []byte(strings.Replace(string(data), "model_type: llm", "model_type: image", 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unsupported model type to be rejected")
	}
}

func TestRejectsInvalidGPULayers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := readExampleConfig(t)
	data = []byte(strings.Replace(string(data), "gpu_layers: 0", "gpu_layers: -2", 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected invalid gpu_layers to be rejected")
	}
}

func readExampleConfig(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	return data
}
