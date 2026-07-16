package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/config"
)

type LicenseAcceptance struct {
	FamilyID       string    `json:"family_id"`
	LicenseName    string    `json:"license_name"`
	TermsURL       string    `json:"terms_url"`
	CatalogVersion string    `json:"catalog_version"`
	AcceptedAt     time.Time `json:"accepted_at"`
}

type LicenseStore struct {
	root string
}

func NewLicenseStore(cfg config.Config) LicenseStore {
	root := filepath.Join(cfg.Paths.StateDir, "licenses")
	if cfg.Paths.StateDir == "" {
		root = filepath.Join(cfg.Paths.ModelDir, ".licenses")
	}
	return LicenseStore{root: root}
}

func (s LicenseStore) Accept(familyID, licenseName, termsURL, catalogVersion string) (LicenseAcceptance, error) {
	familyID = cleanPathPart(familyID)
	if familyID == "unnamed" || strings.TrimSpace(licenseName) == "" || strings.TrimSpace(termsURL) == "" {
		return LicenseAcceptance{}, fmt.Errorf("family id, license name, and terms URL are required")
	}
	acceptance := LicenseAcceptance{
		FamilyID: familyID, LicenseName: licenseName, TermsURL: termsURL,
		CatalogVersion: catalogVersion, AcceptedAt: time.Now().UTC(),
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return LicenseAcceptance{}, err
	}
	tmp, err := os.CreateTemp(s.root, ".acceptance-*.tmp")
	if err != nil {
		return LicenseAcceptance{}, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return LicenseAcceptance{}, err
	}
	data, err := json.MarshalIndent(acceptance, "", "  ")
	if err == nil {
		_, err = tmp.Write(append(data, '\n'))
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return LicenseAcceptance{}, err
	}
	if err := os.Rename(tmpPath, s.path(familyID)); err != nil {
		return LicenseAcceptance{}, err
	}
	return acceptance, nil
}

func (s LicenseStore) Accepted(familyID, licenseName, termsURL string) (bool, *LicenseAcceptance, error) {
	acceptance, err := s.read(familyID)
	if os.IsNotExist(err) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	current := acceptance.LicenseName == licenseName && acceptance.TermsURL == termsURL
	return current, acceptance, nil
}

func (s LicenseStore) List() ([]LicenseAcceptance, error) {
	entries, err := os.ReadDir(s.root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	acceptances := make([]LicenseAcceptance, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		var acceptance LicenseAcceptance
		if err := json.Unmarshal(data, &acceptance); err != nil {
			return nil, fmt.Errorf("decode license acceptance %s: %w", entry.Name(), err)
		}
		acceptances = append(acceptances, acceptance)
	}
	sort.Slice(acceptances, func(i, j int) bool { return acceptances[i].FamilyID < acceptances[j].FamilyID })
	return acceptances, nil
}

func (s LicenseStore) read(familyID string) (*LicenseAcceptance, error) {
	data, err := os.ReadFile(s.path(familyID))
	if err != nil {
		return nil, err
	}
	var acceptance LicenseAcceptance
	if err := json.Unmarshal(data, &acceptance); err != nil {
		return nil, err
	}
	return &acceptance, nil
}

func (s LicenseStore) path(familyID string) string {
	return filepath.Join(s.root, cleanPathPart(familyID)+".json")
}
