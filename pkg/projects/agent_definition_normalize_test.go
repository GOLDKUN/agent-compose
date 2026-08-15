package projects

import (
	"testing"

	domain "agent-compose/pkg/model"
)

func TestNormalizeAgentDefinitionAcceptsPiAndRejectsUnknownProvider(t *testing.T) {
	definition := domain.AgentDefinition{ID: "pi-agent", Name: "reviewer", Provider: "pi-agent", Model: " openai/gpt-5.4 ", ProjectID: "project-1", AgentName: "reviewer"}
	normalized, err := NormalizeAgentDefinition(definition, false)
	if err != nil {
		t.Fatalf("NormalizeAgentDefinition returned error: %v", err)
	}
	if normalized.Provider != "pi" || normalized.Model != "openai/gpt-5.4" {
		t.Fatalf("normalized definition = %#v", normalized)
	}

	definition.Provider = "unknown"
	if _, err := NormalizeAgentDefinition(definition, false); err == nil {
		t.Fatal("NormalizeAgentDefinition accepted an unknown provider")
	}
}
