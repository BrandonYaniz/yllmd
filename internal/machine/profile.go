package machine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Profile struct {
	OS                 string   `json:"os"`
	Architecture       string   `json:"architecture"`
	LogicalCPUs        int      `json:"logical_cpus"`
	MemoryBytes        uint64   `json:"memory_bytes"`
	AvailableDiskBytes uint64   `json:"available_disk_bytes"`
	DiskPath           string   `json:"disk_path"`
	Warnings           []string `json:"warnings,omitempty"`
}

func Detect(modelDir string) (Profile, error) {
	profile := Profile{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		LogicalCPUs:  runtime.NumCPU(),
	}
	memory, err := physicalMemory()
	if err != nil {
		profile.Warnings = append(profile.Warnings, fmt.Sprintf("physical memory unavailable: %v", err))
	} else {
		profile.MemoryBytes = memory
	}
	diskPath, err := existingAncestor(modelDir)
	if err != nil {
		return Profile{}, fmt.Errorf("detect model disk: %w", err)
	}
	profile.DiskPath = diskPath
	available, err := availableDisk(diskPath)
	if err != nil {
		profile.Warnings = append(profile.Warnings, fmt.Sprintf("available disk unavailable at %s: %v", diskPath, err))
	} else {
		profile.AvailableDiskBytes = available
	}
	return profile, nil
}

func existingAncestor(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("model directory is required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		path = parent
	}
}
