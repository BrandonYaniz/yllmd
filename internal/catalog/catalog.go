package catalog

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

//go:embed catalog.yaml
var embeddedCatalog []byte

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
	ID              string   `yaml:"id" json:"id"`
	Name            string   `yaml:"name" json:"name"`
	ModelType       string   `yaml:"model_type" json:"model_type"`
	Level           string   `yaml:"level" json:"level"`
	ParameterCount  string   `yaml:"parameter_count" json:"parameter_count"`
	Capabilities    []string `yaml:"capabilities" json:"capabilities"`
	Status          string   `yaml:"status" json:"status"`
	RecommendedRAM  uint64   `yaml:"recommended_ram_bytes" json:"recommended_ram_bytes,omitempty"`
	ExpectedStorage uint64   `yaml:"expected_storage_bytes" json:"expected_storage_bytes,omitempty"`
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
		}
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

func (c Catalog) SortedFamilies() []Family {
	families := append([]Family(nil), c.Families...)
	sort.Slice(families, func(i, j int) bool { return families[i].Name < families[j].Name })
	return families
}
