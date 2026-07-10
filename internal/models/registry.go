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
	byTier       map[string]LocalModel
	byTypeLevel  map[string]map[string]LocalModel
	orderedNames []string
}

type LocalModel struct {
	Name       string
	Config     config.LocalModelConfig
	Descriptor protocol.ModelDescriptor
	ModelPath  string
}

func NewRegistry(cfg config.Config) Registry {
	registry := Registry{
		cfg:         cfg,
		byName:      make(map[string]LocalModel, len(cfg.LocalModels)),
		byTier:      make(map[string]LocalModel, len(cfg.LocalModels)),
		byTypeLevel: make(map[string]map[string]LocalModel, len(cfg.LocalModels)),
	}
	for name, model := range cfg.LocalModels {
		local := LocalModel{
			Name:       name,
			Config:     model,
			ModelPath:  ResolveModelPath(cfg, name, model),
			Descriptor: descriptorFor(cfg, name, model),
		}
		registry.byName[name] = local
		if _, exists := registry.byTier[model.Tier]; !exists {
			registry.byTier[model.Tier] = local
		}
		modelType := modelTypeFor(model)
		if registry.byTypeLevel[modelType] == nil {
			registry.byTypeLevel[modelType] = make(map[string]LocalModel)
		}
		if _, exists := registry.byTypeLevel[modelType][model.Tier]; !exists {
			registry.byTypeLevel[modelType][model.Tier] = local
		}
		registry.orderedNames = append(registry.orderedNames, name)
	}
	sort.Strings(registry.orderedNames)
	return registry
}

func (r Registry) Resolve(nameOrTier string) (LocalModel, error) {
	if nameOrTier == "" {
		nameOrTier = r.cfg.ModelLifecycle.ResidentModel
	}
	if model, ok := r.byName[nameOrTier]; ok {
		return model, nil
	}
	if model, ok := r.byTier[nameOrTier]; ok {
		return model, nil
	}
	return LocalModel{}, fmt.Errorf("local model %q is not configured", nameOrTier)
}

func (r Registry) ResolveRequest(nameOrTier, modelType, level string) (LocalModel, error) {
	if level != "" {
		if modelType == "" {
			modelType = "llm"
		}
		if modelsByLevel, ok := r.byTypeLevel[modelType]; ok {
			if model, ok := modelsByLevel[level]; ok {
				return model, nil
			}
		}
		return LocalModel{}, fmt.Errorf("local %s model level %q is not configured", modelType, level)
	}
	return r.Resolve(nameOrTier)
}

func (r Registry) Resident() (LocalModel, error) {
	return r.Resolve(r.cfg.ModelLifecycle.ResidentModel)
}

func (r Registry) Descriptors() []protocol.ModelDescriptor {
	descriptors := make([]protocol.ModelDescriptor, 0, len(r.orderedNames))
	for _, name := range r.orderedNames {
		descriptors = append(descriptors, r.byName[name].Descriptor)
	}
	return descriptors
}

func ResolveModelPath(cfg config.Config, name string, model config.LocalModelConfig) string {
	if model.ModelPath != "" {
		return model.ModelPath
	}
	return filepath.Join(cfg.Paths.ModelDir, name, "current", "model.gguf")
}

func descriptorFor(cfg config.Config, name string, model config.LocalModelConfig) protocol.ModelDescriptor {
	modelType := modelTypeFor(model)
	return protocol.ModelDescriptor{
		ID:          protocol.ModelID{Provider: "local", Name: name},
		Name:        name,
		DisplayName: name,
		ModelType:   modelType,
		Level:       model.Tier,
		Tier:        model.Tier,
		Resident:    model.Resident || cfg.ModelLifecycle.ResidentModel == name,
		Capabilities: protocol.ModelCapabilities{
			SupportsStreaming:        true,
			SupportsLocalPreparation: true,
			ContextWindow:            model.Runtime.ContextTokens,
		},
		ProviderMetadata: map[string]string{
			"catalog_id": model.CatalogID,
			"model_path": ResolveModelPath(cfg, name, model),
		},
	}
}

func modelTypeFor(model config.LocalModelConfig) string {
	if model.ModelType == "" {
		return "llm"
	}
	return model.ModelType
}
