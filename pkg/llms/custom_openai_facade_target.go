package llms

import (
	"context"
	"fmt"
	"strings"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

// customOpenAIFacadeTargetRequest groups resolveCustomOpenAIFacadeTarget's
// inputs.
type customOpenAIFacadeTargetRequest struct {
	Config     *appconfig.Config
	Store      LLMResolverStore
	Sandbox    *domain.Sandbox
	ProviderID string
	Model      string
}

func resolveCustomOpenAIFacadeTarget(ctx context.Context, req customOpenAIFacadeTargetRequest) (ResolvedTarget, error) {
	config, store, sandbox, providerID, model := req.Config, req.Store, req.Sandbox, req.ProviderID, req.Model
	envItems, err := SandboxProviderEnvItems(ctx, store, sandbox, ProviderFamilyOpenAI)
	if err != nil {
		return ResolvedTarget{}, err
	}
	sandboxID := ""
	if sandbox != nil {
		sandboxID = sandbox.Summary.ID
	}
	if sandboxID != "" && HasOpenAIEnvProviderInput(envItems) {
		sessionProviderID, err := ensureSessionOpenAIEnvProviderWithConfig(ctx, store, SessionEnvProviderQuery{
			Config: config, SessionID: sandboxID, RequestedModel: model, EnvItems: envItems,
		})
		if err != nil {
			return ResolvedTarget{}, err
		}
		if strings.TrimSpace(sessionProviderID) != "" {
			return ResolveRuntimeLLMTargetWithEnv(ctx, store, RuntimeLLMTargetQuery{
				Config: config, SessionID: sandboxID, PreferredProviderFamily: ProviderFamilyOpenAI, RequestedModel: model, ProviderID: sessionProviderID, EnvItems: envItems,
			})
		}
	}
	if HasEnabledLLMProviderID(ctx, store, providerID) {
		return ResolveRuntimeLLMTargetWithEnv(ctx, store, RuntimeLLMTargetQuery{
			Config: config, SessionID: sandboxID, PreferredProviderFamily: ProviderFamilyOpenAI, RequestedModel: model, ProviderID: providerID, EnvItems: envItems,
		})
	}
	// An explicit custom provider reference (anything other than the "openai"/
	// "anthropic" legacy aliases, which the caller already special-cases) is
	// pinned to that provider id. It must never silently borrow the daemon's
	// env-default OpenAI credentials when the named provider is unknown or
	// unavailable (e.g. a catalog provider with no API key) — the request has
	// to fail locally instead of being forwarded to the wrong upstream.
	return ResolvedTarget{}, domain.ClassifyError(domain.ErrFailedPrecondition, fmt.Sprintf("llm provider %q is not configured", providerID), nil)
}
