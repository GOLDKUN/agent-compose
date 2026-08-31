package projects_test

import (
	"path/filepath"
	"testing"

	"agent-compose/internal/projects"
	"agent-compose/pkg/identity"
	domain "agent-compose/pkg/model"
)

func TestProjectStableIDHelpers(t *testing.T) {
	projectID, err := projects.StableProjectID("demo", filepath.Join("tmp", "agent-compose.yml"))
	if err != nil {
		t.Fatalf("projects.StableProjectID returned error: %v", err)
	}
	sameProjectID, err := projects.StableProjectID("demo", filepath.Join("tmp", "agent-compose.yml"))
	if err != nil {
		t.Fatalf("second projects.StableProjectID returned error: %v", err)
	}
	if sameProjectID != projectID {
		t.Fatalf("project id changed: %q != %q", sameProjectID, projectID)
	}
	otherProjectID, err := projects.StableProjectID("demo", filepath.Join("other", "agent-compose.yml"))
	if err != nil {
		t.Fatalf("other projects.StableProjectID returned error: %v", err)
	}
	if otherProjectID != projectID {
		t.Fatalf("project id changed with source path: %q != %q", otherProjectID, projectID)
	}

	agentID, err := projects.StableProjectAgentID(projectID, "reviewer")
	if err != nil {
		t.Fatalf("projects.StableProjectAgentID returned error: %v", err)
	}
	if again, err := projects.StableProjectAgentID(projectID, "reviewer"); err != nil || again != agentID {
		t.Fatalf("stable agent id = %q, %v; want %q", again, err, agentID)
	}
	schedulerID, err := projects.StableProjectSchedulerID(projectID, "reviewer", "")
	if err != nil {
		t.Fatalf("projects.StableProjectSchedulerID returned error: %v", err)
	}
	sameSchedulerID, err := projects.StableProjectSchedulerID(projectID, "reviewer", "")
	if err != nil {
		t.Fatalf("projects.StableProjectSchedulerID returned error: %v", err)
	}
	if sameSchedulerID != schedulerID {
		t.Fatalf("stable scheduler id = %q, want %q", sameSchedulerID, schedulerID)
	}
	runID, err := projects.StableProjectRunID(projectID, "reviewer", domain.ProjectRunSourceManual, "client-request-1")
	if err != nil {
		t.Fatalf("projects.StableProjectRunID returned error: %v", err)
	}
	otherRunID, err := projects.StableProjectRunID(projectID, "reviewer", domain.ProjectRunSourceManual, "client-request-2")
	if err != nil {
		t.Fatalf("other projects.StableProjectRunID returned error: %v", err)
	}
	for label, id := range map[string]string{
		"project":   projectID,
		"agent":     agentID,
		"scheduler": schedulerID,
		"run":       runID,
	} {
		if !identity.IsID(id) {
			t.Fatalf("%s id = %q, want sha256 id", label, id)
		}
		if shortID := identity.ShortID(id); !identity.IsShortID(shortID) {
			t.Fatalf("%s short id = %q, want 12-char hex short id", label, shortID)
		}
	}
	if otherRunID == runID {
		t.Fatalf("run id did not include idempotency key: %q", runID)
	}
	if _, err := projects.StableProjectID("Demo", ""); err == nil {
		t.Fatalf("projects.StableProjectID accepted non-normalized project name")
	}
	if _, err := projects.StableProjectID("1demo", ""); err != nil {
		t.Fatalf("projects.StableProjectID rejected digit-prefixed project name: %v", err)
	}
	if _, err := projects.StableProjectAgentID(projectID, "Bad Agent"); err == nil {
		t.Fatalf("projects.StableProjectAgentID accepted non-normalized agent name")
	}
}
