package llms

import (
	"context"
	"sort"
	"strings"
)

type ProviderModelWireAPIStore interface {
	LLMProviderModelWireAPI(ctx context.Context, providerID, modelID string) (string, bool, error)
}

func SelectModel(models []Model, requested string) Model {
	requested = strings.TrimSpace(requested)
	for _, model := range models {
		if requested != "" && (model.ID == requested || model.Name == requested) {
			return model
		}
	}
	if requested != "" {
		return Model{}
	}
	for _, model := range models {
		if model.DefaultModel {
			return model
		}
	}
	return models[0]
}

// ModelProviderSelection groups SelectModelAndProvider's selection inputs:
// the candidate models/providers and which model/family/provider was
// requested.
type ModelProviderSelection struct {
	Models         []Model
	Providers      []Provider
	RequestedModel string
	ProviderFamily string
	ProviderID     string
}

func SelectModelAndProvider(ctx context.Context, store ProviderModelWireAPIStore, sel ModelProviderSelection) (Model, Provider, string, bool, error) {
	models, providers, requestedModel, providerFamily, providerID := sel.Models, sel.Providers, sel.RequestedModel, sel.ProviderFamily, sel.ProviderID
	if strings.TrimSpace(requestedModel) != "" {
		requested := SelectModel(models, requestedModel)
		if strings.TrimSpace(requested.ID) == "" {
			return Model{}, Provider{}, "", false, nil
		}
		provider, wireAPI, ok, err := SelectProviderForModel(ctx, store, ProviderForModelSelection{Providers: providers, ModelID: requested.ID, ProviderFamily: providerFamily, ProviderID: providerID})
		return requested, provider, wireAPI, ok, err
	}
	ordered := append([]Model(nil), models...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].DefaultModel != ordered[j].DefaultModel {
			return ordered[i].DefaultModel
		}
		return ordered[i].ID < ordered[j].ID
	})
	for _, model := range ordered {
		provider, wireAPI, ok, err := SelectProviderForModel(ctx, store, ProviderForModelSelection{Providers: providers, ModelID: model.ID, ProviderFamily: providerFamily, ProviderID: providerID})
		if err != nil {
			return Model{}, Provider{}, "", false, err
		}
		if ok {
			return model, provider, wireAPI, true, nil
		}
	}
	return Model{}, Provider{}, "", false, nil
}

// ProviderForModelSelection groups SelectProviderForModel's selection inputs.
type ProviderForModelSelection struct {
	Providers      []Provider
	ModelID        string
	ProviderFamily string
	ProviderID     string
}

func SelectProviderForModel(ctx context.Context, store ProviderModelWireAPIStore, sel ProviderForModelSelection) (Provider, string, bool, error) {
	type candidate struct {
		provider Provider
		wireAPI  string
		priority int
	}
	providers, modelID, providerFamily, providerID := sel.Providers, sel.ModelID, sel.ProviderFamily, sel.ProviderID
	if strings.TrimSpace(providerFamily) != "" {
		providerFamily = NormalizeProviderType(providerFamily)
	}
	providerID = strings.TrimSpace(providerID)
	var candidates []candidate
	for _, provider := range providers {
		if providerID == "" && providerFamily != "" && NormalizeProviderType(provider.ProviderType) != providerFamily {
			continue
		}
		if providerID != "" && provider.ID != providerID {
			continue
		}
		if providerID == "" && strings.TrimSpace(provider.Scope) == ProviderScopeSessionEnv {
			continue
		}
		wireAPI, ok, err := store.LLMProviderModelWireAPI(ctx, provider.ID, modelID)
		if err != nil {
			return Provider{}, "", false, err
		}
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{provider: provider, wireAPI: firstNonEmpty(wireAPI, NormalizeWireAPI(provider.DefaultWireAPI)), priority: ProviderSelectionPriority(provider.Scope)})
	}
	if len(candidates) == 0 {
		return Provider{}, "", false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		if candidates[i].provider.Weight == candidates[j].provider.Weight {
			return candidates[i].provider.ID < candidates[j].provider.ID
		}
		return candidates[i].provider.Weight < candidates[j].provider.Weight
	})
	return candidates[0].provider, candidates[0].wireAPI, true, nil
}

func ProviderSelectionPriority(scope string) int {
	switch strings.TrimSpace(scope) {
	case ProviderScopeSessionEnv:
		return 0
	case ProviderScopeEnvDefault:
		return 1
	default:
		return 2
	}
}
