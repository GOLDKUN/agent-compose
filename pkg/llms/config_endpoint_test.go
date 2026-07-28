package llms

import "testing"

func TestNormalizeAPIEndpointForProtocolCustomV1Prefix(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		want     string
	}{
		{
			name:     "chat completions",
			protocol: APIProtocolChatCompletions,
			want:     "https://api.example.test/qwen3-14b/v1/chat/completions",
		},
		{
			name:     "responses",
			protocol: APIProtocolResponses,
			want:     "https://api.example.test/qwen3-14b/v1/responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeAPIEndpointForProtocol("https://api.example.test/qwen3-14b/v1", tt.protocol)
			if got != tt.want {
				t.Fatalf("NormalizeAPIEndpointForProtocol() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEndpointForEnvDefaultProviderCustomV1Prefix(t *testing.T) {
	got := EndpointForProvider(Provider{
		ProviderType: ProviderFamilyOpenAI,
		BaseURL:      "https://api.example.test/qwen3-14b/v1/chat/completions",
		Scope:        ProviderScopeEnvDefault,
	}, APIProtocolChatCompletions)
	want := "https://api.example.test/qwen3-14b/v1/chat/completions"
	if got != want {
		t.Fatalf("EndpointForProvider() = %q, want %q", got, want)
	}
}
