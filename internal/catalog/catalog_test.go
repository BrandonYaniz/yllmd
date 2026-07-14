package catalog

import (
	"strings"
	"testing"
)

func TestEmbeddedCatalogIsValid(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Families) < 8 {
		t.Fatalf("family count = %d", len(catalog.Families))
	}
	if family, ok := catalog.Family("qwen-coder"); !ok || len(family.Variants) < 5 {
		t.Fatalf("qwen-coder family = %#v, found = %t", family, ok)
	}
}

func TestDecodeRejectsDuplicateVariantIDs(t *testing.T) {
	data := strings.ReplaceAll(string(embeddedCatalog), "phi4-mini-reasoning", "phi4-mini-instruct")
	if _, err := Decode([]byte(data)); err == nil || !strings.Contains(err.Error(), "duplicate variant id") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	data := append(append([]byte(nil), embeddedCatalog...), []byte("\nunknown: true\n")...)
	if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRequiresArtifactForAvailableVariant(t *testing.T) {
	data := strings.Replace(string(embeddedCatalog), "status: planned", "status: available", 1)
	if _, err := Decode([]byte(data)); err == nil || !strings.Contains(err.Error(), "artifact is required") {
		t.Fatalf("error = %v", err)
	}
}
