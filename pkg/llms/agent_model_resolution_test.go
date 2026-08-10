package llms

import (
	"context"
	"errors"
	"testing"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

type agentModelResolutionStoreStub struct {
	providers []Provider
	models    []Model
	globalEnv []domain.SandboxEnvVar
	wireAPIs  map[string]string
	err       error
	calls     *agentModelResolutionStoreCalls
}

type agentModelResolutionStoreCalls struct {
	providers int
	models    int
	globalEnv int
	wireAPIs  int
}

func (s agentModelResolutionStoreStub) ListEnabledLLMProviders(context.Context) ([]Provider, error) {
	if s.calls != nil {
		s.calls.providers++
	}
	if s.err != nil {
		return nil, s.err
	}
	return append([]Provider(nil), s.providers...), nil
}

func (s agentModelResolutionStoreStub) ListEnabledLLMModels(context.Context) ([]Model, error) {
	if s.calls != nil {
		s.calls.models++
	}
	return append([]Model(nil), s.models...), nil
}

func (s agentModelResolutionStoreStub) ListGlobalEnv(context.Context) ([]domain.SandboxEnvVar, error) {
	if s.calls != nil {
		s.calls.globalEnv++
	}
	return append([]domain.SandboxEnvVar(nil), s.globalEnv...), nil
}

func (s agentModelResolutionStoreStub) LLMProviderModelWireAPI(_ context.Context, providerID, modelID string) (string, bool, error) {
	if s.calls != nil {
		s.calls.wireAPIs++
	}
	wireAPI, ok := s.wireAPIs[providerID+"\x00"+modelID]
	return wireAPI, ok, nil
}

func TestResolveAgentModelPrecedence(t *testing.T) {
	t.Setenv("LLM_MODEL", "")
	store := agentModelResolutionStoreStub{
		providers: []Provider{{ID: "openai", ProviderType: ProviderFamilyOpenAI, Scope: ProviderScopeSystem, Enabled: true}},
		models:    []Model{{ID: "daemon-model", Name: "daemon-model", DefaultModel: true, Enabled: true}},
		globalEnv: []domain.SandboxEnvVar{{Name: "LLM_MODEL", Value: "global-model"}},
		wireAPIs:  map[string]string{"openai\x00daemon-model": APIProtocolResponses},
	}
	tests := []struct {
		name  string
		agent domain.AgentDefinition
		want  AgentModelResolution
	}{
		{name: "project", agent: domain.AgentDefinition{Provider: "codex", Model: "project-model", EnvItems: []domain.SandboxEnvVar{{Name: "LLM_MODEL", Value: "agent-model"}}}, want: AgentModelResolution{Model: "project-model", Source: AgentModelSourceProject}},
		{name: "agent env", agent: domain.AgentDefinition{Provider: "codex", EnvItems: []domain.SandboxEnvVar{{Name: "CODEX_MODEL", Value: "agent-model"}}}, want: AgentModelResolution{Model: "agent-model", Source: AgentModelSourceAgentEnv}},
		{name: "configured daemon", agent: domain.AgentDefinition{Provider: "codex"}, want: AgentModelResolution{Model: "global-model", Source: AgentModelSourceDaemonDefault}},
		{name: "provider default", agent: domain.AgentDefinition{Provider: "gemini"}, want: AgentModelResolution{Source: AgentModelSourceProviderDefault}},
		{name: "required model unresolved", agent: domain.AgentDefinition{Provider: "pi"}, want: AgentModelResolution{Source: AgentModelSourceUnresolved}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveAgentModel(context.Background(), &appconfig.Config{LLMModel: "config-model"}, store, test.agent)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("resolution = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveAgentModelUsesDaemonEnvironmentWithoutConfiguredProvider(t *testing.T) {
	t.Setenv("LLM_MODEL", "")
	resolution, err := ResolveAgentModel(context.Background(), &appconfig.Config{LLMModel: "config-model"}, agentModelResolutionStoreStub{}, domain.AgentDefinition{Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Model != "config-model" || resolution.Source != AgentModelSourceDaemonDefault {
		t.Fatalf("resolution = %#v", resolution)
	}

	resolution, err = ResolveAgentModel(context.Background(), &appconfig.Config{LLMModel: "config-model"}, agentModelResolutionStoreStub{globalEnv: []domain.SandboxEnvVar{{Name: "CLAUDE_MODEL", Value: "  global-claude  "}}}, domain.AgentDefinition{Provider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Model != "global-claude" || resolution.Source != AgentModelSourceDaemonDefault {
		t.Fatalf("resolution = %#v", resolution)
	}
}

func TestResolveAgentModelsCachesSharedConfigurationQueries(t *testing.T) {
	t.Setenv("LLM_MODEL", "")
	calls := &agentModelResolutionStoreCalls{}
	store := agentModelResolutionStoreStub{
		providers: []Provider{{ID: "openai", ProviderType: ProviderFamilyOpenAI, Scope: ProviderScopeSystem, Enabled: true}},
		models:    []Model{{ID: "daemon-model", Name: "daemon-model", DefaultModel: true, Enabled: true}},
		wireAPIs:  map[string]string{"openai\x00daemon-model": APIProtocolResponses},
		calls:     calls,
	}
	resolutions, err := ResolveAgentModels(context.Background(), nil, store, []domain.AgentDefinition{{Provider: "codex"}, {Provider: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolutions) != 2 || resolutions[0].Model != "daemon-model" || resolutions[1].Model != "daemon-model" {
		t.Fatalf("resolutions = %#v", resolutions)
	}
	if calls.providers != 1 || calls.models != 1 || calls.wireAPIs != 1 || calls.globalEnv != 1 {
		t.Fatalf("store calls = %#v, want providers/models/global env/wire once", calls)
	}

	envCalls := &agentModelResolutionStoreCalls{}
	resolutions, err = ResolveAgentModels(context.Background(), nil, agentModelResolutionStoreStub{
		globalEnv: []domain.SandboxEnvVar{{Name: "LLM_MODEL", Value: "global-model"}},
		calls:     envCalls,
	}, []domain.AgentDefinition{{Provider: "codex"}, {Provider: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolutions) != 2 || resolutions[0].Model != "global-model" || resolutions[1].Model != "global-model" {
		t.Fatalf("global env resolutions = %#v", resolutions)
	}
	if envCalls.providers != 0 || envCalls.models != 0 || envCalls.globalEnv != 1 || envCalls.wireAPIs != 0 {
		t.Fatalf("global env store calls = %#v, want only one global env lookup", envCalls)
	}
}

func TestResolveAgentModelReturnsReadFailure(t *testing.T) {
	wantErr := errors.New("read failed")
	_, err := ResolveAgentModel(context.Background(), nil, agentModelResolutionStoreStub{err: wantErr}, domain.AgentDefinition{Provider: "codex"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
