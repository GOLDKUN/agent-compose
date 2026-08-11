package llms

import (
	"context"
	"testing"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

func TestResolveCustomOpenAIFacadeTargetFailsLocallyInsteadOfBorrowingDaemonDefault(t *testing.T) {
	isolateLLMEnv(t)
	t.Setenv("OPENAI_API_KEY", "daemon-default-key")

	ctx := context.Background()
	store := newResolverCoverageStore()
	config := &appconfig.Config{
		LLMAPIEndpoint: "https://api.openai.test",
		LLMAPIProtocol: APIProtocolChatCompletions,
		LLMAPIKey:      "daemon-default-key",
	}
	sandbox := &domain.Sandbox{Summary: domain.SandboxSummary{ID: "sandbox-1"}}

	if _, err := resolveCustomOpenAIFacadeTarget(ctx, config, store, sandbox, "unavailable-catalog-provider", "some-model"); err == nil {
		t.Fatal("resolveCustomOpenAIFacadeTarget returned nil error for an unknown/unavailable provider")
	}
	if len(store.providers) != 0 {
		t.Fatalf("resolveCustomOpenAIFacadeTarget silently bootstrapped a provider: %#v", store.providers)
	}
}

func TestResolveCustomOpenAIFacadeTargetUsesEnabledCatalogProvider(t *testing.T) {
	isolateLLMEnv(t)

	ctx := context.Background()
	store := newResolverCoverageStore()
	store.providers = []Provider{{
		ID:             "baizhi",
		ProviderType:   ProviderFamilyOpenAI,
		DefaultWireAPI: APIProtocolChatCompletions,
		BaseURL:        "https://baizhi.test",
		APIKey:         "baizhi-key",
		Enabled:        true,
		Scope:          ProviderScopeSystem,
	}}
	store.models = []Model{{ID: "deepseek-v4-flash", Name: "deepseek-v4-flash", Enabled: true, Scope: ProviderScopeSystem}}
	sandbox := &domain.Sandbox{Summary: domain.SandboxSummary{ID: "sandbox-1"}}

	target, err := resolveCustomOpenAIFacadeTarget(ctx, &appconfig.Config{}, store, sandbox, "baizhi", "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("resolveCustomOpenAIFacadeTarget returned error: %v", err)
	}
	if target.Provider.ID != "baizhi" || target.Model.ID != "deepseek-v4-flash" {
		t.Fatalf("target = %#v, want pinned baizhi provider", target)
	}
}
