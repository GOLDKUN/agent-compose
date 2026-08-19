package llms

import (
	"context"
	"strings"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

// DshFacadeStore is the persistence surface needed to resolve a DSH model and
// issue its run-scoped runtime facade token.
type DshFacadeStore interface {
	LLMResolverStore
	SaveLLMFacadeToken(context.Context, FacadeToken) error
}

// SplitDshModel parses DSH's required <llm-provider-id>/<model-name>
// selection (same format as Pi/OpenCode, see agent-compose-yaml-manual.md).
func SplitDshModel(value string) (string, string, error) {
	providerID, model, ok := strings.Cut(strings.TrimSpace(value), "/")
	providerID = strings.TrimSpace(providerID)
	model = strings.TrimSpace(model)
	if !ok || providerID == "" || model == "" {
		return "", "", domain.ClassifyError(domain.ErrRequired, "dsh model must use <llm-provider-id>/<model-name>", nil)
	}
	return providerID, model, nil
}

// DshFacadeConfigRequest groups EnsureDshFacadeConfig's inputs: the
// environment to resolve against (Config/Store/Sandbox) plus the specific
// call's provider/model selection and token attribution.
type DshFacadeConfigRequest struct {
	Config  *appconfig.Config
	Store   DshFacadeStore
	Sandbox *domain.Sandbox
	Model   string
	Source  string
	RunID   string
}

// EnsureDshFacadeConfig resolves DSH's explicit provider/model selection and
// returns run-scoped facade credentials. Unlike Pi, DSH's llm-deepseek
// adapter only ever speaks chat completions on the wire (its own upstream
// protocol is irrelevant: the facade bridges), so the issued token's WireAPI
// is unconditionally APIProtocolChatCompletions and the guest is always
// routed to /llm/openai/v1 (see docs/design/dsh_agent_provider_design.md §4.1).
func EnsureDshFacadeConfig(ctx context.Context, req DshFacadeConfigRequest) (map[string]string, error) {
	config, store, sandbox := req.Config, req.Store, req.Sandbox
	providerID, modelName, err := SplitDshModel(req.Model)
	if err != nil {
		return nil, err
	}
	baseURL := GuestRuntimeBaseURL(config, sandbox)
	if strings.TrimSpace(baseURL) == "" {
		return nil, nil
	}

	target, err := resolveDshFacadeTarget(ctx, dshFacadeTargetInput{Config: config, Store: store, Sandbox: sandbox, ProviderID: providerID, Model: modelName})
	if err != nil {
		return nil, err
	}
	facadeBaseURL := strings.TrimRight(baseURL, "/") + "/api/runtime/sandboxes/" + sandbox.Summary.ID + "/llm/openai/v1"
	tokenValue, token, err := NewFacadeToken(sandbox.Summary.ID, target.Model.Name, target.Provider.ID, APIProtocolChatCompletions, req.Source, req.RunID)
	if err != nil {
		return nil, err
	}
	if err := store.SaveLLMFacadeToken(ctx, token); err != nil {
		return nil, err
	}

	return map[string]string{
		"AGENT_COMPOSE_SANDBOX_TOKEN": tokenValue,
		"LLM_API_ENDPOINT":            facadeBaseURL,
		"LLM_API_KEY":                 tokenValue,
		"LLM_API_PROTOCOL":            APIProtocolChatCompletions,
		"DSH_MODEL":                   target.Model.Name,
		"DSH_PERMISSION_MODE":         "danger-full-access",
	}, nil
}

// dshFacadeTargetInput groups resolveDshFacadeTarget's inputs.
type dshFacadeTargetInput struct {
	Config     *appconfig.Config
	Store      DshFacadeStore
	Sandbox    *domain.Sandbox
	ProviderID string
	Model      string
}

// resolveDshFacadeTarget mirrors resolvePiFacadeTarget's branch structure
// (configured provider id -> family -> custom OpenAI), but DSH has no
// Anthropic-family route: llm-deepseek always speaks chat completions, so
// there is no Anthropic branch to mirror.
func resolveDshFacadeTarget(ctx context.Context, in dshFacadeTargetInput) (ResolvedTarget, error) {
	config, store, sandbox, providerID, model := in.Config, in.Store, in.Sandbox, in.ProviderID, in.Model
	sandboxID := sandbox.Summary.ID
	envItems, err := SandboxProviderEnvItems(ctx, store, sandbox, "")
	if err != nil {
		return ResolvedTarget{}, err
	}
	if HasSessionEnvProviderInput(envItems) {
		providerID, err := ensureSessionOpenAIEnvProviderWithConfig(ctx, store, SessionEnvProviderQuery{Config: config, SessionID: sandboxID, RequestedModel: model, EnvItems: envItems})
		if err != nil {
			return ResolvedTarget{}, err
		}
		return ResolveRuntimeLLMTargetWithEnv(ctx, config, store, sandboxID, ProviderFamilyOpenAI, model, providerID, envItems)
	}
	if HasEnabledLLMProviderID(ctx, store, providerID) {
		return ResolveRuntimeLLMTargetWithEnv(ctx, config, store, sandboxID, "", model, providerID, envItems)
	}
	switch providerID {
	case ProviderFamilyOpenAI, ProviderIDDefaultOpenAI:
		return ResolveRuntimeLLMTargetWithEnv(ctx, config, store, sandboxID, ProviderFamilyOpenAI, model, "", envItems)
	default:
		return resolveCustomOpenAIFacadeTarget(ctx, customOpenAIFacadeTargetRequest{
			Config:     config,
			Store:      store,
			Sandbox:    sandbox,
			ProviderID: providerID,
			Model:      model,
		})
	}
}
