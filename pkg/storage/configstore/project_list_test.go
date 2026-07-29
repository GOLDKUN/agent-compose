package configstore

import (
	"context"
	"fmt"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestListProjectsFiltersPagesAndCountsCurrentRevision(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	seedProjectListProject(t, store, "project-alpha", "Alpha NEEDLE", "/work/alpha", 2, 300, 0)
	seedProjectListProject(t, store, "project-beta", "Beta", "/work/needle-beta", 1, 200, 0)
	seedProjectListProject(t, store, "project-needle-removed", "Removed", "/work/removed", 1, 400, 350)
	seedProjectListResources(t, store, "project-alpha", 1, "old", true)
	seedProjectListResources(t, store, "project-alpha", 2, "current", true)
	seedProjectListResources(t, store, "project-beta", 1, "current", false)

	result, err := store.ListProjects(ctx, domain.ProjectListOptions{Query: "NeEdLe", Limit: 1})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if result.TotalCount != 2 || len(result.Projects) != 1 || result.Projects[0].ID != "project-alpha" {
		t.Fatalf("first page = %#v", result)
	}
	if !result.HasMore || result.NextOffset != 1 {
		t.Fatalf("first page state = has_more %v next %d", result.HasMore, result.NextOffset)
	}
	if counts := result.CountsByProjectID["project-alpha"]; counts.AgentCount != 1 || counts.SchedulerCount != 1 {
		t.Fatalf("current revision counts = %#v", counts)
	}

	second, err := store.ListProjects(ctx, domain.ProjectListOptions{Query: "needle", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("ListProjects second page: %v", err)
	}
	if second.TotalCount != 2 || len(second.Projects) != 1 || second.Projects[0].ID != "project-beta" || second.HasMore || second.NextOffset != 2 {
		t.Fatalf("second page = %#v", second)
	}
	if counts := second.CountsByProjectID["project-beta"]; counts.AgentCount != 1 || counts.SchedulerCount != 0 {
		t.Fatalf("second project counts = %#v", counts)
	}

	withRemoved, err := store.ListProjects(ctx, domain.ProjectListOptions{Query: "needle", IncludeRemoved: true, Limit: 10})
	if err != nil {
		t.Fatalf("ListProjects including removed: %v", err)
	}
	if withRemoved.TotalCount != 3 || len(withRemoved.Projects) != 3 || withRemoved.Projects[0].ID != "project-needle-removed" {
		t.Fatalf("page including removed = %#v", withRemoved)
	}

	beyond, err := store.ListProjects(ctx, domain.ProjectListOptions{Query: "needle", Limit: 10, Offset: 20})
	if err != nil {
		t.Fatalf("ListProjects beyond total: %v", err)
	}
	if beyond.TotalCount != 2 || len(beyond.Projects) != 0 || beyond.HasMore || beyond.NextOffset != 2 {
		t.Fatalf("page beyond total = %#v", beyond)
	}
}

func TestListProjectsTreatsSearchMetacharactersLiterally(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	seedProjectListProject(t, store, "project-percent", "100% Ready", "/work/percent", 0, 100, 0)
	seedProjectListProject(t, store, "project-underscore", "Under_score", "/work/underscore", 0, 90, 0)
	seedProjectListProject(t, store, "project-plain", "Plain", "/work/plain", 0, 80, 0)

	for _, test := range []struct {
		query string
		want  string
	}{
		{query: "%", want: "project-percent"},
		{query: "_", want: "project-underscore"},
		{query: "PROJECT-PLAIN", want: "project-plain"},
	} {
		result, err := store.ListProjects(ctx, domain.ProjectListOptions{Query: test.query, Limit: 10})
		if err != nil {
			t.Fatalf("ListProjects query %q: %v", test.query, err)
		}
		if result.TotalCount != 1 || len(result.Projects) != 1 || result.Projects[0].ID != test.want {
			t.Fatalf("ListProjects query %q = %#v", test.query, result)
		}
	}
}

func TestListProjectsReturnsMoreThanLegacyTwoHundredLimit(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	for index := range 205 {
		id := fmt.Sprintf("project-%03d", index)
		seedProjectListProject(t, store, id, id, "/work/"+id, 0, int64(1000-index), 0)
	}

	result, err := store.ListProjects(ctx, domain.ProjectListOptions{Limit: 500, Offset: -10})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if result.TotalCount != 205 || len(result.Projects) != 205 || result.HasMore || result.NextOffset != 205 {
		t.Fatalf("large page = total %d projects %d has_more %v next %d", result.TotalCount, len(result.Projects), result.HasMore, result.NextOffset)
	}
}

func seedProjectListProject(t *testing.T, store *ConfigStore, id, name, sourcePath string, revision, updatedAt, removedAt int64) {
	t.Helper()
	_, err := store.db.Exec(`INSERT INTO project(
		id, name, short_id, source_path, source_json, current_revision, spec_hash, created_at, updated_at, removed_at
	) VALUES(?, ?, ?, ?, '{}', ?, '', ?, ?, ?)`, id, name, id, sourcePath, revision, updatedAt, updatedAt, removedAt)
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

func seedProjectListResources(t *testing.T, store *ConfigStore, projectID string, revision int64, suffix string, scheduler bool) {
	t.Helper()
	agentName := "agent-" + suffix
	agentID := projectID + "-" + suffix
	if _, err := store.db.Exec(`INSERT INTO project_agent(
		id, name, short_id, project_id, agent_name, revision, spec_json, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, '{}', 1, 1)`, agentID, agentName, agentID, projectID, agentName, revision); err != nil {
		t.Fatalf("seed project agent %s: %v", agentID, err)
	}
	if !scheduler {
		return
	}
	schedulerID := projectID + "-scheduler-" + suffix
	if _, err := store.db.Exec(`INSERT INTO project_scheduler(
		id, short_id, project_id, scheduler_id, agent_name, revision, spec_json, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, '{}', 1, 1)`, schedulerID, schedulerID, projectID, schedulerID, agentName, revision); err != nil {
		t.Fatalf("seed project scheduler %s: %v", schedulerID, err)
	}
}
