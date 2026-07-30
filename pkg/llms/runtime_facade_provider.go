package llms

import (
	"context"
	"sort"
	"strings"
)

// SelectRuntimeFacadeProvider chooses the provider that a startup-compatible
// facade token should bind to. A provider created from this sandbox's env wins
// over global providers; other session-env providers are not eligible.
func SelectRuntimeFacadeProvider(ctx context.Context, store ProviderListStore, sandboxID, providerFamily string) (Provider, bool, error) {
	if store == nil {
		return Provider{}, false, nil
	}
	providers, err := store.ListEnabledLLMProviders(ctx)
	if err != nil {
		return Provider{}, false, err
	}
	providerFamily = NormalizeProviderType(providerFamily)
	preferredID := SessionEnvProviderID(sandboxID, providerFamily)
	candidates := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if NormalizeProviderType(provider.ProviderType) != providerFamily {
			continue
		}
		if IsSessionEnvProviderID(provider.ID) && provider.ID != preferredID {
			continue
		}
		candidates = append(candidates, provider)
	}
	if len(candidates) == 0 {
		return Provider{}, false, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.ID == preferredID && right.ID != preferredID {
			return true
		}
		if right.ID == preferredID && left.ID != preferredID {
			return false
		}
		leftPriority := ProviderSelectionPriority(left.Scope)
		rightPriority := ProviderSelectionPriority(right.Scope)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if left.Weight != right.Weight {
			return left.Weight < right.Weight
		}
		return strings.TrimSpace(left.ID) < strings.TrimSpace(right.ID)
	})
	return candidates[0], true, nil
}
