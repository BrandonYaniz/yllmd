package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type Config struct {
	OperatingMode   string                          `yaml:"operating_mode,omitempty"`
	Server          ServerConfig                    `yaml:"server"`
	Queue           QueueConfig                     `yaml:"queue"`
	ModelLifecycle  ModelLifecycleConfig            `yaml:"model_lifecycle"`
	Paths           PathsConfig                     `yaml:"paths"`
	Models          map[string]ModelConfig          `yaml:"models"`
	Routing         RoutingConfig                   `yaml:"routing"`
	Updates         UpdatesConfig                   `yaml:"updates"`
	RemoteProviders map[string]RemoteProviderConfig `yaml:"remote_providers,omitempty"`
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
	ResidentModel   string        `yaml:"resident_model,omitempty"`
	IdleCooldown    time.Duration `yaml:"idle_cooldown"`
	MaxLoadedModels int           `yaml:"max_loaded_models"`
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

type ModelConfig struct {
	CatalogID string               `yaml:"catalog_id,omitempty"`
	ModelPath string               `yaml:"model_path,omitempty"`
	Aliases   []string             `yaml:"aliases,omitempty"`
	Enabled   *bool                `yaml:"enabled,omitempty"`
	Backend   LocalBackendConfig   `yaml:"backend"`
	Runtime   LocalRuntimeSettings `yaml:"runtime"`
}

func (m ModelConfig) IsEnabled() bool {
	return m.Enabled == nil || *m.Enabled
}

type LocalBackendConfig struct {
	Type      string `yaml:"type"`
	Command   string `yaml:"command"`
	Transport string `yaml:"transport"`
}

type LocalRuntimeSettings struct {
	ContextTokens int `yaml:"context_tokens"`
	Threads       int `yaml:"threads"`
	GPULayers     int `yaml:"gpu_layers"`
}

type RemoteProviderConfig struct {
	Enabled   bool   `yaml:"enabled"`
	APIKeyEnv string `yaml:"api_key_env"`
	KeyFile   string `yaml:"key_file"`
}

type RoutingConfig struct {
	Default                  RouteReference         `yaml:"default"`
	UnavailableProfilePolicy string                 `yaml:"unavailable_profile_policy,omitempty"`
	UnavailableModelPolicy   string                 `yaml:"unavailable_model_policy,omitempty"`
	Groups                   map[string]GroupConfig `yaml:"groups"`
	// Kept as an optional provider setting while remote generation remains reserved.
	DefaultProvider string `yaml:"default_provider,omitempty"`
}

type RouteReference struct {
	Group   string `yaml:"group"`
	Profile string `yaml:"profile,omitempty"`
}

type GroupConfig struct {
	DefaultProfile string                   `yaml:"default_profile,omitempty"`
	Profiles       map[string]ProfileConfig `yaml:"profiles"`
}

type ProfileConfig struct {
	Model     string   `yaml:"model"`
	Fallbacks []string `yaml:"fallbacks,omitempty"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

func Parse(data []byte) (Config, error) {
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
	if c.OperatingMode != "" && c.OperatingMode != "user" && c.OperatingMode != "daemon" {
		errs = append(errs, fmt.Errorf("operating_mode %q is not supported", c.OperatingMode))
	}
	if c.Server.SocketPath == "" {
		errs = append(errs, errors.New("server.socket_path is required"))
	}
	if c.Queue.Policy != "fifo" {
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
	if c.ModelLifecycle.MaxLoadedModels != 1 {
		errs = append(errs, errors.New("model_lifecycle.max_loaded_models must be 1"))
	}
	if c.Updates.DefaultPolicy == "" || !supportedUpdatePolicy(c.Updates.DefaultPolicy) {
		errs = append(errs, fmt.Errorf("updates.default_policy %q is not supported", c.Updates.DefaultPolicy))
	}
	if len(c.Models) == 0 {
		errs = append(errs, errors.New("at least one model is required"))
	}

	claimed := make(map[string]string)
	for _, name := range sortedKeys(c.Models) {
		model := c.Models[name]
		validateIdentifier(&errs, "models."+name, name)
		claimIdentifier(&errs, claimed, name, "model "+name)
		for index, alias := range model.Aliases {
			validateIdentifier(&errs, fmt.Sprintf("models.%s.aliases[%d]", name, index), alias)
			claimIdentifier(&errs, claimed, alias, "alias for "+name)
		}
		validateModel(&errs, name, model)
	}
	if c.ModelLifecycle.ResidentModel != "" {
		if _, ok := resolveModelReference(c.Models, c.ModelLifecycle.ResidentModel); !ok {
			errs = append(errs, fmt.Errorf("resident model %q is not configured", c.ModelLifecycle.ResidentModel))
		}
	}

	if c.Routing.UnavailableProfilePolicy == "" {
		c.Routing.UnavailableProfilePolicy = "reject"
	}
	if c.Routing.UnavailableProfilePolicy != "reject" {
		errs = append(errs, fmt.Errorf("routing.unavailable_profile_policy %q is not supported", c.Routing.UnavailableProfilePolicy))
	}
	if c.Routing.UnavailableModelPolicy == "" {
		c.Routing.UnavailableModelPolicy = "use_fallback"
	}
	if c.Routing.UnavailableModelPolicy != "reject" && c.Routing.UnavailableModelPolicy != "use_fallback" {
		errs = append(errs, fmt.Errorf("routing.unavailable_model_policy %q is not supported", c.Routing.UnavailableModelPolicy))
	}
	if c.Routing.DefaultProvider != "" && c.Routing.DefaultProvider != "local" {
		errs = append(errs, errors.New("routing.default_provider must be local"))
	}
	for _, groupName := range sortedKeys(c.Routing.Groups) {
		group := c.Routing.Groups[groupName]
		validateIdentifier(&errs, "routing.groups."+groupName, groupName)
		if len(group.Profiles) == 0 {
			errs = append(errs, fmt.Errorf("routing group %q must contain at least one profile", groupName))
		}
		if group.DefaultProfile != "" {
			if _, ok := group.Profiles[group.DefaultProfile]; !ok {
				errs = append(errs, fmt.Errorf("routing group %q default profile %q is not configured", groupName, group.DefaultProfile))
			}
		}
		for _, profileName := range sortedKeys(group.Profiles) {
			profile := group.Profiles[profileName]
			validateIdentifier(&errs, fmt.Sprintf("routing.groups.%s.profiles.%s", groupName, profileName), profileName)
			primary, ok := resolveModelReference(c.Models, profile.Model)
			if !ok {
				errs = append(errs, fmt.Errorf("routing profile %s/%s references unknown model %q", groupName, profileName, profile.Model))
			}
			seenFallbacks := make(map[string]struct{})
			for index, fallback := range profile.Fallbacks {
				resolved, exists := resolveModelReference(c.Models, fallback)
				if !exists {
					errs = append(errs, fmt.Errorf("routing profile %s/%s fallback %q is not configured", groupName, profileName, fallback))
					continue
				}
				if resolved == primary {
					errs = append(errs, fmt.Errorf("routing profile %s/%s repeats primary model %q as a fallback", groupName, profileName, fallback))
				}
				if _, duplicate := seenFallbacks[resolved]; duplicate {
					errs = append(errs, fmt.Errorf("routing profile %s/%s repeats fallback model %q", groupName, profileName, fallback))
				}
				seenFallbacks[resolved] = struct{}{}
				validateIdentifier(&errs, fmt.Sprintf("routing.groups.%s.profiles.%s.fallbacks[%d]", groupName, profileName, index), fallback)
			}
		}
	}
	defaultGroup, ok := c.Routing.Groups[c.Routing.Default.Group]
	if !ok {
		errs = append(errs, fmt.Errorf("routing default group %q is not configured", c.Routing.Default.Group))
	} else {
		profile := c.Routing.Default.Profile
		if profile == "" {
			profile = defaultGroup.DefaultProfile
		}
		if profile == "" {
			errs = append(errs, fmt.Errorf("routing default group %q has no default profile", c.Routing.Default.Group))
		} else if _, ok := defaultGroup.Profiles[profile]; !ok {
			errs = append(errs, fmt.Errorf("routing default profile %s/%s is not configured", c.Routing.Default.Group, profile))
		}
	}
	for name, provider := range c.RemoteProviders {
		if provider.Enabled && provider.APIKeyEnv == "" && provider.KeyFile == "" {
			errs = append(errs, fmt.Errorf("remote_providers.%s requires api_key_env or key_file when enabled", name))
		}
	}
	return errors.Join(errs...)
}

func validateModel(errs *[]error, name string, model ModelConfig) {
	prefix := "models." + name
	if model.CatalogID == "" && model.ModelPath == "" {
		*errs = append(*errs, fmt.Errorf("%s requires catalog_id or model_path", prefix))
	}
	if model.Backend.Type != "process" {
		*errs = append(*errs, fmt.Errorf("%s.backend.type must be process", prefix))
	}
	if model.Backend.Command == "" {
		*errs = append(*errs, fmt.Errorf("%s.backend.command is required", prefix))
	}
	if model.Backend.Transport != "stdio" {
		*errs = append(*errs, fmt.Errorf("%s.backend.transport must be stdio", prefix))
	}
	if model.Runtime.ContextTokens <= 0 {
		*errs = append(*errs, fmt.Errorf("%s.runtime.context_tokens must be positive", prefix))
	}
	if model.Runtime.Threads <= 0 {
		*errs = append(*errs, fmt.Errorf("%s.runtime.threads must be positive", prefix))
	}
	if model.Runtime.GPULayers < -1 {
		*errs = append(*errs, fmt.Errorf("%s.runtime.gpu_layers must be -1, 0, or positive", prefix))
	}
}

func validateIdentifier(errs *[]error, field, value string) {
	if !identifierPattern.MatchString(value) {
		*errs = append(*errs, fmt.Errorf("%s %q is not a valid identifier", field, value))
	}
}

func claimIdentifier(errs *[]error, claimed map[string]string, identifier, owner string) {
	if previous, exists := claimed[identifier]; exists {
		*errs = append(*errs, fmt.Errorf("identifier %q is used by both %s and %s", identifier, previous, owner))
		return
	}
	claimed[identifier] = owner
}

func ResolveModelReference(models map[string]ModelConfig, identifier string) (string, bool) {
	return resolveModelReference(models, identifier)
}

func resolveModelReference(models map[string]ModelConfig, identifier string) (string, bool) {
	if _, ok := models[identifier]; ok {
		return identifier, true
	}
	for name, model := range models {
		for _, alias := range model.Aliases {
			if alias == identifier {
				return name, true
			}
		}
	}
	return "", false
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	type rawConfig struct {
		Policy         string `yaml:"policy"`
		MaxDepth       int    `yaml:"max_depth"`
		DefaultTimeout string `yaml:"default_timeout"`
	}
	var raw rawConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	timeout, err := parseDuration("queue.default_timeout", raw.DefaultTimeout)
	if err != nil {
		return err
	}
	m.Policy, m.MaxDepth, m.DefaultTimeout = raw.Policy, raw.MaxDepth, timeout
	return nil
}

func (m QueueConfig) MarshalYAML() (any, error) {
	return struct {
		Policy         string `yaml:"policy"`
		MaxDepth       int    `yaml:"max_depth"`
		DefaultTimeout string `yaml:"default_timeout"`
	}{m.Policy, m.MaxDepth, m.DefaultTimeout.String()}, nil
}

func (m *ModelLifecycleConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawConfig struct {
		ResidentModel   string `yaml:"resident_model"`
		IdleCooldown    string `yaml:"idle_cooldown"`
		MaxLoadedModels int    `yaml:"max_loaded_models"`
	}
	var raw rawConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	cooldown, err := parseDuration("model_lifecycle.idle_cooldown", raw.IdleCooldown)
	if err != nil {
		return err
	}
	m.ResidentModel, m.IdleCooldown, m.MaxLoadedModels = raw.ResidentModel, cooldown, raw.MaxLoadedModels
	return nil
}

func (m ModelLifecycleConfig) MarshalYAML() (any, error) {
	return struct {
		ResidentModel   string `yaml:"resident_model,omitempty"`
		IdleCooldown    string `yaml:"idle_cooldown"`
		MaxLoadedModels int    `yaml:"max_loaded_models"`
	}{m.ResidentModel, m.IdleCooldown.String(), m.MaxLoadedModels}, nil
}

func (u *UpdatesConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawConfig struct {
		CheckInterval string `yaml:"check_interval"`
		DefaultPolicy string `yaml:"default_policy"`
	}
	var raw rawConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	interval, err := parseDuration("updates.check_interval", raw.CheckInterval)
	if err != nil {
		return err
	}
	u.CheckInterval, u.DefaultPolicy = interval, raw.DefaultPolicy
	return nil
}

func (u UpdatesConfig) MarshalYAML() (any, error) {
	return struct {
		CheckInterval string `yaml:"check_interval"`
		DefaultPolicy string `yaml:"default_policy"`
	}{u.CheckInterval.String(), u.DefaultPolicy}, nil
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
