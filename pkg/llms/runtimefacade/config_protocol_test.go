package runtimefacade

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/samber/do/v2"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/internal/testutil"
	"agent-compose/pkg/llms"
	domain "agent-compose/pkg/model"
)

func TestIntegrationEnsureSessionOpenCodeKeepsResponsesIngressForChatUpstream(t *testing.T) {
	isolateLLMEnv(t)

	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:       root,
		DbAddr:         filepath.Join(root, "data.db"),
		RuntimeBaseURL: "http://agent-compose.test:7410",
		GuestHomePath:  "/root",
	}
	di := do.New()
	do.ProvideValue(di, ctx)
	do.ProvideValue(di, config)
	store, err := testutil.OpenConfigStore(t, di)
	if err != nil {
		t.Fatalf("NewConfigStore returned error: %v", err)
	}
	session := &domain.Sandbox{Summary: domain.SandboxSummary{
		ID:            "sandbox-opencode-chat-upstream",
		Driver:        driverpkg.RuntimeDriverDocker,
		WorkspacePath: filepath.Join(root, "sandboxes", "sandbox-opencode-chat-upstream", "workspace"),
	}}
	llms.SetSandboxProviderEnvItems(session, []domain.SandboxEnvVar{
		{Name: "LLM_API_ENDPOINT", Value: "https://chat.example.test/v1"},
		{Name: "LLM_API_PROTOCOL", Value: llms.APIProtocolChatCompletions},
		{Name: "LLM_API_KEY", Value: "chat-key", Secret: true},
		{Name: "LLM_MODEL", Value: "gpt-chat"},
	})

	runtimeConfig, err := EnsureSessionAgentRuntimeConfig(ctx, SessionFacadeConfigRequest{Config: config, Store: store, Session: session, Agent: "opencode", Model: "openai/gpt-chat", Source: TokenSourceAgent, RunID: "run-chat"})
	if err != nil {
		t.Fatalf("EnsureSessionAgentRuntimeConfig returned error: %v", err)
	}
	env := runtimeConfig.Env
	if env["LLM_API_PROTOCOL"] != llms.APIProtocolResponses {
		t.Fatalf("OpenCode ingress protocol = %q, want responses", env["LLM_API_PROTOCOL"])
	}
	if runtimeConfig.Model != "agent-compose/gpt-chat" {
		t.Fatalf("OpenCode runtime model = %q, want facade model", runtimeConfig.Model)
	}
	token, err := store.GetLLMFacadeToken(ctx, env["AGENT_COMPOSE_SANDBOX_TOKEN"])
	if err != nil {
		t.Fatalf("GetLLMFacadeToken returned error: %v", err)
	}
	if token.WireAPI != llms.APIProtocolResponses {
		t.Fatalf("facade token wire API = %q, want responses", token.WireAPI)
	}
	providers, err := store.ListEnabledLLMProviders(ctx)
	if err != nil {
		t.Fatalf("ListEnabledLLMProviders returned error: %v", err)
	}
	if len(providers) != 1 || providers[0].DefaultWireAPI != llms.APIProtocolChatCompletions {
		t.Fatalf("upstream providers = %#v", providers)
	}
}

