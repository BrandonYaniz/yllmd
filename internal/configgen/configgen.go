package configgen

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/BrandonYaniz/yllmd/internal/catalog"
	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/locations"
	"gopkg.in/yaml.v3"
)

type Options struct {
	Mode          locations.Mode
	Paths         locations.Paths
	Variants      []catalog.Variant
	ResidentID    string
	RunnerCommand string
	Threads       int
	GPULayers     int
}

type generatedConfig struct {
	OperatingMode  string                             `yaml:"operating_mode"`
	Server         config.ServerConfig                `yaml:"server"`
	Queue          generatedQueue                     `yaml:"queue"`
	ModelLifecycle generatedLifecycle                 `yaml:"model_lifecycle"`
	Paths          config.PathsConfig                 `yaml:"paths"`
	Updates        generatedUpdates                   `yaml:"updates"`
	LocalModels    map[string]config.LocalModelConfig `yaml:"local_models"`
	Routing        config.RoutingConfig               `yaml:"routing"`
}

type generatedQueue struct {
	Policy         string `yaml:"policy"`
	MaxDepth       int    `yaml:"max_depth"`
	DefaultTimeout string `yaml:"default_timeout"`
}

type generatedLifecycle struct {
	ResidentModel         string `yaml:"resident_model"`
	IdleCooldown          string `yaml:"idle_cooldown"`
	MaxLoadedModels       int    `yaml:"max_loaded_models"`
	UseCurrentOrBetter    bool   `yaml:"use_current_or_better"`
	UnavailableTierPolicy string `yaml:"unavailable_tier_policy"`
}

type generatedUpdates struct {
	CheckInterval string `yaml:"check_interval"`
	DefaultPolicy string `yaml:"default_policy"`
}

func Generate(options Options) ([]byte, error) {
	if options.Mode != locations.ModeUser && options.Mode != locations.ModeDaemon {
		return nil, fmt.Errorf("unsupported operating mode %q", options.Mode)
	}
	if len(options.Variants) == 0 {
		return nil, errors.New("at least one model variant is required")
	}
	if options.RunnerCommand == "" {
		options.RunnerCommand = "yllama-runner"
	}
	if options.Threads <= 0 {
		return nil, errors.New("threads must be positive")
	}
	if options.GPULayers < -1 {
		return nil, errors.New("gpu layers must be -1, 0, or positive")
	}

	variants := append([]catalog.Variant(nil), options.Variants...)
	sort.Slice(variants, func(i, j int) bool { return variants[i].ID < variants[j].ID })
	if options.ResidentID == "" {
		options.ResidentID = variants[0].ID
	}
	configured := make(map[string]config.LocalModelConfig, len(variants))
	roles := make(map[string]string, len(variants))
	residentFound := false
	for _, variant := range variants {
		role := variant.ModelType + "." + variant.Level
		if existing, exists := roles[role]; exists {
			return nil, fmt.Errorf("variants %q and %q both occupy %s", existing, variant.ID, role)
		}
		roles[role] = variant.ID
		resident := variant.ID == options.ResidentID
		residentFound = residentFound || resident
		configured[variant.ID] = config.LocalModelConfig{
			CatalogID: variant.ID,
			ModelType: variant.ModelType,
			Tier:      variant.Level,
			Resident:  resident,
			ModelPath: filepath.Join(options.Paths.ModelDir, variant.ID, "current", "model.gguf"),
			Backend: config.LocalBackendConfig{
				Type:      "process",
				Command:   options.RunnerCommand,
				Transport: "stdio",
			},
			Runtime: config.LocalRuntimeSettings{
				ContextTokens: contextTokens(variant.Level),
				Threads:       options.Threads,
				GPULayers:     options.GPULayers,
			},
		}
	}
	if !residentFound {
		return nil, fmt.Errorf("resident variant %q was not selected", options.ResidentID)
	}

	socketMode := "0600"
	socketGroup := ""
	if options.Mode == locations.ModeDaemon {
		socketMode = "0660"
		socketGroup = "yllm"
	}
	generated := generatedConfig{
		OperatingMode: string(options.Mode),
		Server: config.ServerConfig{
			SocketPath:  options.Paths.SocketPath,
			SocketMode:  socketMode,
			SocketGroup: socketGroup,
		},
		Queue: generatedQueue{Policy: "fifo", MaxDepth: 128, DefaultTimeout: "2m"},
		ModelLifecycle: generatedLifecycle{
			ResidentModel:         options.ResidentID,
			IdleCooldown:          "15m",
			MaxLoadedModels:       1,
			UseCurrentOrBetter:    true,
			UnavailableTierPolicy: "use_available",
		},
		Paths: config.PathsConfig{
			StateDir:   options.Paths.StateDir,
			ModelDir:   options.Paths.ModelDir,
			RuntimeDir: options.Paths.RuntimeDir,
			LogDir:     options.Paths.LogDir,
		},
		Updates:     generatedUpdates{CheckInterval: "24h", DefaultPolicy: "notify"},
		LocalModels: configured,
		Routing: config.RoutingConfig{
			DefaultProvider:               "local",
			AllowAutoRemote:               false,
			RequireExplicitRemoteProvider: true,
		},
	}
	data, err := yaml.Marshal(generated)
	if err != nil {
		return nil, fmt.Errorf("encode generated config: %w", err)
	}
	return data, nil
}

func contextTokens(level string) int {
	switch level {
	case "deep":
		return 32768
	case "balanced":
		return 16384
	default:
		return 8192
	}
}
