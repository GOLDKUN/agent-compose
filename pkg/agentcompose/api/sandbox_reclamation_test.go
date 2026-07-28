package api

import (
	"testing"
	"time"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestSandboxToV2IncludesWorkspaceReclamation(t *testing.T) {
	started := time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC)
	completed := started.Add(time.Minute)
	got := sandboxToV2(&domain.Sandbox{
		Summary: domain.SandboxSummary{ID: "sandbox-1"},
		WorkspaceReclamation: &domain.SandboxWorkspaceReclamation{
			State: domain.SandboxWorkspaceReclamationStateReclaimed, StartedAt: started, CompletedAt: completed,
		},
	})
	if got.GetWorkspaceReclamationState() != agentcomposev2.WorkspaceReclamationState_WORKSPACE_RECLAMATION_STATE_RECLAIMED ||
		!got.GetWorkspaceReclamationStartedAt().AsTime().Equal(started) ||
		!got.GetWorkspaceReclamationCompletedAt().AsTime().Equal(completed) {
		t.Fatalf("workspace reclamation = %#v", got)
	}
}
