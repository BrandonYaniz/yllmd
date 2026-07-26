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
	OperatingMode  string                        `yaml:"operating_mode"`
	Server         config.ServerConfig           `yaml:"server"`
	Queue          generatedQueue                `yaml:"queue"`
	ModelLifecycle generatedLifecycle            `yaml:"model_lifecycle"`
	Paths          config.PathsConfig            `yaml:"paths"`
	Updates        generatedUpdates              `yaml:"updates"`
	Models         map[string]config.ModelConfig `yaml:"models"`
	Routing        config.RoutingConfig          `yaml:"routing"`
}

type generatedQueue struct {
	Policy         string `yaml:"policy"`
	MaxDepth       int    `yaml:"max_depth"`
	DefaultTimeout string `yaml:"default_timeout"`
}

type generatedLifecycle struct {
	ResidentModel   string `yaml:"resident_model"`
	IdleCooldown    string `yaml:"idle_cooldown"`
	MaxLoadedModels int    `yaml:"max_loaded_models"`
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
	configured := make(map[string]config.ModelConfig, len(variants))
	groups := make(map[string]config.GroupConfig)
	routes := make(map[string]string, len(variants))
	residentFound := false
	for _, variant := range variants {
		route := variant.ModelType + "/" + variant.Level
		if existing, exists := routes[route]; exists {
			return nil, fmt.Errorf("variants %q and %q both map to generated route %s", existing, variant.ID, route)
		}
		routes[route] = variant.ID
		resident := variant.ID == options.ResidentID
		residentFound = residentFound || resident
		configured[variant.ID] = config.ModelConfig{
			CatalogID: variant.ID,
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
		group := groups[variant.ModelType]
		if group.Profiles == nil {
			group.Profiles = make(map[string]config.ProfileConfig)
		}
		group.Profiles[variant.Level] = config.ProfileConfig{Model: variant.ID}
		if group.DefaultProfile == "" || resident {
			group.DefaultProfile = variant.Level
		}
		groups[variant.ModelType] = group
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
			ResidentModel: options.ResidentID, IdleCooldown: "15m", MaxLoadedModels: 1,
		},
		Paths: config.PathsConfig{
			StateDir:   options.Paths.StateDir,
			ModelDir:   options.Paths.ModelDir,
			RuntimeDir: options.Paths.RuntimeDir,
			LogDir:     options.Paths.LogDir,
		},
		Updates: generatedUpdates{CheckInterval: "24h", DefaultPolicy: "notify"},
		Models:  configured,
		Routing: config.RoutingConfig{
			Default:                  config.RouteReference{},
			UnavailableProfilePolicy: "reject",
			UnavailableModelPolicy:   "use_fallback",
			Groups:                   groups,
			DefaultProvider:          "local",
		},
	}
	for groupName, group := range groups {
		for profileName, profile := range group.Profiles {
			if profile.Model == options.ResidentID {
				generated.Routing.Default = config.RouteReference{Group: groupName, Profile: profileName}
			}
		}
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
