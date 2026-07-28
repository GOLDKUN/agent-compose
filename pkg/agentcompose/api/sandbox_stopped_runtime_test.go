package api

import (
	"testing"
	"time"

	domain "agent-compose/pkg/model"
)

func TestSandboxToV2IncludesStoppedRuntimeLifecycle(t *testing.T) {
	releasedAt := time.Now().UTC().Truncate(time.Second)
	got := sandboxToV2(&domain.Sandbox{
		Summary:              domain.SandboxSummary{ID: "sandbox", VMStatus: domain.VMStatusStopped},
		StoppedRuntimePolicy: domain.StoppedRuntimePolicyRemove,
		StoppedRuntime: &domain.StoppedRuntime{
			State: domain.StoppedRuntimeStateReleased, ReleasedAt: releasedAt,
		},
	})
	if got.GetStoppedRuntimePolicy() != domain.StoppedRuntimePolicyRemove ||
		got.GetStoppedRuntimeState() != domain.StoppedRuntimeStateReleased ||
		!got.GetStoppedRuntimeReleasedAt().AsTime().Equal(releasedAt) {
		t.Fatalf("sandbox proto = %#v", got)
	}
}

func TestSandboxToV2DefaultsLegacyRuntimeToRetained(t *testing.T) {
	got := sandboxToV2(&domain.Sandbox{Summary: domain.SandboxSummary{ID: "sandbox"}})
	if got.GetStoppedRuntimePolicy() != domain.StoppedRuntimePolicyRetain || got.GetStoppedRuntimeState() != domain.StoppedRuntimeStateRetained {
		t.Fatalf("legacy sandbox proto policy/state = %q/%q", got.GetStoppedRuntimePolicy(), got.GetStoppedRuntimeState())
	}
}
