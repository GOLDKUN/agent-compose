package llms

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type ProviderModelConfigStore interface {
	LLMProviderModelConfig(ctx context.Context, providerID, modelID string) (ProviderModelConfig, bool, error)
}

// BuildResolvedTarget applies provider defaults followed by per-model catalog
// overrides when the selected model has metadata.
func BuildResolvedTarget(ctx context.Context, store ProviderModelWireAPIStore, provider Provider, model Model, wireAPI string) (ResolvedTarget, error) {
	wireAPI = firstNonEmptyTrimmed(wireAPI, provider.DefaultWireAPI)
	config := ProviderModelConfig{}
	if configStore, ok := store.(ProviderModelConfigStore); ok {
		resolved, found, err := configStore.LLMProviderModelConfig(ctx, provider.ID, model.ID)
		if err != nil {
			return ResolvedTarget{}, err
		}
		if found {
			config = resolved
			wireAPI = firstNonEmptyTrimmed(config.WireAPI, wireAPI)
		}
	}
	effectiveProvider := provider
	if strings.TrimSpace(config.BaseURL) != "" {
		effectiveProvider.BaseURL = strings.TrimSpace(config.BaseURL)
	}
	headers, err := ProviderForwardHeaders(effectiveProvider)
	if err != nil {
		return ResolvedTarget{}, err
	}
	if raw := strings.TrimSpace(config.HeadersJSON); raw != "" && raw != "{}" {
		values := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return ResolvedTarget{}, fmt.Errorf("decode provider %q model %q headers: %w", provider.ID, model.ID, err)
		}
		for key, value := range values {
			headers.Set(key, value)
		}
	}
	wireAPI = NormalizeWireAPI(wireAPI)
	return ResolvedTarget{
		Provider: effectiveProvider, Model: model, WireAPI: wireAPI,
		Endpoint: EndpointForProvider(effectiveProvider, wireAPI), Headers: headers,
		MaxOutputTokens: config.MaxOutputTokens,
	}, nil
}
