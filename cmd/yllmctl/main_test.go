package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BrandonYaniz/yllmd/internal/catalog"
	"github.com/BrandonYaniz/yllmd/internal/machine"
)

func TestCatalogInstallSelectionDirectVariant(t *testing.T) {
	selected, err := catalogInstallSelection("qwen25-coder-7b-instruct", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "qwen25-coder-7b-instruct" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestCatalogInstallSelectionMultipleFamilyVariants(t *testing.T) {
	requested := []string{"qwen25-coder-3b-instruct", "qwen25-coder-7b-instruct"}
	selected, err := catalogInstallSelection("qwen-coder", requested, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0] != requested[0] || selected[1] != requested[1] {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestCatalogInstallSelectionRejectsWrongFamily(t *testing.T) {
	_, err := catalogInstallSelection("qwen-coder", []string{"phi4-mini-instruct"}, false)
	if err == nil {
		t.Fatal("expected family membership error")
	}
}

func TestCatalogInstallSelectionRejectsDuplicate(t *testing.T) {
	_, err := catalogInstallSelection("qwen-coder", []string{"qwen25-coder-3b-instruct", "qwen25-coder-3b-instruct"}, false)
	if err == nil {
		t.Fatal("expected duplicate selection error")
	}
}

func TestCatalogInstallSelectionAllIncludesOnlyQualifiedVariants(t *testing.T) {
	selected, err := catalogInstallSelection("qwen-coder", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selected, ",") != "qwen25-coder-1.5b-instruct,qwen25-coder-3b-instruct,qwen25-coder-7b-instruct" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestGuidedModelSelectionOffersMultipleCoderVariants(t *testing.T) {
	modelCatalog, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	selected, accepted, err := guidedModelSelection(strings.NewReader("qwen-coder\nall\n"), &output, modelCatalog, machine.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	want := "qwen25-coder-1.5b-instruct,qwen25-coder-3b-instruct,qwen25-coder-7b-instruct"
	if strings.Join(selected, ",") != want || accepted {
		t.Fatalf("selected = %#v, accepted = %t", selected, accepted)
	}
	for _, expected := range []string{"qwen25-coder-1.5b-instruct", "qwen25-coder-3b-instruct", "qwen25-coder-7b-instruct", "(3 available)"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output missing %q:\n%s", expected, output.String())
		}
	}
}

func TestSelectedVariantsSupportsNumbersIDsAndAll(t *testing.T) {
	variants := []catalog.Variant{{ID: "small"}, {ID: "large"}}
	selected, err := selectedVariants("1, large", variants)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selected, ",") != "small,large" {
		t.Fatalf("selected = %#v", selected)
	}
	selected, err = selectedVariants("all", variants)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selected, ",") != "small,large" {
		t.Fatalf("all selected = %#v", selected)
	}
}

func TestSelectedVariantsRejectsDuplicate(t *testing.T) {
	variants := []catalog.Variant{{ID: "small"}, {ID: "large"}}
	if _, err := selectedVariants("1,small", variants); err == nil {
		t.Fatal("expected duplicate selection error")
	}
}

func TestGuidedModelSelectionAcceptsRequiredLicense(t *testing.T) {
	modelCatalog, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	selected, accepted, err := guidedModelSelection(strings.NewReader("google-gemma\nall\nyes\n"), &output, modelCatalog, machine.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selected, ",") != "gemma3-1b-it" || !accepted {
		t.Fatalf("selected = %#v, accepted = %t", selected, accepted)
	}
	for _, expected := range []string{"Available model families", "Google Gemma", "Gemma Terms of Use", "gemma3-1b-it"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output missing %q:\n%s", expected, output.String())
		}
	}
}
