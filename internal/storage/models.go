package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/config"
)

type Activation struct {
	ModelName      string `json:"model_name"`
	VersionID      string `json:"version_id"`
	PreviousTarget string `json:"previous_target"`
}

type InstallRequest struct {
	ModelName  string
	VersionID  string
	SourcePath string
	SHA256     string
	CatalogID  string
	Activate   bool
}

type InstallResult struct {
	ModelName    string
	VersionID    string
	ModelPath    string
	ManifestPath string
	ChecksumPath string
	Activation   *Activation
}

type RollbackResult struct {
	ModelName string
	VersionID string
}

type VersionInfo struct {
	VersionID    string
	ModelPath    string
	ManifestPath string
	ChecksumPath string
	Active       bool
	Manifest     *Manifest
}

type Manifest struct {
	ModelName   string    `json:"model_name"`
	VersionID   string    `json:"version_id"`
	CatalogID   string    `json:"catalog_id,omitempty"`
	SHA256      string    `json:"sha256"`
	InstalledAt time.Time `json:"installed_at"`
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

func (s ModelStore) ManifestPath(modelName, versionID string) string {
	return filepath.Join(s.VersionDir(modelName, versionID), "manifest.json")
}

func (s ModelStore) ChecksumPath(modelName, versionID string) string {
	return filepath.Join(s.VersionDir(modelName, versionID), "checksum.sha256")
}

func (s ModelStore) RollbackPath(modelName string) string {
	return filepath.Join(s.ModelDir(modelName), "rollback.json")
}

func (s ModelStore) EnsureLayout(modelName string) error {
	return os.MkdirAll(filepath.Join(s.ModelDir(modelName), "versions"), 0o755)
}

func (s ModelStore) InstallLocalFile(request InstallRequest) (InstallResult, error) {
	modelName := cleanPathPart(request.ModelName)
	versionID := cleanPathPart(request.VersionID)
	if modelName == "unnamed" {
		return InstallResult{}, fmt.Errorf("model name is required")
	}
	if versionID == "unnamed" {
		return InstallResult{}, fmt.Errorf("version id is required")
	}
	if strings.TrimSpace(request.SourcePath) == "" {
		return InstallResult{}, fmt.Errorf("source path is required")
	}
	if request.SHA256 == "" {
		return InstallResult{}, fmt.Errorf("sha256 is required")
	}
	if err := s.EnsureLayout(modelName); err != nil {
		return InstallResult{}, err
	}
	versionDir := s.VersionDir(modelName, versionID)
	if _, err := os.Stat(versionDir); err == nil {
		return InstallResult{}, fmt.Errorf("version already exists: %s", versionID)
	} else if !os.IsNotExist(err) {
		return InstallResult{}, err
	}

	tmpDir, err := os.MkdirTemp(s.ModelDir(modelName), ".install-"+versionID+"-")
	if err != nil {
		return InstallResult{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	tmpModel := filepath.Join(tmpDir, "model.gguf")
	if err := copyFile(tmpModel, request.SourcePath); err != nil {
		return InstallResult{}, err
	}
	if err := VerifySHA256(tmpModel, request.SHA256); err != nil {
		return InstallResult{}, err
	}
	manifest := Manifest{
		ModelName:   modelName,
		VersionID:   versionID,
		CatalogID:   request.CatalogID,
		SHA256:      strings.ToLower(strings.TrimSpace(request.SHA256)),
		InstalledAt: time.Now().UTC(),
	}
	if err := writeManifest(filepath.Join(tmpDir, "manifest.json"), manifest); err != nil {
		return InstallResult{}, err
	}
	if err := writeChecksum(filepath.Join(tmpDir, "checksum.sha256"), manifest.SHA256); err != nil {
		return InstallResult{}, err
	}
	if err := os.Rename(tmpDir, versionDir); err != nil {
		return InstallResult{}, err
	}
	cleanup = false

	result := InstallResult{
		ModelName:    modelName,
		VersionID:    versionID,
		ModelPath:    s.VersionModelPath(modelName, versionID),
		ManifestPath: s.ManifestPath(modelName, versionID),
		ChecksumPath: s.ChecksumPath(modelName, versionID),
	}
	if request.Activate {
		activation, err := s.ActivateVersion(modelName, versionID)
		if err != nil {
			return InstallResult{}, err
		}
		result.Activation = &activation
	}
	return result, nil
}

func (s ModelStore) ActiveVersion(modelName string) (string, error) {
	target, err := os.Readlink(s.CurrentDir(modelName))
	if err != nil {
		return "", err
	}
	return filepath.Base(filepath.Clean(target)), nil
}

func (s ModelStore) ListVersions(modelName string) ([]VersionInfo, error) {
	modelName = cleanPathPart(modelName)
	versionsDir := filepath.Join(s.ModelDir(modelName), "versions")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	activeVersion, _ := s.ActiveVersion(modelName)
	versions := make([]VersionInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		versionID := entry.Name()
		version := VersionInfo{
			VersionID:    versionID,
			ModelPath:    s.VersionModelPath(modelName, versionID),
			ManifestPath: s.ManifestPath(modelName, versionID),
			ChecksumPath: s.ChecksumPath(modelName, versionID),
			Active:       versionID == activeVersion,
		}
		manifest, err := readManifest(version.ManifestPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		version.Manifest = manifest
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].VersionID < versions[j].VersionID
	})
	return versions, nil
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
	activation := Activation{ModelName: cleanPathPart(modelName), VersionID: cleanPathPart(versionID), PreviousTarget: previous}
	if err := writeJSON(s.RollbackPath(modelName), activation); err != nil {
		return Activation{}, err
	}
	return activation, nil
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

func (s ModelStore) RollbackLatest(modelName string) (RollbackResult, error) {
	modelName = cleanPathPart(modelName)
	data, err := os.ReadFile(s.RollbackPath(modelName))
	if err != nil {
		return RollbackResult{}, err
	}
	var activation Activation
	if err := json.Unmarshal(data, &activation); err != nil {
		return RollbackResult{}, err
	}
	if activation.ModelName != modelName {
		return RollbackResult{}, fmt.Errorf("rollback record model %q does not match %q", activation.ModelName, modelName)
	}
	if err := s.RollbackActivation(activation); err != nil {
		return RollbackResult{}, err
	}
	_ = os.Remove(s.RollbackPath(modelName))
	versionID := ""
	if activation.PreviousTarget != "" {
		versionID = filepath.Base(filepath.Clean(activation.PreviousTarget))
	}
	return RollbackResult{ModelName: modelName, VersionID: versionID}, nil
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

func copyFile(dst, src string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer target.Close()
	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return target.Sync()
}

func writeManifest(path string, manifest Manifest) error {
	return writeJSON(path, manifest)
}

func readManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func writeChecksum(path, checksum string) error {
	return os.WriteFile(path, []byte(strings.ToLower(strings.TrimSpace(checksum))+"  model.gguf\n"), 0o600)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func cleanPathPart(value string) string {
	value = filepath.Base(filepath.Clean(value))
	value = strings.Trim(value, string(filepath.Separator))
	if value == "." || value == "" {
		return "unnamed"
	}
	return value
}
