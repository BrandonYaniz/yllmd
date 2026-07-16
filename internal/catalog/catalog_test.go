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
	_, variant, ok := catalog.Variant("qwen25-coder-1.5b-instruct")
	if !ok || variant.Status != "available" || variant.Artifact == nil {
		t.Fatalf("qualified Qwen Coder variant = %#v, found = %t", variant, ok)
	}
	if variant.Artifact.SizeBytes != 1117320768 || variant.Artifact.SHA256 != "cc324af070c2ecbfd324a30884d2f951a7ff756aba85cb811a6ec436933bb046" {
		t.Fatalf("qualified artifact = %#v", variant.Artifact)
	}
	for id, expected := range map[string]struct {
		size     uint64
		checksum string
		template string
	}{
		"phi4-mini-instruct":        {2491874688, "01999f17c39cc3074afae5e9c539bc82d45f2dd7faa3917c66cbef76fce8c0c2", "phi4-chat"},
		"gemma3-1b-it":              {806058240, "8ccc5cd1f1b3602548715ae25a66ed73fd5dc68a210412eea643eb20eb75a135", "gemma3-chat"},
		"llama32-1b-instruct":       {807694464, "6f85a640a97cf2bf5b8e764087b1e83da0fdb51d7c9fab7d0fece9385611df83", "llama3-instruct"},
		"granite3.3-2b-instruct":    {1545303328, "ac71e9e32c0bea919b409c5918f69ca74339854b0319c5065e4e9fb6d95c4852", "granite3-chat"},
		"mistral-nemo-12b-instruct": {7477208192, "7c1a10d202d8788dbe5628dc962254d10654c853cae6aaeca0618f05490d4a46", "mistral-nemo-instruct"},
	} {
		_, variant, ok := catalog.Variant(id)
		if !ok || variant.Status != "available" || variant.Artifact == nil {
			t.Fatalf("qualified variant %q = %#v, found = %t", id, variant, ok)
		}
		if variant.Artifact.SizeBytes != expected.size || variant.Artifact.SHA256 != expected.checksum || variant.Artifact.PromptTemplate != expected.template {
			t.Fatalf("qualified artifact %q = %#v", id, variant.Artifact)
		}
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
