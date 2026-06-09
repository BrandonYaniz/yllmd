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

type Activation struct {
	ModelName      string
	VersionID      string
	PreviousTarget string
}

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

func (s ModelStore) ActiveVersion(modelName string) (string, error) {
	target, err := os.Readlink(s.CurrentDir(modelName))
	if err != nil {
		return "", err
	}
	return filepath.Base(filepath.Clean(target)), nil
}

func (s ModelStore) ActivateVersion(modelName, versionID string) (Activation, error) {
	versionDir := s.VersionDir(modelName, versionID)
	if info, err := os.Stat(versionDir); err != nil {
		return Activation{}, err
	} else if !info.IsDir() {
		return Activation{}, fmt.Errorf("version path is not a directory: %s", versionDir)
	}
	if _, err := os.Stat(filepath.Join(versionDir, "model.gguf")); err != nil {
		return Activation{}, fmt.Errorf("version is missing model.gguf: %w", err)
	}
	if err := os.MkdirAll(s.ModelDir(modelName), 0o755); err != nil {
		return Activation{}, err
	}

	current := s.CurrentDir(modelName)
	previous, err := os.Readlink(current)
	if err != nil && !os.IsNotExist(err) {
		return Activation{}, err
	}
	tmp := current + ".next"
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join("versions", cleanPathPart(versionID)), tmp); err != nil {
		return Activation{}, err
	}
	if err := os.Rename(tmp, current); err != nil {
		_ = os.Remove(tmp)
		return Activation{}, err
	}
	return Activation{ModelName: cleanPathPart(modelName), VersionID: cleanPathPart(versionID), PreviousTarget: previous}, nil
}

func (s ModelStore) RollbackActivation(activation Activation) error {
	if activation.PreviousTarget == "" {
		return os.Remove(s.CurrentDir(activation.ModelName))
	}
	current := s.CurrentDir(activation.ModelName)
	tmp := current + ".rollback"
	_ = os.Remove(tmp)
	if err := os.Symlink(activation.PreviousTarget, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, current); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
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
