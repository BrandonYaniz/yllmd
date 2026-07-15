package catalog

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed catalog.yaml
var embeddedCatalog []byte

var runnerVersionPattern = regexp.MustCompile(`^[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{2}(?:-Release)?$`)

type Catalog struct {
	SchemaVersion  int      `yaml:"schema_version" json:"schema_version"`
	CatalogVersion string   `yaml:"catalog_version" json:"catalog_version"`
	Families       []Family `yaml:"families" json:"families"`
}

type Family struct {
	ID          string    `yaml:"id" json:"id"`
	Name        string    `yaml:"name" json:"name"`
	Publisher   string    `yaml:"publisher" json:"publisher"`
	Countries   []string  `yaml:"countries" json:"countries"`
	Description string    `yaml:"description" json:"description"`
	License     License   `yaml:"license" json:"license"`
	Variants    []Variant `yaml:"variants" json:"variants"`
}

type License struct {
	Name               string `yaml:"name" json:"name"`
	SPDXID             string `yaml:"spdx_id" json:"spdx_id,omitempty"`
	Category           string `yaml:"category" json:"category"`
	CommercialUse      bool   `yaml:"commercial_use" json:"commercial_use"`
	AcceptanceRequired bool   `yaml:"acceptance_required" json:"acceptance_required"`
	TermsURL           string `yaml:"terms_url" json:"terms_url"`
}

type Variant struct {
	ID              string    `yaml:"id" json:"id"`
	Name            string    `yaml:"name" json:"name"`
	ModelType       string    `yaml:"model_type" json:"model_type"`
	Level           string    `yaml:"level" json:"level"`
	ParameterCount  string    `yaml:"parameter_count" json:"parameter_count"`
	Capabilities    []string  `yaml:"capabilities" json:"capabilities"`
	Status          string    `yaml:"status" json:"status"`
	RecommendedRAM  uint64    `yaml:"recommended_ram_bytes" json:"recommended_ram_bytes,omitempty"`
	ExpectedStorage uint64    `yaml:"expected_storage_bytes" json:"expected_storage_bytes,omitempty"`
	Artifact        *Artifact `yaml:"artifact,omitempty" json:"artifact,omitempty"`
}

type Artifact struct {
	Format         string `yaml:"format" json:"format"`
	Quantization   string `yaml:"quantization" json:"quantization"`
	Repository     string `yaml:"repository" json:"repository"`
	Revision       string `yaml:"revision" json:"revision"`
	URL            string `yaml:"url" json:"url"`
	Filename       string `yaml:"filename" json:"filename"`
	SizeBytes      uint64 `yaml:"size_bytes" json:"size_bytes"`
	SHA256         string `yaml:"sha256" json:"sha256"`
	MinimumRunner  string `yaml:"minimum_runner_version" json:"minimum_runner_version"`
	PromptTemplate string `yaml:"prompt_template" json:"prompt_template"`
}

func Load() (Catalog, error) {
	return Decode(embeddedCatalog)
}

