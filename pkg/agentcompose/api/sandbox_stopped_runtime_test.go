package api

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	domain "github.com/chaitin/agent-compose/pkg/model"
	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
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

func TestStopSandboxRetriesPendingRuntimeRelease(t *testing.T) {
	const sandboxID = "28fed243-4d9d-4e56-96cf-8b2baa8643c8"
	delegate := &characterizationSessionDelegate{}
	store := &characterizationSandboxStore{session: &domain.Sandbox{
		Summary:              domain.SandboxSummary{ID: sandboxID, VMStatus: domain.VMStatusStopped},
		StoppedRuntimePolicy: domain.StoppedRuntimePolicyRemove,
		StoppedRuntime:       &domain.StoppedRuntime{State: domain.StoppedRuntimeStateReleasePending},
	}}
	handler := NewSandboxHandler(SandboxHandlerDeps{
		Delegate:  delegate,
		Store:     store,
		Remover:   nil,
		Dashboard: nil,
	})

	if _, err := handler.StopSandbox(context.Background(), connect.NewRequest(&agentcomposev2.StopSandboxRequest{SandboxId: sandboxID})); err != nil {
		t.Fatalf("StopSandbox release retry returned error: %v", err)
	}
	if len(delegate.stopSessionIDs) != 1 || delegate.stopSessionIDs[0] != sandboxID {
		t.Fatalf("release retry delegate calls = %#v", delegate.stopSessionIDs)
	}
}

func TestStopSandboxDoesNotReleaseUnexpectedRuntimeLoss(t *testing.T) {
	const sandboxID = "28fed243-4d9d-4e56-96cf-8b2baa8643c8"
	delegate := &characterizationSessionDelegate{}
	store := &characterizationSandboxStore{session: &domain.Sandbox{
		Summary:              domain.SandboxSummary{ID: sandboxID, VMStatus: domain.VMStatusStopped},
		StoppedRuntimePolicy: domain.StoppedRuntimePolicyRemove,
	}}
	handler := NewSandboxHandler(SandboxHandlerDeps{
		Delegate:  delegate,
		Store:     store,
		Remover:   nil,
		Dashboard: nil,
	})

	_, err := handler.StopSandbox(context.Background(), connect.NewRequest(&agentcomposev2.StopSandboxRequest{SandboxId: sandboxID}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("StopSandbox unexpected loss code = %v, error = %v", connect.CodeOf(err), err)
	}
	if len(delegate.stopSessionIDs) != 0 {
		t.Fatalf("unexpected loss reached delegate: %#v", delegate.stopSessionIDs)
	}
}
