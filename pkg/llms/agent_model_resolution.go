package llms

import (
	"context"
	"fmt"
	"os"
	"strings"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

// AgentModelSource identifies the configuration layer that selected a model.
type AgentModelSource string

// Agent model sources distinguish persisted intent from runtime defaults.
const (
	AgentModelSourceProject         AgentModelSource = "project"
	AgentModelSourceAgentEnv        AgentModelSource = "agent_env"
	AgentModelSourceDaemonDefault   AgentModelSource = "daemon_default"
	AgentModelSourceProviderDefault AgentModelSource = "provider_default"
	AgentModelSourceUnresolved      AgentModelSource = "unresolved"
)

// AgentModelResolution describes the model preview for a new agent run.
type AgentModelResolution struct {
	Model  string
	Source AgentModelSource
}

// AgentModelResolutionStore is the read-only configuration surface used to
// preview the model a new agent run would select. It deliberately excludes the
// bootstrap write capability required by LLMResolverStore so project queries
// cannot mutate daemon LLM configuration.
type AgentModelResolutionStore interface {
	ProviderListStore
	ProviderModelWireAPIStore
	GlobalEnvStore
	ListEnabledLLMModels(context.Context) ([]Model, error)
}

// ResolveAgentModel previews the model selected for a new run without a
// request- or session-level override. An empty model with provider_default
// means the agent provider owns the final selection.
func ResolveAgentModel(ctx context.Context, config *appconfig.Config, store AgentModelResolutionStore, agent domain.AgentDefinition) (AgentModelResolution, error) {
	provider := domain.NormalizeAgentKind(agent.Provider)
	configuredModel := strings.TrimSpace(agent.Model)
	if configuredModel != "" {
		return AgentModelResolution{Model: configuredModel, Source: AgentModelSourceProject}, nil
	}
	if model := agentEnvironmentModel(provider, agent.EnvItems); model != "" {
		return AgentModelResolution{Model: model, Source: AgentModelSourceAgentEnv}, nil
	}

	providerFamily := agentProviderFamily(provider)
	if providerFamily == "" {
		source := AgentModelSourceProviderDefault
		if provider == "opencode" || provider == "pi" {
			source = AgentModelSourceUnresolved
		}
		return AgentModelResolution{Source: source}, nil
	}

	providers, models, err := enabledLLMConfiguration(ctx, store)
	if err != nil {
		return AgentModelResolution{}, err
	}
	if hasConfiguredProviderForFamily(providers, providerFamily) {
		return selectConfiguredDefaultModel(ctx, store, providers, models, providerFamily)
	}

	model, err := daemonEnvironmentModel(ctx, config, store, providerFamily)
	if err != nil {
		return AgentModelResolution{}, err
	}
	if model != "" {
		return AgentModelResolution{Model: model, Source: AgentModelSourceDaemonDefault}, nil
	}

	resolution, ok, err := selectStoredDefaultModel(ctx, store, providers, models, providerFamily)
	if err != nil {
		return AgentModelResolution{}, err
	}
	if ok {
		return resolution, nil
	}
	return AgentModelResolution{Source: AgentModelSourceProviderDefault}, nil
}

func agentEnvironmentModel(provider string, items []domain.SandboxEnvVar) string {
	switch provider {
	case "codex":
		return firstNonEmptyTrimmed(EnvItemValue(items, "CODEX_MODEL"), EnvItemValue(items, "LLM_MODEL"))
	case "claude":
		return firstNonEmptyTrimmed(EnvItemValue(items, "ANTHROPIC_MODEL"), EnvItemValue(items, "CLAUDE_MODEL"), EnvItemValue(items, "LLM_MODEL"))
	case "opencode":
		return firstNonEmptyTrimmed(EnvItemValue(items, "OPENCODE_MODEL"), EnvItemValue(items, "LLM_MODEL"))
	default:
		return ""
	}
}

func agentProviderFamily(provider string) string {
	switch provider {
	case "codex":
		return ProviderFamilyOpenAI
	case "claude":
		return ProviderFamilyAnthropic
	default:
		return ""
	}
}

func enabledLLMConfiguration(ctx context.Context, store AgentModelResolutionStore) ([]Provider, []Model, error) {
	if store == nil {
		return nil, nil, nil
	}
	providers, err := store.ListEnabledLLMProviders(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list enabled llm providers for agent model preview: %w", err)
	}
	models, err := store.ListEnabledLLMModels(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list enabled llm models for agent model preview: %w", err)
	}
	return providers, models, nil
}

func hasConfiguredProviderForFamily(providers []Provider, providerFamily string) bool {
	for _, provider := range providers {
		if NormalizeProviderType(provider.ProviderType) == providerFamily && ProviderScopeIsConfigured(provider.Scope) {
			return true
		}
	}
	return false
}

func selectConfiguredDefaultModel(ctx context.Context, store AgentModelResolutionStore, providers []Provider, models []Model, providerFamily string) (AgentModelResolution, error) {
	resolution, ok, err := selectStoredDefaultModel(ctx, store, providers, models, providerFamily)
	if err != nil {
		return AgentModelResolution{}, err
	}
	if !ok {
		return AgentModelResolution{Source: AgentModelSourceUnresolved}, nil
	}
	return resolution, nil
}

func selectStoredDefaultModel(ctx context.Context, store AgentModelResolutionStore, providers []Provider, models []Model, providerFamily string) (AgentModelResolution, bool, error) {
	if store == nil || len(providers) == 0 || len(models) == 0 {
		return AgentModelResolution{}, false, nil
	}
	model, _, _, ok, err := SelectModelAndProvider(ctx, store, models, providers, "", providerFamily, "")
	if err != nil {
		return AgentModelResolution{}, false, fmt.Errorf("select default agent model: %w", err)
	}
	if !ok {
		return AgentModelResolution{}, false, nil
	}
	return AgentModelResolution{Model: firstNonEmptyTrimmed(model.Name, model.ID), Source: AgentModelSourceDaemonDefault}, true, nil
}

func daemonEnvironmentModel(ctx context.Context, config *appconfig.Config, store AgentModelResolutionStore, providerFamily string) (string, error) {
	var global []domain.SandboxEnvVar
	if store != nil {
		items, err := store.ListGlobalEnv(ctx)
		if err != nil {
			return "", fmt.Errorf("list global environment for agent model preview: %w", err)
		}
		global = items
	}
	keys := []string{"LLM_MODEL"}
	if providerFamily == ProviderFamilyAnthropic {
		keys = []string{"ANTHROPIC_MODEL", "CLAUDE_MODEL", "LLM_MODEL"}
	}
	for _, key := range keys {
		if value := EnvItemValue(global, key); value != "" {
			return value, nil
		}
	}
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, nil
		}
	}
	for _, key := range keys {
		if value := strings.TrimSpace(configLLMEnvValue(config, key)); value != "" {
			return value, nil
		}
	}
	return "", nil
}
