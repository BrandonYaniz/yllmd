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
	if cfg.Routing.Default.Group != "llm" || cfg.Routing.Default.Profile != "balanced" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if len(cfg.Models) != 3 || len(cfg.Routing.Groups) != 2 {
		t.Fatalf("models/groups = %d/%d", len(cfg.Models), len(cfg.Routing.Groups))
	}
}

func TestValidDynamicRouting(t *testing.T) {
	cfg := exampleConfig(t)
	cfg.Routing.Groups["writing"] = GroupConfig{
		DefaultProfile: "structure.v2",
		Profiles: map[string]ProfileConfig{
			"structure.v2": {Model: "general-balanced"},
			"draft-pass1":  {Model: "general-balanced"},
			"draft-pass2":  {Model: "general-balanced", Fallbacks: []string{"general-fast"}},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationValidationFailures(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Config)
		contains string
	}{
		{"missing default group", func(c *Config) { c.Routing.Default.Group = "missing" }, "default group"},
		{"missing default profile", func(c *Config) { c.Routing.Default.Profile = "missing" }, "default profile"},
		{"missing group default", func(c *Config) {
			g := c.Routing.Groups["llm"]
			g.DefaultProfile = "missing"
			c.Routing.Groups["llm"] = g
		}, "default profile"},
		{"unknown primary", func(c *Config) {
			g := c.Routing.Groups["llm"]
			g.Profiles["fast"] = ProfileConfig{Model: "missing"}
			c.Routing.Groups["llm"] = g
		}, "unknown model"},
		{"unknown fallback", func(c *Config) {
			g := c.Routing.Groups["llm"]
			p := g.Profiles["fast"]
			p.Fallbacks = []string{"missing"}
			g.Profiles["fast"] = p
			c.Routing.Groups["llm"] = g
		}, "fallback"},
		{"duplicate fallback", func(c *Config) {
			g := c.Routing.Groups["llm"]
			p := g.Profiles["deep"]
			p.Fallbacks = []string{"general-fast", "general-fast"}
			g.Profiles["deep"] = p
			c.Routing.Groups["llm"] = g
		}, "repeats fallback"},
		{"primary fallback", func(c *Config) {
			g := c.Routing.Groups["llm"]
			p := g.Profiles["fast"]
			p.Fallbacks = []string{"general-fast"}
			g.Profiles["fast"] = p
			c.Routing.Groups["llm"] = g
		}, "repeats primary"},
		{"invalid identifier", func(c *Config) { c.Models["Not Valid"] = c.Models["general-fast"] }, "valid identifier"},
		{"unknown resident", func(c *Config) { c.ModelLifecycle.ResidentModel = "missing" }, "resident model"},
		{"alias collision", func(c *Config) {
			m := c.Models["general-fast"]
			m.Aliases = []string{"general"}
			c.Models["general-fast"] = m
		}, "identifier"},
		{"empty profiles", func(c *Config) { c.Routing.Groups["empty"] = GroupConfig{} }, "at least one profile"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := exampleConfig(t)
			test.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want substring %q", err, test.contains)
			}
		})
	}
}

func TestRejectsOldAndUnknownFields(t *testing.T) {
	if _, err := Parse([]byte("local_models:\n  fast: {}\n")); err == nil {
		t.Fatal("expected old local_models field to be rejected")
	}
	data := readExampleConfig(t)
	data = []byte(strings.Replace(string(data), "transport: stdio}", "transport: stdio, url: nope}", 1))
	if _, err := Parse(data); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func exampleConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := Parse(readExampleConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func readExampleConfig(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
