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
	return c.GenerateWithEnv(ctx, prompt, model, outputSchemaJSON, "", nil)
}

func (c *LLMClient) GenerateWithEnv(ctx context.Context, prompt, model, outputSchemaJSON, scopeID string, envItems []domain.SandboxEnvVar) (llms.GenerateResult, error) {
	if c == nil {
		return llms.GenerateResult{}, fmt.Errorf("llm client is unavailable")
	}
	target, err := llms.ResolveRuntimeLLMTargetWithEnv(ctx, c.config, c.store, scopeID, "", model, "", envItems)
	if err != nil {
		return llms.GenerateResult{}, err
	}
	return llms.Generate(ctx, c.client, llms.GenerateRequest{
		Endpoint:         target.Endpoint,
		Protocol:         target.WireAPI,
		Prompt:           prompt,
		Model:            firstNonEmpty(target.Model.ID, target.Model.Name),
		OutputSchemaJSON: outputSchemaJSON,
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
