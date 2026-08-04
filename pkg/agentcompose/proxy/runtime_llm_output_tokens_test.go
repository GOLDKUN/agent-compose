package proxy

import (
	"encoding/json"
	"testing"

	protocolbridge "github.com/chaitin/ai-api-protocol-bridge"
)

func TestInjectMaxOutputTokensUsesUpstreamProtocolFields(t *testing.T) {
	tests := []struct {
		name       string
		protocol   protocolbridge.Protocol
		wantFields map[string]int
		omitFields []string
	}{
		{
			name:     "OpenAI Responses",
			protocol: protocolbridge.ProtocolOpenAIResponses,
			wantFields: map[string]int{
				"max_output_tokens": 65536,
			},
			omitFields: []string{"max_tokens", "max_completion_tokens"},
		},
		{
			name:     "OpenAI Chat Completions",
			protocol: protocolbridge.ProtocolOpenAIChat,
			wantFields: map[string]int{
				"max_tokens":            65536,
				"max_completion_tokens": 65536,
			},
			omitFields: []string{"max_output_tokens"},
		},
		{
			name:     "Anthropic Messages",
			protocol: protocolbridge.ProtocolAnthropicMessages,
			wantFields: map[string]int{
				"max_tokens": 65536,
			},
			omitFields: []string{"max_output_tokens", "max_completion_tokens"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := injectMaxOutputTokens([]byte(`{"model":"test-model"}`), tc.protocol, 65536)
			var request map[string]json.RawMessage
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatalf("decode injected request body: %v", err)
			}
			for field, want := range tc.wantFields {
				var got int
				if err := json.Unmarshal(request[field], &got); err != nil {
					t.Fatalf("decode %s: %v", field, err)
				}
				if got != want {
					t.Errorf("%s = %d, want %d", field, got, want)
				}
			}
			for _, field := range tc.omitFields {
				if _, ok := request[field]; ok {
					t.Errorf("request unexpectedly contains %s: %s", field, body)
				}
			}
		})
	}
}

func TestInjectMaxOutputTokensDisabledPreservesBody(t *testing.T) {
	body := []byte(`{"model":"test-model"}`)
	got := injectMaxOutputTokens(body, protocolbridge.ProtocolAnthropicMessages, 0)
	if string(got) != string(body) {
		t.Fatalf("body = %s, want %s", got, body)
	}
}
