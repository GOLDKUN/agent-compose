package runs

import (
	"testing"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestSandboxOptionsFromAgentSpecSnapshotsStoppedRuntimePolicy(t *testing.T) {
	options := sandboxOptionsFromAgentSpec(&agentcomposev2.AgentSpec{
		Sandbox: &agentcomposev2.SandboxSpec{StoppedRuntimePolicy: domain.StoppedRuntimePolicyRemove},
	})
	if options.StoppedRuntimePolicy != domain.StoppedRuntimePolicyRemove {
		t.Fatalf("stopped runtime policy = %q, want remove", options.StoppedRuntimePolicy)
	}
}
