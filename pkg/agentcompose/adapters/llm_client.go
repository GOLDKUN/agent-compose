package adapters

import (
	"context"
	"fmt"
	"net/http"
	"time"

	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/llms"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage/configstore"
)

type LLMClient struct {
	config *appconfig.Config
	store  *configstore.ConfigStore
	client *http.Client
}

func NewLLMClient(config *appconfig.Config, store *configstore.ConfigStore) *LLMClient {
	var timeout time.Duration
	if config != nil {
		timeout = config.LLMTimeout
	}
	return &LLMClient{
		config: config,
		store:  store,
		client: &http.Client{Timeout: timeout},
	}
}

func (c *LLMClient) Generate(ctx context.Context, prompt, model, outputSchemaJSON string) (llms.GenerateResult, error) {
	return c.GenerateWithEnv(ctx, GenerateWithEnvRequest{Prompt: prompt, Model: model, OutputSchemaJSON: outputSchemaJSON})
}

// GenerateWithEnvRequest bundles the prompt/model inputs and scope/env
// context GenerateWithEnv needs to resolve an LLM target and generate.
type GenerateWithEnvRequest struct {
	Prompt           string
	Model            string
	OutputSchemaJSON string
	ScopeID          string
	EnvItems         []domain.SandboxEnvVar
}

func (c *LLMClient) GenerateWithEnv(ctx context.Context, req GenerateWithEnvRequest) (llms.GenerateResult, error) {
	if c == nil {
		return llms.GenerateResult{}, fmt.Errorf("llm client is unavailable")
	}
	target, err := llms.ResolveRuntimeLLMTargetWithEnv(ctx, c.config, c.store, req.ScopeID, "", req.Model, "", req.EnvItems)
	if err != nil {
		return llms.GenerateResult{}, err
	}
	return llms.Generate(ctx, c.client, llms.GenerateRequest{
		Endpoint:         target.Endpoint,
		Protocol:         target.WireAPI,
		Prompt:           req.Prompt,
		Model:            firstNonEmpty(target.Model.ID, target.Model.Name),
		OutputSchemaJSON: req.OutputSchemaJSON,
		Headers:          target.Headers,
		MaxOutputTokens:  firstPositive(target.MaxOutputTokens, configuredMaxOutputTokens(c.config)),
	})
}

func configuredMaxOutputTokens(config *appconfig.Config) int {
	if config == nil {
		return 0
	}
	return config.LLMMaxOutputTokens
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
