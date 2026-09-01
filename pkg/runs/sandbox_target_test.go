package runs

import (
	"context"
	"testing"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestSandboxRunTargetResolverResolveBatch(t *testing.T) {
	store := &sandboxRunTargetStoreStub{
		runs: map[string]domain.ProjectRunRecord{
			"run-sandbox": {SandboxID: "run-sandbox", ProjectID: "run-project", AgentName: "run-agent"},
		},
		managedAgents: map[string]domain.ProjectAgentRecord{
			"managed-id": {ProjectID: "managed-project", AgentName: "managed-agent", ID: "managed-id"},
		},
	}
	resolver, err := NewSandboxRunTargetResolver(store)
	if err != nil {
		t.Fatalf("NewSandboxRunTargetResolver returned error: %v", err)
	}
	sandboxes := []*domain.Sandbox{
		sandboxWithTags("run-sandbox", domain.SandboxTag{Name: "project", Value: "ignored-project"}),
		sandboxWithTags("tag-sandbox", domain.SandboxTag{Name: "project", Value: "tag-project"}, domain.SandboxTag{Name: "agent", Value: "tag-agent"}),
		sandboxWithTags("legacy-tag-sandbox", domain.SandboxTag{Name: "project_id", Value: "legacy-tag-project"}, domain.SandboxTag{Name: "agent", Value: "legacy-tag-agent"}),
		sandboxWithTags("managed-sandbox", domain.SandboxTag{Name: domain.AgentSandboxTagID, Value: "managed-id"}),
		sandboxWithTags("unknown-sandbox", domain.SandboxTag{Name: domain.AgentSandboxTagID, Value: "unknown"}),
	}

	targets, err := resolver.ResolveBatch(context.Background(), sandboxes)
	if err != nil {
		t.Fatalf("ResolveBatch returned error: %v", err)
	}
	wants := map[string]SandboxRunTarget{
		"run-sandbox":        {ProjectID: "run-project", AgentName: "run-agent"},
		"tag-sandbox":        {ProjectID: "tag-project", AgentName: "tag-agent"},
		"legacy-tag-sandbox": {ProjectID: "legacy-tag-project", AgentName: "legacy-tag-agent"},
		"managed-sandbox":    {ProjectID: "managed-project", AgentName: "managed-agent"},
	}
	for sandboxID, want := range wants {
		if got := targets[sandboxID]; got != want {
			t.Errorf("target %s = %#v, want %#v", sandboxID, got, want)
		}
	}
	if _, exists := targets["unknown-sandbox"]; exists {
		t.Fatalf("unknown sandbox unexpectedly resolved: %#v", targets["unknown-sandbox"])
	}
	if store.runCalls != 1 || store.managedCalls != 1 {
		t.Fatalf("store calls = runs:%d managed:%d", store.runCalls, store.managedCalls)
	}
}

func sandboxWithTags(id string, tags ...domain.SandboxTag) *domain.Sandbox {
	return &domain.Sandbox{Summary: domain.SandboxSummary{ID: id, Tags: tags}}
}

type sandboxRunTargetStoreStub struct {
	runs          map[string]domain.ProjectRunRecord
	managedAgents map[string]domain.ProjectAgentRecord
	runCalls      int
	managedCalls  int
}

func (s *sandboxRunTargetStoreStub) ListLatestProjectRunsForSandboxes(context.Context, []string) (map[string]domain.ProjectRunRecord, error) {
	s.runCalls++
	return s.runs, nil
}

func (s *sandboxRunTargetStoreStub) ListProjectAgentsByIDs(context.Context, []string) (map[string]domain.ProjectAgentRecord, error) {
	s.managedCalls++
	return s.managedAgents, nil
}
