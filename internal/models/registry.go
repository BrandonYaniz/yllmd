package models

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
)

type Registry struct {
	cfg          config.Config
	byName       map[string]LocalModel
	byIdentifier map[string]LocalModel
	orderedNames []string
}

type LocalModel struct {
	Name       string
	Config     config.ModelConfig
	Descriptor protocol.ModelDescriptor
	ModelPath  string
}

type ResolvedTarget struct {
	RequestedGroup     string
	RequestedProfile   string
	RequestedModel     string
	ResolvedGroup      string
	ResolvedProfile    string
	ResolvedModel      string
	UsedDefaultGroup   bool
	UsedDefaultProfile bool
	UsedFallback       bool
	FallbackFrom       string
	FallbackModels     []string
}

func NewRegistry(cfg config.Config) Registry {
	registry := Registry{
		cfg: cfg, byName: make(map[string]LocalModel, len(cfg.Models)),
		byIdentifier: make(map[string]LocalModel, len(cfg.Models)),
	}
	for name, model := range cfg.Models {
		local := LocalModel{Name: name, Config: model, ModelPath: ResolveModelPath(cfg, name, model), Descriptor: descriptorFor(cfg, name, model)}
		registry.byName[name], registry.byIdentifier[name] = local, local
		for _, alias := range model.Aliases {
			registry.byIdentifier[alias] = local
		}
		registry.orderedNames = append(registry.orderedNames, name)
	}
	sort.Strings(registry.orderedNames)
	return registry
}

func (r Registry) Resolve(identifier string) (LocalModel, error) {
	model, ok := r.byIdentifier[identifier]
	if !ok {
		return LocalModel{}, fmt.Errorf("local model %q is not configured", identifier)
	}
	return model, nil
}

func (r Registry) ResolveTarget(target protocol.ModelTarget) (ResolvedTarget, error) {
	return r.ResolveTargetWithAvailability(target, func(model LocalModel) error {
		if !model.Config.IsEnabled() {
			return fmt.Errorf("model %q is administratively disabled", model.Name)
		}
		return nil
	})
}

func (r Registry) ResolveTargetWithAvailability(target protocol.ModelTarget, available func(LocalModel) error) (ResolvedTarget, error) {
	if target.Model != "" && (target.Group != "" || target.Profile != "") {
		return ResolvedTarget{}, fmt.Errorf("exact model cannot be combined with a route")
	}
	if target.Profile != "" && target.Group == "" {
		return ResolvedTarget{}, fmt.Errorf("profile requires a group")
	}
	result := ResolvedTarget{RequestedGroup: target.Group, RequestedProfile: target.Profile, RequestedModel: target.Model}
	if target.Model != "" {
		model, err := r.Resolve(target.Model)
		if err != nil {
			return ResolvedTarget{}, err
		}
		if err := available(model); err != nil {
			return ResolvedTarget{}, err
		}
		result.ResolvedModel = model.Name
		return result, nil
	}

	groupName, profileName := target.Group, target.Profile
	if groupName == "" {
		groupName = r.cfg.Routing.Default.Group
		profileName = r.cfg.Routing.Default.Profile
		result.UsedDefaultGroup = true
		result.UsedDefaultProfile = true
	}
	group, ok := r.cfg.Routing.Groups[groupName]
	if !ok {
		return ResolvedTarget{}, fmt.Errorf("routing group %q is not configured", groupName)
	}
	if profileName == "" {
		profileName = group.DefaultProfile
		result.UsedDefaultProfile = true
		if profileName == "" {
			return ResolvedTarget{}, fmt.Errorf("routing group %q has no default profile", groupName)
		}
	}
	profile, ok := group.Profiles[profileName]
	if !ok {
		return ResolvedTarget{}, fmt.Errorf("routing profile %s/%s is not configured", groupName, profileName)
	}
	result.ResolvedGroup, result.ResolvedProfile = groupName, profileName

	candidates := append([]string{profile.Model}, profile.Fallbacks...)
	var unavailable error
	for index, identifier := range candidates {
		model, err := r.Resolve(identifier)
		if err != nil {
			return ResolvedTarget{}, err
		}
		if err := available(model); err != nil {
			unavailable = err
			if index == 0 && r.unavailableModelPolicy() != "use_fallback" {
				break
			}
			continue
		}
		result.ResolvedModel = model.Name
		if r.unavailableModelPolicy() == "use_fallback" {
			for _, fallbackIdentifier := range candidates[index+1:] {
				fallback, err := r.Resolve(fallbackIdentifier)
				if err == nil && available(fallback) == nil {
					result.FallbackModels = append(result.FallbackModels, fallback.Name)
				}
			}
		}
		if index > 0 {
			result.UsedFallback, result.FallbackFrom = true, candidates[0]
			if primary, err := r.Resolve(candidates[0]); err == nil {
				result.FallbackFrom = primary.Name
			}
		}
		return result, nil
	}
	if unavailable != nil {
		return ResolvedTarget{}, unavailable
	}
	return ResolvedTarget{}, fmt.Errorf("routing profile %s/%s has no available model", groupName, profileName)
}

