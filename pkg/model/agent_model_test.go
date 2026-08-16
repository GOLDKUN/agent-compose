package model

import (
	"encoding/json"
	"testing"
)

func TestNormalizeAgentKindPiAliases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "pi", want: "pi"},
		{input: " pi-agent ", want: "pi"},
		{input: "PI_AGENT", want: "pi"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			if got := NormalizeAgentKind(test.input); got != test.want {
				t.Fatalf("NormalizeAgentKind(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNormalizeAgentDefinitionAcceptsPiAndRejectsUnknownProvider(t *testing.T) {
	definition := AgentDefinition{ID: "pi-agent", Name: "reviewer", Provider: "pi-agent", Model: " openai/gpt-5.4 ", ProjectID: "project-1", AgentName: "reviewer"}
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

func TestNormalizeAgentKindDshAliases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "dsh", want: "dsh"},
		{input: " deepseek ", want: "dsh"},
		{input: "DEEPSEEK-HARNESS", want: "dsh"},
		{input: "deepseek_harness", want: "dsh"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			if got := NormalizeAgentKind(test.input); got != test.want {
				t.Fatalf("NormalizeAgentKind(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNormalizeAgentDefinitionAcceptsDsh(t *testing.T) {
	definition := AgentDefinition{ID: "dsh-agent", Name: "reviewer", Provider: "deepseek-harness", Model: " deepseek-official/deepseek-v4-flash ", ProjectID: "project-1", AgentName: "reviewer"}
	normalized, err := NormalizeAgentDefinition(definition, false)
	if err != nil {
		t.Fatalf("NormalizeAgentDefinition returned error: %v", err)
	}
	if normalized.Provider != "dsh" || normalized.Model != "deepseek-official/deepseek-v4-flash" {
		t.Fatalf("normalized definition = %#v", normalized)
	}
}

func TestProjectOwnershipJSONKeepsHistoricalFieldNames(t *testing.T) {
	payload := struct {
		Agent     AgentDefinition  `json:"agent"`
		Scheduler SchedulerSummary `json:"scheduler"`
	}{
		Agent: AgentDefinition{ProjectID: "project-1", ProjectRevision: 2, AgentName: "reviewer"},
		Scheduler: SchedulerSummary{
			ProjectID:          "project-1",
			ProjectRevision:    2,
			AgentName:          "reviewer",
			ProjectSchedulerID: "nightly",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal project ownership: %v", err)
	}
	var decoded map[string]map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode project ownership: %v", err)
	}
	assertHistoricalProjectOwnershipKeys(t, decoded["agent"], false)
	assertHistoricalProjectOwnershipKeys(t, decoded["scheduler"], true)
}

func assertHistoricalProjectOwnershipKeys(t *testing.T, payload map[string]any, scheduler bool) {
	t.Helper()
	for _, key := range []string{"managed_project_id", "managed_project_revision", "managed_agent_name"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("historical key %q missing from %#v", key, payload)
		}
	}
	if scheduler {
		if _, ok := payload["managed_scheduler_id"]; !ok {
			t.Fatalf("historical scheduler key missing from %#v", payload)
		}
	}
	for _, key := range []string{"project_id", "project_revision", "agent_name", "project_scheduler_id"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("internal key %q leaked into %#v", key, payload)
		}
	}
}
