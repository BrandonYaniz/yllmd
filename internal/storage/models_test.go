package storage

import (
	"crypto/sha256"
	"encoding/hex"
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
