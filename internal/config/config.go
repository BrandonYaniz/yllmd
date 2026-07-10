package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server          ServerConfig                    `yaml:"server"`
	Queue           QueueConfig                     `yaml:"queue"`
	ModelLifecycle  ModelLifecycleConfig            `yaml:"model_lifecycle"`
	Paths           PathsConfig                     `yaml:"paths"`
	Updates         UpdatesConfig                   `yaml:"updates"`
	LocalModels     map[string]LocalModelConfig     `yaml:"local_models"`
	RemoteProviders map[string]RemoteProviderConfig `yaml:"remote_providers"`
	Routing         RoutingConfig                   `yaml:"routing"`
}

type ServerConfig struct {
	SocketPath  string `yaml:"socket_path"`
	SocketMode  string `yaml:"socket_mode"`
	SocketGroup string `yaml:"socket_group"`
}

type QueueConfig struct {
	Policy         string        `yaml:"policy"`
	MaxDepth       int           `yaml:"max_depth"`
	DefaultTimeout time.Duration `yaml:"default_timeout"`
}

type ModelLifecycleConfig struct {
	ResidentModel         string        `yaml:"resident_model"`
	IdleCooldown          time.Duration `yaml:"idle_cooldown"`
	MaxLoadedModels       int           `yaml:"max_loaded_models"`
	UseCurrentOrBetter    bool          `yaml:"use_current_or_better"`
	UnavailableTierPolicy string        `yaml:"unavailable_tier_policy"`
}

type PathsConfig struct {
	StateDir   string `yaml:"state_dir"`
	ModelDir   string `yaml:"model_dir"`
	RuntimeDir string `yaml:"runtime_dir"`
	LogDir     string `yaml:"log_dir"`
}

type UpdatesConfig struct {
	CheckInterval time.Duration `yaml:"check_interval"`
	DefaultPolicy string        `yaml:"default_policy"`
}

type LocalModelConfig struct {
	CatalogID string               `yaml:"catalog_id"`
	ModelType string               `yaml:"model_type"`
	ModelPath string               `yaml:"model_path"`
	Tier      string               `yaml:"tier"`
	Resident  bool                 `yaml:"resident"`
	Backend   LocalBackendConfig   `yaml:"backend"`
	Runtime   LocalRuntimeSettings `yaml:"runtime"`
}

type LocalBackendConfig struct {
	Type      string `yaml:"type"`
	Command   string `yaml:"command"`
	Transport string `yaml:"transport"`
}

type LocalRuntimeSettings struct {
	ContextTokens int `yaml:"context_tokens"`
	Threads       int `yaml:"threads"`
}

type RemoteProviderConfig struct {
	Enabled   bool   `yaml:"enabled"`
	APIKeyEnv string `yaml:"api_key_env"`
	KeyFile   string `yaml:"key_file"`
}

