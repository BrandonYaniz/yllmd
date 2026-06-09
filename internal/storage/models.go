package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BrandonYaniz/yllmd/internal/config"
)

type ModelStore struct {
	root string
}

func NewModelStore(cfg config.Config) ModelStore {
	root := cfg.Paths.ModelDir
	if root == "" {
		root = filepath.Join(cfg.Paths.StateDir, "models")
	}
	return ModelStore{root: root}
}

func (s ModelStore) Root() string {
	return s.root
}

func (s ModelStore) ModelDir(modelName string) string {
	return filepath.Join(s.root, cleanPathPart(modelName))
}

func (s ModelStore) CurrentDir(modelName string) string {
	return filepath.Join(s.ModelDir(modelName), "current")
}

func (s ModelStore) CurrentModelPath(modelName string) string {
	return filepath.Join(s.CurrentDir(modelName), "model.gguf")
}

func (s ModelStore) VersionDir(modelName, versionID string) string {
	return filepath.Join(s.ModelDir(modelName), "versions", cleanPathPart(versionID))
}

func (s ModelStore) VersionModelPath(modelName, versionID string) string {
	return filepath.Join(s.VersionDir(modelName, versionID), "model.gguf")
}

func (s ModelStore) EnsureLayout(modelName string) error {
	return os.MkdirAll(filepath.Join(s.ModelDir(modelName), "versions"), 0o755)
}

func VerifySHA256(path, expectedHex string) error {
	expectedHex = strings.ToLower(strings.TrimSpace(expectedHex))
	if len(expectedHex) != sha256.Size*2 {
		return fmt.Errorf("expected checksum must be %d hex characters", sha256.Size*2)
	}
	if _, err := hex.DecodeString(expectedHex); err != nil {
		return fmt.Errorf("expected checksum is not valid hex: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expectedHex {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHex, actual)
	}
	return nil
}

func cleanPathPart(value string) string {
	value = filepath.Base(filepath.Clean(value))
	value = strings.Trim(value, string(filepath.Separator))
	if value == "." || value == "" {
		return "unnamed"
	}
	return value
}
