package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BrandonYaniz/yllmd/internal/config"
)

func TestModelStorePaths(t *testing.T) {
	root := t.TempDir()
	store := NewModelStore(config.Config{Paths: config.PathsConfig{ModelDir: root}})

	if got := store.CurrentModelPath("fast"); got != filepath.Join(root, "fast", "current", "model.gguf") {
		t.Fatalf("current model path = %q", got)
	}
	if got := store.VersionModelPath("fast", "v1"); got != filepath.Join(root, "fast", "versions", "v1", "model.gguf") {
		t.Fatalf("version model path = %q", got)
	}
}

func TestEnsureLayout(t *testing.T) {
	store := NewModelStore(config.Config{Paths: config.PathsConfig{ModelDir: t.TempDir()}})
	if err := store.EnsureLayout("fast"); err != nil {
		t.Fatalf("EnsureLayout returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.ModelDir("fast"), "versions")); err != nil {
		t.Fatalf("expected versions directory: %v", err)
	}
}

func TestVerifySHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	content := []byte("model bytes")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write model: %v", err)
	}
	sum := sha256.Sum256(content)
	if err := VerifySHA256(path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("VerifySHA256 returned error: %v", err)
	}
}

func TestVerifySHA256Mismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, []byte("model bytes"), 0o600); err != nil {
		t.Fatalf("write model: %v", err)
	}
	wrong := sha256.Sum256([]byte("other bytes"))
	if err := VerifySHA256(path, hex.EncodeToString(wrong[:])); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestActivateVersion(t *testing.T) {
	store := NewModelStore(config.Config{Paths: config.PathsConfig{ModelDir: t.TempDir()}})
	writeVersion(t, store, "fast", "v1")

	activation, err := store.ActivateVersion("fast", "v1")
	if err != nil {
		t.Fatalf("ActivateVersion returned error: %v", err)
	}
	if activation.PreviousTarget != "" {
		t.Fatalf("previous target = %q", activation.PreviousTarget)
	}
	active, err := store.ActiveVersion("fast")
	if err != nil {
		t.Fatalf("ActiveVersion returned error: %v", err)
	}
	if active != "v1" {
		t.Fatalf("active version = %q", active)
	}
	if got, err := os.ReadFile(store.CurrentModelPath("fast")); err != nil || string(got) != "v1" {
		t.Fatalf("read current model = %q, %v", string(got), err)
	}
}

func TestRollbackActivation(t *testing.T) {
	store := NewModelStore(config.Config{Paths: config.PathsConfig{ModelDir: t.TempDir()}})
	writeVersion(t, store, "fast", "v1")
	writeVersion(t, store, "fast", "v2")

	if _, err := store.ActivateVersion("fast", "v1"); err != nil {
		t.Fatalf("activate v1: %v", err)
	}
	activation, err := store.ActivateVersion("fast", "v2")
	if err != nil {
		t.Fatalf("activate v2: %v", err)
	}
	if err := store.RollbackActivation(activation); err != nil {
		t.Fatalf("RollbackActivation returned error: %v", err)
	}
	active, err := store.ActiveVersion("fast")
	if err != nil {
		t.Fatalf("ActiveVersion returned error: %v", err)
	}
	if active != "v1" {
		t.Fatalf("active version = %q", active)
	}
}

func TestActivateVersionRequiresModelFile(t *testing.T) {
	store := NewModelStore(config.Config{Paths: config.PathsConfig{ModelDir: t.TempDir()}})
	if err := os.MkdirAll(store.VersionDir("fast", "v1"), 0o755); err != nil {
		t.Fatalf("mkdir version: %v", err)
	}
	if _, err := store.ActivateVersion("fast", "v1"); err == nil {
		t.Fatal("expected missing model file error")
	}
}

func TestInstallLocalFileActivatesVersion(t *testing.T) {
	store := NewModelStore(config.Config{Paths: config.PathsConfig{ModelDir: t.TempDir()}})
	source, checksum := writeSourceModel(t, []byte("installed model"))

	result, err := store.InstallLocalFile(InstallRequest{
		ModelName:  "fast",
		VersionID:  "v1",
		SourcePath: source,
		SHA256:     checksum,
		CatalogID:  "fast-catalog",
		Activate:   true,
	})
	if err != nil {
		t.Fatalf("InstallLocalFile returned error: %v", err)
	}
	if result.Activation == nil {
		t.Fatal("expected activation")
	}
	if result.ModelPath != store.VersionModelPath("fast", "v1") {
		t.Fatalf("model path = %q", result.ModelPath)
	}
	data, err := os.ReadFile(store.CurrentModelPath("fast"))
	if err != nil {
		t.Fatalf("read current model: %v", err)
	}
	if string(data) != "installed model" {
		t.Fatalf("current model content = %q", data)
	}
	manifestData, err := os.ReadFile(result.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ModelName != "fast" || manifest.VersionID != "v1" || manifest.SHA256 != checksum {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestInstallLocalFileRejectsDuplicateVersion(t *testing.T) {
	store := NewModelStore(config.Config{Paths: config.PathsConfig{ModelDir: t.TempDir()}})
	source, checksum := writeSourceModel(t, []byte("installed model"))
	request := InstallRequest{ModelName: "fast", VersionID: "v1", SourcePath: source, SHA256: checksum}
	if _, err := store.InstallLocalFile(request); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := store.InstallLocalFile(request); err == nil {
		t.Fatal("expected duplicate version error")
	}
}

func TestInstallLocalFileChecksumFailureCleansTemp(t *testing.T) {
	store := NewModelStore(config.Config{Paths: config.PathsConfig{ModelDir: t.TempDir()}})
	source, _ := writeSourceModel(t, []byte("installed model"))
	wrong := sha256.Sum256([]byte("wrong"))
	if _, err := store.InstallLocalFile(InstallRequest{
		ModelName:  "fast",
		VersionID:  "v1",
		SourcePath: source,
		SHA256:     hex.EncodeToString(wrong[:]),
	}); err == nil {
		t.Fatal("expected checksum failure")
	}
	entries, err := os.ReadDir(store.ModelDir("fast"))
	if err != nil {
		t.Fatalf("read model dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) >= len(".install-") && entry.Name()[:len(".install-")] == ".install-" {
			t.Fatalf("temporary install directory was not cleaned: %s", entry.Name())
		}
	}
}

func writeVersion(t *testing.T, store ModelStore, modelName, versionID string) {
	t.Helper()
	if err := os.MkdirAll(store.VersionDir(modelName, versionID), 0o755); err != nil {
		t.Fatalf("mkdir version: %v", err)
	}
	if err := os.WriteFile(store.VersionModelPath(modelName, versionID), []byte(versionID), 0o600); err != nil {
		t.Fatalf("write model: %v", err)
	}
}

func writeSourceModel(t *testing.T, content []byte) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.gguf")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write source model: %v", err)
	}
	sum := sha256.Sum256(content)
	return path, hex.EncodeToString(sum[:])
}