func Decode(data []byte) (Catalog, error) {
	var catalog Catalog
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode catalog: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func (c Catalog) Validate() error {
	var errs []error
	if c.SchemaVersion != 1 {
		errs = append(errs, fmt.Errorf("unsupported catalog schema version %d", c.SchemaVersion))
	}
	if c.CatalogVersion == "" {
		errs = append(errs, errors.New("catalog_version is required"))
	}
	if len(c.Families) == 0 {
		errs = append(errs, errors.New("at least one model family is required"))
	}
	familyIDs := make(map[string]struct{}, len(c.Families))
	variantIDs := make(map[string]struct{})
	for i, family := range c.Families {
		path := fmt.Sprintf("families[%d]", i)
		if family.ID == "" || family.Name == "" || family.Publisher == "" {
			errs = append(errs, fmt.Errorf("%s requires id, name, and publisher", path))
		}
		if _, exists := familyIDs[family.ID]; exists {
			errs = append(errs, fmt.Errorf("duplicate family id %q", family.ID))
		}
		familyIDs[family.ID] = struct{}{}
		if len(family.Countries) == 0 {
			errs = append(errs, fmt.Errorf("%s.countries is required", path))
		}
		if family.License.Name == "" || family.License.Category == "" || family.License.TermsURL == "" {
			errs = append(errs, fmt.Errorf("%s.license requires name, category, and terms_url", path))
		}
		if len(family.Variants) == 0 {
			errs = append(errs, fmt.Errorf("%s requires at least one variant", path))
		}
		for j, variant := range family.Variants {
			variantPath := fmt.Sprintf("%s.variants[%d]", path, j)
			if variant.ID == "" || variant.Name == "" || variant.ParameterCount == "" {
				errs = append(errs, fmt.Errorf("%s requires id, name, and parameter_count", variantPath))
			}
			if _, exists := variantIDs[variant.ID]; exists {
				errs = append(errs, fmt.Errorf("duplicate variant id %q", variant.ID))
			}
			variantIDs[variant.ID] = struct{}{}
			if variant.ModelType != "llm" && variant.ModelType != "code" {
				errs = append(errs, fmt.Errorf("%s.model_type %q is not supported", variantPath, variant.ModelType))
			}
			if variant.Level != "fast" && variant.Level != "balanced" && variant.Level != "deep" {
				errs = append(errs, fmt.Errorf("%s.level %q is not supported", variantPath, variant.Level))
			}
			if variant.Status != "planned" && variant.Status != "available" && variant.Status != "deprecated" {
				errs = append(errs, fmt.Errorf("%s.status %q is not supported", variantPath, variant.Status))
			}
			if variant.Status == "available" && variant.Artifact == nil {
				errs = append(errs, fmt.Errorf("%s.artifact is required when status is available", variantPath))
			}
			if variant.Status == "available" && (variant.RecommendedRAM == 0 || variant.ExpectedStorage == 0) {
				errs = append(errs, fmt.Errorf("%s requires recommended_ram_bytes and expected_storage_bytes when status is available", variantPath))
			}
			if variant.Artifact != nil {
				if err := validateArtifact(*variant.Artifact); err != nil {
					errs = append(errs, fmt.Errorf("%s.artifact: %w", variantPath, err))
				}
				if variant.ExpectedStorage > 0 && variant.ExpectedStorage != variant.Artifact.SizeBytes {
					errs = append(errs, fmt.Errorf("%s.expected_storage_bytes must equal artifact.size_bytes", variantPath))
				}
			}
		}
	}
	return errors.Join(errs...)
}

func validateArtifact(artifact Artifact) error {
	var errs []error
	if artifact.Format != "gguf" {
		errs = append(errs, fmt.Errorf("format %q is not supported", artifact.Format))
	}
	if artifact.Quantization == "" || artifact.Repository == "" || artifact.URL == "" || artifact.Filename == "" {
		errs = append(errs, errors.New("quantization, repository, url, and filename are required"))
	}
	if artifact.SizeBytes == 0 {
		errs = append(errs, errors.New("size_bytes must be positive"))
	}
	checksum := strings.ToLower(strings.TrimSpace(artifact.SHA256))
	if decoded, err := hex.DecodeString(checksum); err != nil || len(decoded) != 32 {
		errs = append(errs, errors.New("sha256 must be a 64-character hexadecimal digest"))
	}
	revision := strings.TrimSpace(artifact.Revision)
	if decoded, err := hex.DecodeString(revision); err != nil || len(decoded) != 20 {
		errs = append(errs, errors.New("revision must be a full 40-character Git commit hash"))
	}
	if artifact.MinimumRunner == "" || artifact.PromptTemplate == "" {
		errs = append(errs, errors.New("minimum_runner_version and prompt_template are required"))
	}
	if artifact.MinimumRunner != "" && !runnerVersionPattern.MatchString(artifact.MinimumRunner) {
		errs = append(errs, errors.New("minimum_runner_version must use YY.MM.DD.NN[-Release] format"))
	}
	if artifact.PromptTemplate != "qwen2.5-chatml" {
		errs = append(errs, fmt.Errorf("prompt_template %q is not supported", artifact.PromptTemplate))
	}
	parsedURL, err := url.Parse(artifact.URL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		errs = append(errs, errors.New("url must be an absolute HTTPS URL"))
	} else if revision != "" && !strings.Contains(parsedURL.EscapedPath(), "/resolve/"+revision+"/") {
		errs = append(errs, errors.New("url must pin the declared revision"))
	}
	return errors.Join(errs...)
}

func (c Catalog) Family(id string) (Family, bool) {
	for _, family := range c.Families {
		if family.ID == id {
			return family, true
		}
	}
	return Family{}, false
}

func (c Catalog) Variant(id string) (Family, Variant, bool) {
	for _, family := range c.Families {
		for _, variant := range family.Variants {
			if variant.ID == id {
				return family, variant, true
			}
		}
	}
	return Family{}, Variant{}, false
}

func (c Catalog) SortedFamilies() []Family {
	families := append([]Family(nil), c.Families...)
	sort.Slice(families, func(i, j int) bool { return families[i].Name < families[j].Name })
	return families
}