func (r Registry) unavailableModelPolicy() string {
	if r.cfg.Routing.UnavailableModelPolicy == "" {
		return "use_fallback"
	}
	return r.cfg.Routing.UnavailableModelPolicy
}

func (r Registry) Resident() (LocalModel, error) {
	if r.cfg.ModelLifecycle.ResidentModel == "" {
		resolved, err := r.ResolveTarget(protocol.ModelTarget{})
		if err != nil {
			return LocalModel{}, err
		}
		return r.Resolve(resolved.ResolvedModel)
	}
	return r.Resolve(r.cfg.ModelLifecycle.ResidentModel)
}

func (r Registry) Descriptors() []protocol.ModelDescriptor {
	descriptors := make([]protocol.ModelDescriptor, 0, len(r.orderedNames))
	for _, name := range r.orderedNames {
		descriptors = append(descriptors, r.byName[name].Descriptor)
	}
	return descriptors
}

func (r Registry) Groups() []protocol.RoutingGroup {
	names := make([]string, 0, len(r.cfg.Routing.Groups))
	for name := range r.cfg.Routing.Groups {
		names = append(names, name)
	}
	sort.Strings(names)
	groups := make([]protocol.RoutingGroup, 0, len(names))
	for _, name := range names {
		group := r.cfg.Routing.Groups[name]
		profileNames := make([]string, 0, len(group.Profiles))
		for profile := range group.Profiles {
			profileNames = append(profileNames, profile)
		}
		sort.Strings(profileNames)
		descriptor := protocol.RoutingGroup{Name: name, DefaultProfile: group.DefaultProfile, Profiles: make([]protocol.RoutingProfile, 0, len(profileNames))}
		for _, profileName := range profileNames {
			profile := group.Profiles[profileName]
			fallbacks := append([]string(nil), profile.Fallbacks...)
			if fallbacks == nil {
				fallbacks = []string{}
			}
			descriptor.Profiles = append(descriptor.Profiles, protocol.RoutingProfile{Name: profileName, Model: profile.Model, Fallbacks: fallbacks})
		}
		groups = append(groups, descriptor)
	}
	return groups
}

func ResolveModelPath(cfg config.Config, name string, model config.ModelConfig) string {
	if model.ModelPath != "" {
		return model.ModelPath
	}
	return filepath.Join(cfg.Paths.ModelDir, name, "current", "model.gguf")
}

func descriptorFor(cfg config.Config, name string, model config.ModelConfig) protocol.ModelDescriptor {
	return protocol.ModelDescriptor{
		ID: protocol.ModelID{Provider: "local", Name: name}, Name: name, DisplayName: name,
		CatalogID: model.CatalogID, Aliases: append([]string(nil), model.Aliases...), Enabled: model.IsEnabled(),
		Resident:         cfg.ModelLifecycle.ResidentModel == name,
		Runtime:          protocol.ModelRuntime{ContextTokens: model.Runtime.ContextTokens, Threads: model.Runtime.Threads, GPULayers: model.Runtime.GPULayers},
		Capabilities:     protocol.ModelCapabilities{SupportsStreaming: true, SupportsLocalPreparation: true, ContextWindow: model.Runtime.ContextTokens},
		ProviderMetadata: map[string]string{"catalog_id": model.CatalogID, "model_path": ResolveModelPath(cfg, name, model)},
	}
}
