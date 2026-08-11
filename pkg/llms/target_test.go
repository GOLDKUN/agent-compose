package llms

import (
	"context"
	"testing"
)

type targetModelConfigStore struct {
	config ProviderModelConfig
	found  bool
}

func (s *targetModelConfigStore) LLMProviderModelConfig(context.Context, string, string) (ProviderModelConfig, bool, error) {
	return s.config, s.found, nil
}

func (s *targetModelConfigStore) LLMProviderModelWireAPI(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

func TestBuildResolvedTargetDropsForbiddenPerModelHeaders(t *testing.T) {
	store := &targetModelConfigStore{
		found: true,
		config: ProviderModelConfig{
			HeadersJSON: `{"Authorization":"Bearer stolen","X-Model-Header":"allowed"}`,
		},
	}
	provider := Provider{
		ID:             "provider-1",
		BaseURL:        "https://provider.test",
		DefaultWireAPI: APIProtocolChatCompletions,
		AuthHeader:     "Authorization",
		AuthScheme:     "Bearer",
		APIKey:         "provider-secret",
	}
	model := Model{ID: "model-1", Name: "model-1"}

	target, err := BuildResolvedTarget(context.Background(), store, provider, model, "")
	if err != nil {
		t.Fatalf("BuildResolvedTarget returned error: %v", err)
	}
	if got := target.Headers.Get("Authorization"); got != "Bearer provider-secret" {
		t.Fatalf("Authorization header = %q, want provider credential to survive unclobbered", got)
	}
	if got := target.Headers.Get("X-Model-Header"); got != "allowed" {
		t.Fatalf("X-Model-Header = %q, want per-model header to pass through", got)
	}
}
