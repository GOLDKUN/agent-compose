package sandboxes

import (
	"testing"

	domain "agent-compose/pkg/model"
)

func TestLegacySandboxDefaultsToRetainedRuntime(t *testing.T) {
	for _, sandbox := range []*domain.Sandbox{nil, {}, {StoppedRuntimePolicy: " "}, {StoppedRuntimePolicy: "invalid"}} {
		if policy := EffectiveStoppedRuntimePolicy(sandbox); policy != domain.StoppedRuntimePolicyRetain {
			t.Fatalf("legacy policy = %q, want retain", policy)
		}
	}
	sandbox := &domain.Sandbox{}
	if state := EffectiveStoppedRuntimeState(sandbox); state != domain.StoppedRuntimeStateRetained {
		t.Fatalf("legacy state = %q, want retained", state)
	}
}
