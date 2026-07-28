package adapters

import (
	"testing"

	domain "agent-compose/pkg/model"
)

func TestStoppedRuntimePolicyFromAgentDefinition(t *testing.T) {
	for _, tt := range []struct {
		name       string
		definition *domain.AgentDefinition
		want       string
	}{
		{name: "missing", want: domain.StoppedRuntimePolicyRemove},
		{name: "default", definition: &domain.AgentDefinition{ConfigJSON: `{}`}, want: domain.StoppedRuntimePolicyRemove},
		{name: "empty policy", definition: &domain.AgentDefinition{ConfigJSON: `{"sandbox":{}}`}, want: domain.StoppedRuntimePolicyRemove},
		{name: "retain", definition: &domain.AgentDefinition{ConfigJSON: `{"sandbox":{"stopped_runtime_policy":"retain"}}`}, want: domain.StoppedRuntimePolicyRetain},
		{name: "remove", definition: &domain.AgentDefinition{ConfigJSON: `{"sandbox":{"stopped_runtime_policy":"remove"}}`}, want: domain.StoppedRuntimePolicyRemove},
		{name: "invalid json", definition: &domain.AgentDefinition{ConfigJSON: `{`}, want: domain.StoppedRuntimePolicyRetain},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := stoppedRuntimePolicyFromAgentDefinition(tt.definition); got != tt.want {
				t.Fatalf("policy = %q, want %q", got, tt.want)
			}
		})
	}
}
