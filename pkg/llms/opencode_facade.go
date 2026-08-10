package llms

import (
	"context"
	"strings"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

// OpenCodeFacadeStore is the persistence surface needed to resolve an
// OpenCode provider target and issue a runtime facade token.
type OpenCodeFacadeStore interface {
	LLMResolverStore
	SaveLLMFacadeToken(context.Context, FacadeToken) error
}

// EnsureOpenCodeFacadeConfig resolves an OpenCode provider/model pair, writes
// its guest runtime config, and returns the managed facade environment.
func EnsureOpenCodeFacadeConfig(ctx context.Context, config *appconfig.Config, store OpenCodeFacadeStore, sandbox *domain.Sandbox, model, source, runID string) (map[string]string, error) {
	providerID, modelName, err := SplitOpenCodeModel(model)
	if err != nil {
		return nil, err
	}
	baseURL := GuestRuntimeBaseURL(config, sandbox)
	if strings.TrimSpace(baseURL) == "" {
		return nil, nil
	}
	if providerID == "opencode" {
		return nil, nil
	}
	if HasEnabledLLMProviderID(ctx, store, providerID) {
		return ensureOpenCodeConfiguredFacadeConfig(ctx, config, store, sandbox, providerID, modelName, source, runID)
	}
	switch providerID {
	case ProviderFamilyAnthropic:
		return ensureOpenCodeAnthropicFacadeConfig(ctx, config, store, sandbox, modelName, source, runID)
	case ProviderFamilyOpenAI:
		return ensureOpenCodeOpenAIFacadeConfig(ctx, config, store, sandbox, modelName, source, runID)
	default:
		return ensureOpenCodeCustomFacadeConfig(ctx, config, store, sandbox, providerID, modelName, source, runID)
	}
}

func ensureOpenCodeConfiguredFacadeConfig(ctx context.Context, config *appconfig.Config, store OpenCodeFacadeStore, sandbox *domain.Sandbox, providerID, model, source, runID string) (map[string]string, error) {
	providerEnv, err := SandboxProviderEnvItems(ctx, store, sandbox, "")
	if err != nil {
		return nil, err
	}
	target, err := ResolveRuntimeLLMTargetWithEnv(ctx, config, store, sandbox.Summary.ID, "", model, providerID, providerEnv)
	if err != nil {
		return nil, err
	}
	switch NormalizeProviderType(target.Provider.ProviderType) {
	case ProviderFamilyOpenAI:
		familyEnv, err := SandboxProviderEnvItems(ctx, store, sandbox, ProviderFamilyOpenAI)
		if err != nil {
			return nil, err
		}
		if HasOpenAIEnvProviderInput(familyEnv) {
			return ensureOpenCodeOpenAIFacadeConfig(ctx, config, store, sandbox, model, source, runID)
		}
	case ProviderFamilyAnthropic:
		familyEnv, err := SandboxProviderEnvItems(ctx, store, sandbox, ProviderFamilyAnthropic)
		if err != nil {
			return nil, err
		}
		if HasAnthropicEnvProviderInput(familyEnv) {
			return ensureOpenCodeAnthropicFacadeConfig(ctx, config, store, sandbox, model, source, runID)
		}
	}
	return ensureOpenCodeResolvedFacadeConfig(ctx, config, store, sandbox, target, source, runID)
}

func ensureOpenCodeResolvedFacadeConfig(ctx context.Context, config *appconfig.Config, store OpenCodeFacadeStore, sandbox *domain.Sandbox, target ResolvedTarget, source, runID string) (map[string]string, error) {
	baseURL := GuestRuntimeBaseURL(config, sandbox)
	providerFamily := NormalizeProviderType(target.Provider.ProviderType)
	if providerFamily == ProviderFamilyAnthropic {
		tokenValue, token, err := NewFacadeToken(sandbox.Summary.ID, target.Model.Name, target.Provider.ID, APIProtocolMessages, source, runID)
		if err != nil {
			return nil, err
		}
		if err := store.SaveLLMFacadeToken(ctx, token); err != nil {
			return nil, err
		}
		anthropicBaseURL := strings.TrimRight(baseURL, "/") + "/api/runtime/sandboxes/" + sandbox.Summary.ID + "/llm/anthropic"
		if err := WriteOpenCodeAnthropicRuntimeConfig(sandbox, target.Model.Name, anthropicBaseURL+"/v1"); err != nil {
			return nil, err
		}
		return map[string]string{
			"AGENT_COMPOSE_SANDBOX_TOKEN": tokenValue, "LLM_API_ENDPOINT": anthropicBaseURL,
			"LLM_API_KEY": tokenValue, "LLM_API_PROTOCOL": APIProtocolMessages,
			"LLM_MODEL": "anthropic/" + target.Model.Name, "OPENCODE_MODEL": "anthropic/" + target.Model.Name,
			"ANTHROPIC_API_KEY": tokenValue, "ANTHROPIC_AUTH_TOKEN": tokenValue,
			"ANTHROPIC_BASE_URL": anthropicBaseURL, "OPENCODE_CONFIG": GuestOpenCodeConfigPath(config),
		}, nil
	}
	if providerFamily != ProviderFamilyOpenAI {
		return nil, domain.ClassifyError(domain.ErrFailedPrecondition, "opencode requires an OpenAI-compatible or Anthropic model", nil)
	}
	protocol := NormalizeWireAPI(target.WireAPI)
	if protocol != APIProtocolResponses && protocol != APIProtocolChatCompletions {
		return nil, domain.ClassifyError(domain.ErrFailedPrecondition, "opencode model uses an unsupported wire protocol", nil)
	}
	tokenValue, token, err := NewFacadeToken(sandbox.Summary.ID, target.Model.Name, target.Provider.ID, protocol, source, runID)
	if err != nil {
		return nil, err
	}
	if err := store.SaveLLMFacadeToken(ctx, token); err != nil {
		return nil, err
	}
	openAIBaseURL := strings.TrimRight(baseURL, "/") + "/api/runtime/sandboxes/" + sandbox.Summary.ID + "/llm/openai/v1"
	if err := WriteOpenCodeRuntimeConfig(sandbox, "agent-compose", target.Model.Name, openAIBaseURL); err != nil {
		return nil, err
	}
	env := openCodeOpenAIEnv(tokenValue, openAIBaseURL, protocol, config)
	env["LLM_MODEL"] = "agent-compose/" + target.Model.Name
	env["OPENCODE_MODEL"] = "agent-compose/" + target.Model.Name
	return env, nil
}

func ensureOpenCodeAnthropicFacadeConfig(ctx context.Context, config *appconfig.Config, store OpenCodeFacadeStore, sandbox *domain.Sandbox, model, source, runID string) (map[string]string, error) {
	providerEnv, err := SandboxProviderEnvItems(ctx, store, sandbox, ProviderFamilyAnthropic)
	if err != nil {
		return nil, err
	}
	target, err := ResolveRuntimeLLMTargetWithEnv(ctx, config, store, sandbox.Summary.ID, ProviderFamilyAnthropic, model, "", providerEnv)
	if err != nil {
		return nil, err
	}
	baseURL := GuestRuntimeBaseURL(config, sandbox)
	tokenValue, token, err := NewFacadeToken(sandbox.Summary.ID, target.Model.Name, target.Provider.ID, APIProtocolMessages, source, runID)
	if err != nil {
		return nil, err
	}
	if err := store.SaveLLMFacadeToken(ctx, token); err != nil {
		return nil, err
	}
	anthropicBaseURL := strings.TrimRight(baseURL, "/") + "/api/runtime/sandboxes/" + sandbox.Summary.ID + "/llm/anthropic"
	if err := WriteOpenCodeAnthropicRuntimeConfig(sandbox, target.Model.Name, anthropicBaseURL+"/v1"); err != nil {
		return nil, err
	}
	return map[string]string{
		"AGENT_COMPOSE_SANDBOX_TOKEN": tokenValue,
		"LLM_API_ENDPOINT":            anthropicBaseURL,
		"LLM_API_KEY":                 tokenValue,
		"LLM_API_PROTOCOL":            APIProtocolMessages,
		"LLM_MODEL":                   "anthropic/" + target.Model.Name,
		"ANTHROPIC_API_KEY":           tokenValue,
		"ANTHROPIC_AUTH_TOKEN":        tokenValue,
		"ANTHROPIC_BASE_URL":          anthropicBaseURL,
		"OPENCODE_CONFIG":             GuestOpenCodeConfigPath(config),
		"OPENCODE_MODEL":              "anthropic/" + target.Model.Name,
	}, nil
}

func ensureOpenCodeOpenAIFacadeConfig(ctx context.Context, config *appconfig.Config, store OpenCodeFacadeStore, sandbox *domain.Sandbox, model, source, runID string) (map[string]string, error) {
	providerEnv, err := SandboxProviderEnvItems(ctx, store, sandbox, ProviderFamilyOpenAI)
	if err != nil {
		return nil, err
	}
	target, err := ResolveRuntimeLLMTargetWithEnv(ctx, config, store, sandbox.Summary.ID, ProviderFamilyOpenAI, model, "", providerEnv)
	if err != nil {
		return nil, err
	}
	baseURL := GuestRuntimeBaseURL(config, sandbox)
	tokenValue, token, err := NewFacadeToken(sandbox.Summary.ID, target.Model.Name, target.Provider.ID, APIProtocolResponses, source, runID)
	if err != nil {
		return nil, err
	}
	if err := store.SaveLLMFacadeToken(ctx, token); err != nil {
		return nil, err
	}
	openAIBaseURL := strings.TrimRight(baseURL, "/") + "/api/runtime/sandboxes/" + sandbox.Summary.ID + "/llm/openai/v1"
	if err := WriteOpenCodeRuntimeConfig(sandbox, "agent-compose", target.Model.Name, openAIBaseURL); err != nil {
		return nil, err
	}
	env := openCodeOpenAIEnv(tokenValue, openAIBaseURL, APIProtocolResponses, config)
	env["LLM_MODEL"] = "agent-compose/" + target.Model.Name
	env["OPENCODE_MODEL"] = "agent-compose/" + target.Model.Name
	return env, nil
}

func ensureOpenCodeCustomFacadeConfig(ctx context.Context, config *appconfig.Config, store OpenCodeFacadeStore, sandbox *domain.Sandbox, providerID, model, source, runID string) (map[string]string, error) {
	target, err := resolveCustomOpenAIFacadeTarget(ctx, config, store, sandbox, providerID, model)
	if err != nil {
		return nil, err
	}
	baseURL := GuestRuntimeBaseURL(config, sandbox)
	tokenValue, token, err := NewFacadeToken(sandbox.Summary.ID, target.Model.Name, target.Provider.ID, APIProtocolChatCompletions, source, runID)
	if err != nil {
		return nil, err
	}
	if err := store.SaveLLMFacadeToken(ctx, token); err != nil {
		return nil, err
	}
	openAIBaseURL := strings.TrimRight(baseURL, "/") + "/api/runtime/sandboxes/" + sandbox.Summary.ID + "/llm/openai/v1"
	if err := WriteOpenCodeRuntimeConfig(sandbox, providerID, target.Model.Name, openAIBaseURL); err != nil {
		return nil, err
	}
	return openCodeOpenAIEnv(tokenValue, openAIBaseURL, APIProtocolChatCompletions, config), nil
}

func openCodeOpenAIEnv(tokenValue, baseURL, protocol string, config *appconfig.Config) map[string]string {
	return map[string]string{
		"AGENT_COMPOSE_SANDBOX_TOKEN": tokenValue,
		"LLM_API_ENDPOINT":            baseURL,
		"LLM_API_KEY":                 tokenValue,
		"LLM_API_PROTOCOL":            protocol,
		"OPENAI_API_KEY":              tokenValue,
		"OPENAI_BASE_URL":             baseURL,
		"OPENCODE_CONFIG":             GuestOpenCodeConfigPath(config),
	}
}
