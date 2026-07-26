package models

import (
	"errors"
	"testing"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
)

func TestRegistryResolvesExactNameAndAlias(t *testing.T) {
	registry := NewRegistry(testConfig())
	for _, identifier := range []string{"fast", "quick"} {
		model, err := registry.Resolve(identifier)
		if err != nil {
			t.Fatalf("resolve %s: %v", identifier, err)
		}
		if model.Name != "fast" {
			t.Fatalf("%s resolved to %q", identifier, model.Name)
		}
	}
}

func TestRegistryResolvesDynamicRoutesAndDefaults(t *testing.T) {
	registry := NewRegistry(testConfig())
	tests := []struct {
		name                  string
		target                protocol.ModelTarget
		model, group, profile string
	}{
		{"exact", protocol.ModelTarget{Model: "balanced-model"}, "balanced-model", "", ""},
		{"route", protocol.ModelTarget{Group: "writing", Profile: "draft-pass1"}, "balanced-model", "writing", "draft-pass1"},
		{"group default", protocol.ModelTarget{Group: "writing"}, "fast", "writing", "structure"},
		{"global default", protocol.ModelTarget{}, "balanced-model", "llm", "balanced"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := registry.ResolveTarget(test.target)
			if err != nil {
				t.Fatal(err)
			}
			if got.ResolvedModel != test.model || got.ResolvedGroup != test.group || got.ResolvedProfile != test.profile {
				t.Fatalf("resolved = %#v", got)
			}
		})
	}
}

func TestRegistryUsesOrderedOperationalFallbacks(t *testing.T) {
	registry := NewRegistry(testConfig())
	got, err := registry.ResolveTargetWithAvailability(protocol.ModelTarget{Group: "llm", Profile: "deep"}, func(model LocalModel) error {
		if model.Name != "fast" {
			return errors.New("unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ResolvedModel != "fast" || !got.UsedFallback || got.FallbackFrom != "deep-model" {
		t.Fatalf("resolved = %#v", got)
	}
	if _, err := registry.ResolveTargetWithAvailability(protocol.ModelTarget{Model: "deep-model"}, func(LocalModel) error { return errors.New("unavailable") }); err == nil {
		t.Fatal("exact unavailable model should fail")
	}
}

func TestRegistryRejectPolicyDoesNotExposeRuntimeFallbacks(t *testing.T) {
	cfg := testConfig()
	cfg.Routing.UnavailableModelPolicy = "reject"
	got, err := NewRegistry(cfg).ResolveTarget(protocol.ModelTarget{Group: "llm", Profile: "deep"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FallbackModels) != 0 {
		t.Fatalf("fallback models = %#v", got.FallbackModels)
	}
}

func TestRegistrySkipsDisabledRuntimeFallback(t *testing.T) {
	cfg := testConfig()
	disabled := false
	model := cfg.Models["balanced-model"]
	model.Enabled = &disabled
	cfg.Models["balanced-model"] = model
	got, err := NewRegistry(cfg).ResolveTarget(protocol.ModelTarget{Group: "llm", Profile: "deep"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FallbackModels) != 1 || got.FallbackModels[0] != "fast" {
		t.Fatalf("fallback models = %#v", got.FallbackModels)
	}
}

func TestRegistryRejectsInvalidTargets(t *testing.T) {
	registry := NewRegistry(testConfig())
	for _, target := range []protocol.ModelTarget{
		{Group: "missing"}, {Group: "writing", Profile: "missing"}, {Profile: "structure"},
		{Model: "fast", Group: "writing"},
	} {
		if _, err := registry.ResolveTarget(target); err == nil {
			t.Fatalf("expected error for %#v", target)
		}
	}
}

func TestRegistryDescriptorsAndRoutesAreStable(t *testing.T) {
	registry := NewRegistry(testConfig())
	descriptors, groups := registry.Descriptors(), registry.Groups()
	if len(descriptors) != 3 || descriptors[0].Name != "balanced-model" || !descriptors[2].Resident {
		t.Fatalf("descriptors = %#v", descriptors)
	}
	if len(groups) != 2 || groups[0].Name != "llm" || groups[1].Name != "writing" {
		t.Fatalf("groups = %#v", groups)
	}
}

func testConfig() config.Config {
	backend := config.LocalBackendConfig{Type: "process", Command: "runner", Transport: "stdio"}
	return config.Config{
		ModelLifecycle: config.ModelLifecycleConfig{ResidentModel: "fast", IdleCooldown: time.Minute, MaxLoadedModels: 1},
		Paths:          config.PathsConfig{ModelDir: "/models"},
		Models: map[string]config.ModelConfig{
			"fast":           {CatalogID: "fast-catalog", Aliases: []string{"quick"}, Backend: backend, Runtime: config.LocalRuntimeSettings{ContextTokens: 1024, Threads: 2}},
			"balanced-model": {CatalogID: "balanced-catalog", Backend: backend, Runtime: config.LocalRuntimeSettings{ContextTokens: 2048, Threads: 4}},
			"deep-model":     {CatalogID: "deep-catalog", Backend: backend, Runtime: config.LocalRuntimeSettings{ContextTokens: 4096, Threads: 4}},
		},
		Routing: config.RoutingConfig{
			Default: config.RouteReference{Group: "llm", Profile: "balanced"}, UnavailableModelPolicy: "use_fallback",
			Groups: map[string]config.GroupConfig{
				"llm": {DefaultProfile: "balanced", Profiles: map[string]config.ProfileConfig{
					"balanced": {Model: "balanced-model"}, "deep": {Model: "deep-model", Fallbacks: []string{"balanced-model", "fast"}},
				}},
				"writing": {DefaultProfile: "structure", Profiles: map[string]config.ProfileConfig{
					"structure": {Model: "fast"}, "draft-pass1": {Model: "balanced-model"}, "draft-pass2": {Model: "balanced-model"},
				}},
			},
		},
	}
}