func TestIntegrationEnsureSessionOpenCodeKeepsConfiguredProviderInMixedEnvironment(t *testing.T) {
	isolateLLMEnv(t)

	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:       root,
		DbAddr:         filepath.Join(root, "data.db"),
		RuntimeBaseURL: "http://agent-compose.test:7410",
		GuestHomePath:  "/root",
	}
	di := do.New()
	do.ProvideValue(di, ctx)
	do.ProvideValue(di, config)
	store, err := testutil.OpenConfigStore(t, di)
	if err != nil {
		t.Fatalf("NewConfigStore returned error: %v", err)
	}
	stringPointer := func(value string) *string { return &value }
	if err := store.ApplyModelCatalog(ctx, llms.ModelCatalog{Providers: map[string]llms.CatalogProvider{
		"baizhi": {
			BaseURL:  stringPointer("https://baizhi.example.test/api/openai"),
			Protocol: stringPointer(llms.APIProtocolChatCompletions),
			APIKey:   stringPointer("baizhi-key"),
			Models:   []llms.CatalogModel{{ID: "deepseek-v4-flash"}},
		},
		"openai": {
			BaseURL:  stringPointer("https://openai.example.test/v1"),
			Protocol: stringPointer(llms.APIProtocolResponses),
			APIKey:   stringPointer("configured-openai-key"),
			Models:   []llms.CatalogModel{{ID: "gpt-test"}},
		},
	}}); err != nil {
		t.Fatalf("save configured provider: %v", err)
	}
	session := &domain.Sandbox{Summary: domain.SandboxSummary{
		ID:            "sandbox-opencode-mixed-provider-env",
		Driver:        driverpkg.RuntimeDriverDocker,
		WorkspacePath: filepath.Join(root, "sandboxes", "sandbox-opencode-mixed-provider-env", "workspace"),
	}}
	llms.SetSandboxProviderEnvItems(session, []domain.SandboxEnvVar{
		{Name: "LLM_API_ENDPOINT", Value: "https://legacy-openai.example.test/v1"},
		{Name: "LLM_API_KEY", Value: "legacy-openai-key", Secret: true},
		{Name: "LLM_MODEL", Value: "legacy-gpt"},
		{Name: "ANTHROPIC_BASE_URL", Value: "https://anthropic.example.test"},
		{Name: "ANTHROPIC_API_KEY", Value: "anthropic-key", Secret: true},
		{Name: "ANTHROPIC_MODEL", Value: "claude-test"},
	})

	assertTarget := func(testSession *domain.Sandbox, reference, model, protocol, providerID, runID string) {
		t.Helper()
		runtimeConfig, err := EnsureSessionAgentRuntimeConfig(ctx, SessionFacadeConfigRequest{Config: config, Store: store, Session: testSession, Agent: "opencode", Model: reference, Source: TokenSourceAgent, RunID: runID})
		if err != nil {
			t.Fatalf("EnsureSessionAgentRuntimeConfig(%q) returned error: %v", reference, err)
		}
		if runtimeConfig.Env["LLM_API_PROTOCOL"] != protocol || runtimeConfig.Model != "agent-compose/"+model {
			t.Fatalf("OpenCode runtime config for %q = %#v", reference, runtimeConfig)
		}
		token, err := store.GetLLMFacadeToken(ctx, runtimeConfig.Env["AGENT_COMPOSE_SANDBOX_TOKEN"])
		if err != nil {
			t.Fatalf("GetLLMFacadeToken(%q) returned error: %v", reference, err)
		}
		if token.ProviderID != providerID || token.Model != model {
			t.Fatalf("facade token for %q = %#v", reference, token)
		}
	}

	sessionProviderID := llms.SessionEnvProviderID(session.Summary.ID, llms.ProviderFamilyOpenAI)
	assertTarget(session, "openai/gpt-test", "legacy-gpt", llms.APIProtocolResponses, sessionProviderID, "run-openai-env")
	assertTarget(session, "baizhi/deepseek-v4-flash", "legacy-gpt", llms.APIProtocolResponses, sessionProviderID, "run-baizhi-env")

	exactSession := &domain.Sandbox{Summary: domain.SandboxSummary{
		ID:            "sandbox-opencode-configured-provider",
		Driver:        driverpkg.RuntimeDriverDocker,
		WorkspacePath: filepath.Join(root, "sandboxes", "sandbox-opencode-configured-provider", "workspace"),
	}}
	assertTarget(exactSession, "openai/gpt-test", "gpt-test", llms.APIProtocolResponses, "openai", "run-openai-configured")
	assertTarget(exactSession, "baizhi/deepseek-v4-flash", "deepseek-v4-flash", llms.APIProtocolChatCompletions, "baizhi", "run-baizhi-configured")
}
