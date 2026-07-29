package compose

import (
	"strings"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestParseAgentStoppedRuntimePolicy(t *testing.T) {
	parsed, err := Parse([]byte("agents:\n  worker:\n    sandbox:\n      stopped_runtime_policy: remove\n"))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Agents["worker"].Sandbox == nil || parsed.Agents["worker"].Sandbox.StoppedRuntimePolicy != domain.StoppedRuntimePolicyRemove {
		t.Fatalf("parsed sandbox = %#v", parsed.Agents["worker"].Sandbox)
	}
}

func TestNormalizeAgentStoppedRuntimePolicy(t *testing.T) {
	for _, tt := range []struct {
		name       string
		sandbox    *SandboxSpec
		wantPolicy string
		wantNil    bool
	}{
		{name: "missing", wantNil: true},
		{name: "default", sandbox: &SandboxSpec{}, wantPolicy: domain.StoppedRuntimePolicyRemove},
		{name: "remove", sandbox: &SandboxSpec{StoppedRuntimePolicy: " remove "}, wantPolicy: domain.StoppedRuntimePolicyRemove},
	} {
		t.Run(tt.name, func(t *testing.T) {
			normalized, err := Normalize(&ProjectSpec{Name: "project", Agents: map[string]AgentSpec{"worker": {Sandbox: tt.sandbox}}}, NormalizeOptions{})
			if err != nil {
				t.Fatalf("Normalize returned error: %v", err)
			}
			got := normalized.Agents[0].Sandbox
			if tt.wantNil {
				if got != nil {
					t.Fatalf("sandbox = %#v, want nil", got)
				}
				return
			}
			if got == nil || got.StoppedRuntimePolicy != tt.wantPolicy {
				t.Fatalf("sandbox = %#v, want policy %q", got, tt.wantPolicy)
			}
		})
	}
}

func TestNormalizeAgentRejectsUnknownStoppedRuntimePolicy(t *testing.T) {
	_, err := Normalize(&ProjectSpec{Name: "project", Agents: map[string]AgentSpec{"worker": {Sandbox: &SandboxSpec{StoppedRuntimePolicy: "prune"}}}}, NormalizeOptions{})
	if err == nil || !strings.Contains(err.Error(), "agents.worker.sandbox.stopped_runtime_policy") {
		t.Fatalf("Normalize error = %v, want stopped runtime policy path", err)
	}
}
