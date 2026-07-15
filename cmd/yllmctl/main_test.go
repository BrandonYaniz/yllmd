package main

import "testing"

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

func TestCatalogInstallSelectionAllRequiresQualifiedVariant(t *testing.T) {
	_, err := catalogInstallSelection("qwen-coder", nil, true)
	if err == nil {
		t.Fatal("expected no qualified variants error")
	}
}
