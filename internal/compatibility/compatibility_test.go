package compatibility

import (
	"strings"
	"testing"

	"github.com/BrandonYaniz/yllmd/internal/catalog"
	"github.com/BrandonYaniz/yllmd/internal/machine"
)

func TestEstimateRequirements(t *testing.T) {
	requirements, err := EstimateRequirements(catalog.Variant{ID: "coder", ParameterCount: "7B"})
	if err != nil {
		t.Fatal(err)
	}
	if requirements.StorageBytes == 0 || requirements.RecommendedRAM <= requirements.StorageBytes {
		t.Fatalf("requirements = %#v", requirements)
	}
}

func TestAssessReportsMemoryAndQualification(t *testing.T) {
	assessment, err := Assess(catalog.Variant{
		ID: "large", ParameterCount: "32B", Status: "planned",
	}, machine.Profile{MemoryBytes: 8 * gibibyte, AvailableDiskBytes: 100 * gibibyte})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Compatible || assessment.Installable {
		t.Fatalf("assessment = %#v", assessment)
	}
	if joined := strings.Join(assessment.Reasons, " "); !strings.Contains(joined, "recommended RAM") || !strings.Contains(joined, "qualification") {
		t.Fatalf("reasons = %q", joined)
	}
}

func TestFormatBytes(t *testing.T) {
	if got := FormatBytes(8 * gibibyte); got != "8 GiB" {
		t.Fatalf("formatted = %q", got)
	}
}
