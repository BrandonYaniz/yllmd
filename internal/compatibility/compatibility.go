package compatibility

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/BrandonYaniz/yllmd/internal/catalog"
	"github.com/BrandonYaniz/yllmd/internal/machine"
)

const gibibyte = uint64(1024 * 1024 * 1024)

type Requirements struct {
	StorageBytes   uint64 `json:"storage_bytes"`
	RecommendedRAM uint64 `json:"recommended_ram_bytes"`
	Source         string `json:"source"`
}

type Assessment struct {
	Variant            catalog.Variant `json:"variant"`
	Requirements       Requirements    `json:"requirements"`
	Compatible         bool            `json:"compatible"`
	CompatibilityKnown bool            `json:"compatibility_known"`
	Installable        bool            `json:"installable"`
	Reasons            []string        `json:"reasons,omitempty"`
}

func Assess(variant catalog.Variant, profile machine.Profile) (Assessment, error) {
	requirements, err := EstimateRequirements(variant)
	if err != nil {
		return Assessment{}, err
	}
	assessment := Assessment{
		Variant:            variant,
		Requirements:       requirements,
		Compatible:         true,
		CompatibilityKnown: profile.MemoryBytes > 0 && profile.AvailableDiskBytes > 0,
		Installable:        variant.Status == "available",
	}
	if profile.MemoryBytes > 0 && profile.MemoryBytes < requirements.RecommendedRAM {
		assessment.Compatible = false
		assessment.Reasons = append(assessment.Reasons, fmt.Sprintf(
			"recommended RAM is %s; machine has %s",
			FormatBytes(requirements.RecommendedRAM), FormatBytes(profile.MemoryBytes),
		))
	}
	requiredDisk := requirements.StorageBytes + requirements.StorageBytes/10
	if profile.AvailableDiskBytes > 0 && profile.AvailableDiskBytes < requiredDisk {
		assessment.Compatible = false
		assessment.Reasons = append(assessment.Reasons, fmt.Sprintf(
			"installation needs about %s including staging; %s is available",
			FormatBytes(requiredDisk), FormatBytes(profile.AvailableDiskBytes),
		))
	}
	if !assessment.Installable {
		assessment.Reasons = append(assessment.Reasons, "artifact qualification is not complete")
	}
	return assessment, nil
}

func EstimateRequirements(variant catalog.Variant) (Requirements, error) {
	if variant.ExpectedStorage > 0 && variant.RecommendedRAM > 0 {
		return Requirements{
			StorageBytes:   variant.ExpectedStorage,
			RecommendedRAM: variant.RecommendedRAM,
			Source:         "qualified artifact profile",
		}, nil
	}
	parameters, err := leadingNumber(variant.ParameterCount)
	if err != nil {
		return Requirements{}, fmt.Errorf("estimate %s requirements: %w", variant.ID, err)
	}
	// Q4_K_M files generally require roughly 0.6 GiB per billion parameters.
	// Add runtime and KV-cache headroom rather than presenting weight size alone.
	storage := uint64(math.Ceil(parameters * 0.625 * float64(gibibyte)))
	ram := uint64(math.Ceil(float64(storage)*1.25)) + 2*gibibyte
	if ram < 4*gibibyte {
		ram = 4 * gibibyte
	}
	return Requirements{
		StorageBytes:   storage,
		RecommendedRAM: ram,
		Source:         "estimated for the planned Q4_K_M artifact",
	}, nil

}

func leadingNumber(value string) (float64, error) {
	end := 0
	for end < len(value) {
		character := value[end]
		if (character < '0' || character > '9') && character != '.' {
			break
		}
		end++
	}
	if end == 0 {
		return 0, fmt.Errorf("parameter count %q does not begin with a number", value)
	}
	number, err := strconv.ParseFloat(value[:end], 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("invalid parameter count %q", value)
	}
	return number, nil
}

func FormatBytes(bytes uint64) string {
	if bytes < gibibyte {
		return fmt.Sprintf("%.1f MiB", float64(bytes)/(1024*1024))
	}
	value := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", float64(bytes)/float64(gibibyte)), "0"), ".")
	return value + " GiB"
}
