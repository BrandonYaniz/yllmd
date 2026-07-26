package configgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrandonYaniz/yllmd/internal/catalog"
	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/locations"
)

func TestGenerateProducesLoadableConfig(t *testing.T) {
	paths, err := locations.Resolve(locations.ModeUser, "linux", "amd64", "/home/alice")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Generate(Options{
		Mode:  locations.ModeUser,
		Paths: paths,
		Variants: []catalog.Variant{
			{ID: "general-fast", ModelType: "llm", Level: "fast"},
			{ID: "code-balanced", ModelType: "code", Level: "balanced"},
		},
		ResidentID: "general-fast", Threads: 8, GPULayers: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "config_version") || strings.Contains(string(data), "local_models") {
		t.Fatalf("generated obsolete schema fields:\n%s", data)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load generated config: %v\n%s", err, data)
	}
	if loaded.OperatingMode != "user" || loaded.ModelLifecycle.ResidentModel != "general-fast" {
		t.Fatalf("loaded config = %#v", loaded)
	}
	if loaded.Paths.ModelDir != "/home/alice/yllmd/models" {
		t.Fatalf("model dir = %q", loaded.Paths.ModelDir)
	}
	if loaded.Models["general-fast"].Runtime.GPULayers != -1 {
		t.Fatalf("gpu layers = %d", loaded.Models["general-fast"].Runtime.GPULayers)
	}
}

func TestGenerateRejectsInvalidGPULayers(t *testing.T) {
	_, err := Generate(Options{
		Mode: locations.ModeUser, Paths: locations.Paths{ModelDir: "/models"},
		Variants: []catalog.Variant{{ID: "one", ModelType: "llm", Level: "fast"}},
		Threads:  4, GPULayers: -2,
	})
	if err == nil {
		t.Fatal("expected gpu layer validation error")
	}
}

func TestGenerateRejectsDuplicateRoles(t *testing.T) {
	_, err := Generate(Options{
		Mode:  locations.ModeUser,
		Paths: locations.Paths{ModelDir: "/models"},
		Variants: []catalog.Variant{
			{ID: "one", ModelType: "code", Level: "fast"},
			{ID: "two", ModelType: "code", Level: "fast"},
		},
		Threads: 4,
	})
	if err == nil {
		t.Fatal("expected duplicate role error")
	}
}
