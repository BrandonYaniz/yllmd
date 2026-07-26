package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BrandonYaniz/yllmd/internal/catalog"
	"github.com/BrandonYaniz/yllmd/internal/machine"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
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

func TestCatalogUpdateStatuses(t *testing.T) {
	modelCatalog, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, current, _ := modelCatalog.Variant("qwen25-coder-1.5b-instruct")
	installed := []protocol.InstalledModel{
		{Name: "local-only", ActiveVersion: "v1", Versions: []protocol.ModelVersion{{Version: "v1"}}},
		{Name: "qwen25-coder-3b-instruct", Configured: true, ActiveVersion: "old", Versions: []protocol.ModelVersion{{Version: "old"}}},
		{Name: "qwen25-coder-1.5b-instruct", Configured: true, ActiveVersion: "old", Versions: []protocol.ModelVersion{{Version: "old"}, {Version: current.Artifact.Revision}}},
	}
	rows := catalogUpdateStatuses(modelCatalog, installed)
	if len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].Model != "qwen25-coder-1.5b-instruct" || rows[0].UpdateAvailable || !rows[0].CatalogInstalled {
		t.Fatalf("current row = %#v", rows[0])
	}
	if rows[1].Model != "qwen25-coder-3b-instruct" || !rows[1].UpdateAvailable || rows[1].CatalogInstalled {
		t.Fatalf("outdated row = %#v", rows[1])
	}
}

func TestPendingCatalogUpdatePlansActivateOnlyConfiguredModels(t *testing.T) {
	rows := []catalogUpdateStatus{
		{Model: "outdated", UpdateAvailable: true},
		{Model: "downloaded", Configured: true, ActiveVersion: "old", CatalogVersion: "new", CatalogInstalled: true},
		{Model: "unconfigured", ActiveVersion: "old", CatalogVersion: "new", CatalogInstalled: true},
		{Model: "current", Configured: true, ActiveVersion: "new", CatalogVersion: "new", CatalogInstalled: true},
	}
	plans := pendingCatalogUpdatePlans(rows, false)
	if len(plans) != 1 || plans[0].variant != "outdated" || plans[0].activate {
		t.Fatalf("plans = %#v", plans)
	}
	plans = pendingCatalogUpdatePlans(rows, true)
	if len(plans) != 2 || plans[0] != (catalogActionPlan{variant: "outdated", activate: false}) || plans[1] != (catalogActionPlan{variant: "downloaded", activate: true}) {
		t.Fatalf("activated plans = %#v", plans)
	}
}

func TestGenerationTargetFlags(t *testing.T) {
	tests := []struct {
		name                  string
		model, group, profile string
		want                  *protocol.ModelTarget
		wantError             bool
	}{
		{"exact", "fiction-primary", "", "", &protocol.ModelTarget{Model: "fiction-primary"}, false},
		{"route", "", "writing", "draft-pass1", &protocol.ModelTarget{Group: "writing", Profile: "draft-pass1"}, false},
		{"group", "", "writing", "", &protocol.ModelTarget{Group: "writing"}, false},
		{"profile only", "", "", "draft", nil, true},
		{"mixed exact route", "one", "writing", "", nil, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := generationTarget(test.model, test.group, test.profile)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v", err)
			}
			if target == nil != (test.want == nil) {
				t.Fatalf("target = %#v", target)
			}
			if target != nil && *target != *test.want {
				t.Fatalf("target = %#v, want %#v", target, test.want)
			}
		})
	}
}
