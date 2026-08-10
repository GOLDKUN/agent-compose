package configstore

import (
	"context"
	"strings"
	"testing"

	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/llms"
)

func TestIntegrationApplyModelCatalogResolvesLiteralModelsDefaultsAndBehavior(t *testing.T) {
	clearLLMTestEnvironment(t)
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	baseURL, protocol, apiKey, limit := "https://gateway.example/api/openai", llms.APIProtocolChatCompletions, "catalog-key", 99999
	catalog := llms.ModelCatalog{Default: "baizhi/deepseek-v4-flash", Providers: map[string]llms.CatalogProvider{
		"baizhi": {BaseURL: &baseURL, Protocol: &protocol, APIKey: &apiKey, Models: []llms.CatalogModel{{ID: "deepseek-v4-flash", MaxOutputTokens: &limit}}},
	}}
	if err := store.ApplyModelCatalog(ctx, catalog); err != nil {
		t.Fatalf("ApplyModelCatalog: %v", err)
	}

	providerID, modelID, ok, err := store.DefaultLLMModelReference(ctx)
	if err != nil || !ok || providerID != "baizhi" || modelID != "deepseek-v4-flash" {
		t.Fatalf("default = %q/%q ok=%v err=%v", providerID, modelID, ok, err)
	}
	literal, err := llms.ResolveRuntimeLLMTargetWithEnv(ctx, nil, store, "", "", "baizhi/upstream-only-model", "", nil)
	if err != nil {
		t.Fatalf("resolve literal model: %v", err)
	}
	if literal.Model.ID != "upstream-only-model" || literal.MaxOutputTokens != 0 {
		t.Fatalf("literal target = %#v", literal)
	}
	configured, err := llms.ResolveRuntimeLLMTargetWithEnv(ctx, nil, store, "", "", "baizhi/deepseek-v4-flash", "", nil)
	if err != nil {
		t.Fatalf("resolve configured model: %v", err)
	}
	if configured.MaxOutputTokens != limit || configured.Provider.APIKey != apiKey {
		t.Fatalf("configured target = %#v", configured)
	}
	defaultTarget, err := llms.ResolveRuntimeLLMTargetWithEnv(ctx, nil, store, "", "", "", "", nil)
	if err != nil {
		t.Fatalf("resolve catalog default: %v", err)
	}
	if defaultTarget.Provider.ID != "baizhi" || defaultTarget.Model.ID != "deepseek-v4-flash" {
		t.Fatalf("catalog default target = %#v", defaultTarget)
	}
}

func TestIntegrationDaemonEnvironmentDefaultWinsButExplicitCatalogProviderRemainsPinned(t *testing.T) {
	clearLLMTestEnvironment(t)
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	baseURL, protocol, apiKey := "https://gateway.example/v1", llms.APIProtocolResponses, "catalog-key"
	if err := store.ApplyModelCatalog(ctx, llms.ModelCatalog{Default: "baizhi/catalog-model", Providers: map[string]llms.CatalogProvider{
		"baizhi": {BaseURL: &baseURL, Protocol: &protocol, APIKey: &apiKey},
	}}); err != nil {
		t.Fatal(err)
	}
	config := &appconfig.Config{LLMAPIEndpoint: "https://legacy.example/v1", LLMAPIProtocol: llms.APIProtocolResponses, LLMAPIKey: "legacy-key", LLMModel: "feature/gpt-5.6-sol"}
	legacy, err := llms.ResolveRuntimeLLMTargetWithEnv(ctx, config, store, "", "", "", "", nil)
	if err != nil {
		t.Fatalf("resolve daemon environment default: %v", err)
	}
	if legacy.Provider.ID != llms.ProviderIDDefaultOpenAI || legacy.Model.ID != "feature/gpt-5.6-sol" || legacy.Provider.APIKey != "legacy-key" {
		t.Fatalf("daemon environment target = %#v", legacy)
	}
	explicit, err := llms.ResolveRuntimeLLMTargetWithEnv(ctx, config, store, "", "", "baizhi/literal-model", "", nil)
	if err != nil {
		t.Fatalf("resolve explicit catalog provider: %v", err)
	}
	if explicit.Provider.ID != "baizhi" || explicit.Model.ID != "literal-model" || explicit.Provider.APIKey != apiKey {
		t.Fatalf("explicit catalog target = %#v", explicit)
	}
}

func TestIntegrationExplicitUnavailableCatalogProviderDoesNotFallBack(t *testing.T) {
	clearLLMTestEnvironment(t)
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	baseURL, protocol := "https://unavailable.example/v1", llms.APIProtocolResponses
	if err := store.ApplyModelCatalog(ctx, llms.ModelCatalog{Providers: map[string]llms.CatalogProvider{"custom": {BaseURL: &baseURL, Protocol: &protocol}}}); err != nil {
		t.Fatal(err)
	}
	_, err := llms.ResolveRuntimeLLMTargetWithEnv(ctx, &appconfig.Config{LLMAPIEndpoint: "https://daemon.example/v1", LLMAPIKey: "daemon-key", LLMModel: "daemon-model"}, store, "", "", "custom/literal-model", "", nil)
	if err == nil || !strings.Contains(err.Error(), `provider "custom"`) {
		t.Fatalf("explicit unavailable provider error = %v", err)
	}
}

func clearLLMTestEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"LLM_API_ENDPOINT", "LLM_API_PROTOCOL", "LLM_API_KEY", "LLM_MODEL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_API_ENDPOINT", "ANTHROPIC_MODEL", "CLAUDE_MODEL"} {
		t.Setenv(key, "")
	}
}