type RoutingConfig struct {
	DefaultProvider               string `yaml:"default_provider"`
	AllowAutoRemote               bool   `yaml:"allow_auto_remote"`
	RequireExplicitRemoteProvider bool   `yaml:"require_explicit_remote_provider"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var errs []error
	if c.Server.SocketPath == "" {
		errs = append(errs, errors.New("server.socket_path is required"))
	}
	if c.Queue.Policy == "" {
		errs = append(errs, errors.New("queue.policy is required"))
	} else if c.Queue.Policy != "fifo" {
		errs = append(errs, fmt.Errorf("queue.policy %q is not supported", c.Queue.Policy))
	}
	if c.Queue.MaxDepth <= 0 {
		errs = append(errs, errors.New("queue.max_depth must be positive"))
	}
	if c.Queue.DefaultTimeout <= 0 {
		errs = append(errs, errors.New("queue.default_timeout must be positive"))
	}
	if c.ModelLifecycle.IdleCooldown <= 0 {
		errs = append(errs, errors.New("model_lifecycle.idle_cooldown must be positive"))
	}
	if c.ModelLifecycle.MaxLoadedModels <= 0 {
		errs = append(errs, errors.New("model_lifecycle.max_loaded_models must be positive"))
	}
	if c.Updates.DefaultPolicy == "" {
		errs = append(errs, errors.New("updates.default_policy is required"))
	} else if !supportedUpdatePolicy(c.Updates.DefaultPolicy) {
		errs = append(errs, fmt.Errorf("updates.default_policy %q is not supported", c.Updates.DefaultPolicy))
	}
	if len(c.LocalModels) == 0 {
		errs = append(errs, errors.New("at least one local model is required for v1"))
	}
	if c.ModelLifecycle.ResidentModel != "" {
		if _, ok := c.LocalModels[c.ModelLifecycle.ResidentModel]; !ok {
			errs = append(errs, fmt.Errorf("resident model %q is not configured", c.ModelLifecycle.ResidentModel))
		}
	}
	for name, model := range c.LocalModels {
		if model.ModelType != "" && !supportedModelType(model.ModelType) {
			errs = append(errs, fmt.Errorf("local_models.%s.model_type %q is not supported", name, model.ModelType))
		}
		if model.Tier == "" {
			errs = append(errs, fmt.Errorf("local_models.%s.tier is required", name))
		}
		if model.Backend.Type != "process" {
			errs = append(errs, fmt.Errorf("local_models.%s.backend.type must be process", name))
		}
		if model.Backend.Command == "" {
			errs = append(errs, fmt.Errorf("local_models.%s.backend.command is required", name))
		}
		if model.Backend.Transport != "stdio" {
			errs = append(errs, fmt.Errorf("local_models.%s.backend.transport must be stdio", name))
		}
		if model.Runtime.ContextTokens <= 0 {
			errs = append(errs, fmt.Errorf("local_models.%s.runtime.context_tokens must be positive", name))
		}
		if model.Runtime.Threads <= 0 {
			errs = append(errs, fmt.Errorf("local_models.%s.runtime.threads must be positive", name))
		}
	}
	for name, provider := range c.RemoteProviders {
		if provider.Enabled && provider.APIKeyEnv == "" && provider.KeyFile == "" {
			errs = append(errs, fmt.Errorf("remote_providers.%s requires api_key_env or key_file when enabled", name))
		}
	}
	if c.Routing.DefaultProvider == "" {
		errs = append(errs, errors.New("routing.default_provider is required"))
	}
	if c.Routing.DefaultProvider != "local" {
		errs = append(errs, errors.New("v1 requires routing.default_provider to be local"))
	}
	return errors.Join(errs...)
}

func supportedModelType(modelType string) bool {
	switch modelType {
	case "llm", "code":
		return true
	default:
		return false
	}
}

func supportedUpdatePolicy(policy string) bool {
	switch policy {
	case "manual", "notify", "download", "auto":
		return true
	default:
		return false
	}
}

func (m *QueueConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawQueueConfig struct {
		Policy         string `yaml:"policy"`
		MaxDepth       int    `yaml:"max_depth"`
		DefaultTimeout string `yaml:"default_timeout"`
	}
	var raw rawQueueConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	timeout, err := parseDuration("queue.default_timeout", raw.DefaultTimeout)
	if err != nil {
		return err
	}
	m.Policy = raw.Policy
	m.MaxDepth = raw.MaxDepth
	m.DefaultTimeout = timeout
	return nil
}

func (m *ModelLifecycleConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawModelLifecycleConfig struct {
		ResidentModel         string `yaml:"resident_model"`
		IdleCooldown          string `yaml:"idle_cooldown"`
		MaxLoadedModels       int    `yaml:"max_loaded_models"`
		UseCurrentOrBetter    bool   `yaml:"use_current_or_better"`
		UnavailableTierPolicy string `yaml:"unavailable_tier_policy"`
	}
	var raw rawModelLifecycleConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	cooldown, err := parseDuration("model_lifecycle.idle_cooldown", raw.IdleCooldown)
	if err != nil {
		return err
	}
	m.ResidentModel = raw.ResidentModel
	m.IdleCooldown = cooldown
	m.MaxLoadedModels = raw.MaxLoadedModels
	m.UseCurrentOrBetter = raw.UseCurrentOrBetter
	m.UnavailableTierPolicy = raw.UnavailableTierPolicy
	return nil
}

func (u *UpdatesConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawUpdatesConfig struct {
		CheckInterval string `yaml:"check_interval"`
		DefaultPolicy string `yaml:"default_policy"`
	}
	var raw rawUpdatesConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	interval, err := parseDuration("updates.check_interval", raw.CheckInterval)
	if err != nil {
		return err
	}
	u.CheckInterval = interval
	u.DefaultPolicy = raw.DefaultPolicy
	return nil
}

func parseDuration(field, value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("%s is required", field)
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", field, err)
	}
	return duration, nil
}
