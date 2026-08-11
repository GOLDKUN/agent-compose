package llms

import (
	"context"
	"fmt"
	"strings"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

func resolveCustomOpenAIFacadeTarget(ctx context.Context, config *appconfig.Config, store LLMResolverStore, sandbox *domain.Sandbox, providerID, model string) (ResolvedTarget, error) {
	envItems, err := SandboxProviderEnvItems(ctx, store, sandbox, ProviderFamilyOpenAI)
	if err != nil {
		return ResolvedTarget{}, err
	}
	sandboxID := ""
	if sandbox != nil {
		sandboxID = sandbox.Summary.ID
	}
	if sandboxID != "" && HasOpenAIEnvProviderInput(envItems) {
		sessionProviderID, err := ensureSessionOpenAIEnvProviderWithConfig(ctx, config, store, sandboxID, model, envItems)
		if err != nil {
			return ResolvedTarget{}, err
		}
		if strings.TrimSpace(sessionProviderID) != "" {
			return ResolveRuntimeLLMTargetWithEnv(ctx, config, store, sandboxID, ProviderFamilyOpenAI, model, sessionProviderID, envItems)
		}
	}
	if HasEnabledLLMProviderID(ctx, store, providerID) {
		return ResolveRuntimeLLMTargetWithEnv(ctx, config, store, sandboxID, ProviderFamilyOpenAI, model, providerID, envItems)
	}
	// An explicit custom provider reference (anything other than the "openai"/
	// "anthropic" legacy aliases, which the caller already special-cases) is
	// pinned to that provider id. It must never silently borrow the daemon's
	// env-default OpenAI credentials when the named provider is unknown or
	// unavailable (e.g. a catalog provider with no API key) — the request has
	// to fail locally instead of being forwarded to the wrong upstream.
	return ResolvedTarget{}, domain.ClassifyError(domain.ErrFailedPrecondition, fmt.Sprintf("llm provider %q is not configured", providerID), nil)
}
