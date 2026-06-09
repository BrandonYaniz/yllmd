package models

import (
	"testing"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/config"
)

func TestRegistryResolvesByNameAndTier(t *testing.T) {
	registry := NewRegistry(testConfig())
	byName, err := registry.Resolve("fast")
	if err != nil {
		t.Fatalf("resolve by name: %v", err)
	}
	if byName.Name != "fast" {
		t.Fatalf("name = %q", byName.Name)
	}

	byTier, err := registry.Resolve("balanced")
	if err != nil {
		t.Fatalf("resolve by tier: %v", err)
	}
	if byTier.Name != "balanced-model" {
		t.Fatalf("tier resolved to %q", byTier.Name)
	}
}

func TestRegistryDescriptorsAreStable(t *testing.T) {
	descriptors := NewRegistry(testConfig()).Descriptors()
	if len(descriptors) != 2 {
		t.Fatalf("descriptor count = %d", len(descriptors))
	}
	if descriptors[0].Name != "balanced-model" {
		t.Fatalf("first descriptor = %q", descriptors[0].Name)
	}
	if descriptors[1].Name != "fast" {
		t.Fatalf("second descriptor = %q", descriptors[1].Name)
	}
	if !descriptors[1].Resident {
		t.Fatal("expected fast to be resident")
	}
}

func testConfig() config.Config {
	return config.Config{
		ModelLifecycle: config.ModelLifecycleConfig{
			ResidentModel:   "fast",
			IdleCooldown:    time.Minute,
			MaxLoadedModels: 1,
		},
		Paths: config.PathsConfig{ModelDir: "/models"},
		LocalModels: map[string]config.LocalModelConfig{
			"fast": {
				CatalogID: "fast-catalog",
				Tier:      "fast",
				Resident:  true,
				Runtime:   config.LocalRuntimeSettings{ContextTokens: 1024, Threads: 2},
			},
			"balanced-model": {
				CatalogID: "balanced-catalog",
				Tier:      "balanced",
				Runtime:   config.LocalRuntimeSettings{ContextTokens: 2048, Threads: 4},
			},
		},
	}
}
