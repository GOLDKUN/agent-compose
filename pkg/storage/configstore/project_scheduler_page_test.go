package configstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
)

func TestProjectSchedulerPageUsesStableCursorAndProjectQuery(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	for _, project := range []domain.ProjectRecord{{ID: "project-a", Name: "alpha"}, {ID: "project-b", Name: "beta"}} {
		if _, err := store.UpsertProject(ctx, project); err != nil {
			t.Fatalf("upsert project %s: %v", project.ID, err)
		}
	}
	for _, scheduler := range []domain.ProjectSchedulerRecord{
		{ProjectID: "project-a", AgentName: "agent-a", ID: "scheduler-record-a", Enabled: true},
		{ProjectID: "project-a", AgentName: "agent-b", ID: "scheduler-record-b", Enabled: true},
		{ProjectID: "project-b", AgentName: "agent-c", ID: "scheduler-record-c", Enabled: true},
	} {
		agentID, err := domain.StableProjectAgentID(scheduler.ProjectID, scheduler.AgentName)
		if err != nil {
			t.Fatalf("derive agent id %s: %v", scheduler.AgentName, err)
		}
		if _, err := store.UpsertProjectAgent(ctx, domain.ProjectAgentRecord{ID: agentID, ProjectID: scheduler.ProjectID, AgentName: scheduler.AgentName}); err != nil {
			t.Fatalf("upsert agent %s: %v", scheduler.AgentName, err)
		}
		if _, err := store.UpsertProjectScheduler(ctx, scheduler); err != nil {
			t.Fatalf("upsert scheduler %s: %v", scheduler.SchedulerID, err)
		}
	}
	// The retained scheduler_id column is migration-only state. Deliberately
	// diverge it to prove current reads, writes, ordering, and cursors use id.
	if _, err := store.db.ExecContext(ctx, `UPDATE project_scheduler SET scheduler_id = ? WHERE id = ?`, "legacy-alias-a", "scheduler-record-a"); err != nil {
		t.Fatalf("diverge compatibility scheduler id: %v", err)
	}
	loaded, err := store.GetProjectScheduler(ctx, "project-a", "scheduler-record-a")
	if err != nil || loaded.ID != "scheduler-record-a" || loaded.SchedulerID != loaded.ID {
		t.Fatalf("get scheduler by native id = %#v, err = %v", loaded, err)
	}
	if _, err := store.GetProjectScheduler(ctx, "project-a", "legacy-alias-a"); err == nil {
		t.Fatal("get scheduler unexpectedly used compatibility scheduler_id")
	}
	if loaded, err = store.SetProjectSchedulerEnabled(ctx, "project-a", "scheduler-record-a", false); err != nil || loaded.Enabled {
		t.Fatalf("disable scheduler by native id = %#v, err = %v", loaded, err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE project_scheduler SET last_error = ? WHERE id = ?`, "last failure", "scheduler-record-a"); err != nil {
		t.Fatalf("update scheduler error: %v", err)
	}
	for index, startedAt := range []int64{1700000000, 1700000060} {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO scheduler_run(scheduler_id, run_id, trigger_id, started_at) VALUES(?, ?, ?, ?)`, "scheduler-record-a", fmt.Sprintf("run-%d", index), "trigger-1", startedAt); err != nil {
			t.Fatalf("insert scheduler run: %v", err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO scheduler_run(scheduler_id, run_id, started_at) VALUES(?, ?, ?)`, "scheduler-record-a", "old-invocation", int64(1700000120)); err != nil {
		t.Fatalf("insert old invocation: %v", err)
	}

	first, err := store.ListProjectSchedulersPage(ctx, "", 0, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %#v, err = %v", first, err)
	}
	if first[0].RunCount != 2 || first[0].LastError != "last failure" || !first[0].LatestRunAt.Equal(time.Unix(1700000060, 0).UTC()) {
		t.Fatalf("scheduler summary = %#v", first[0])
	}
	second, err := store.ListProjectSchedulersPage(ctx, "", 2, 2)
	if err != nil || len(second) != 1 || second[0].ProjectID != "project-b" {
		t.Fatalf("second page = %#v, err = %v", second, err)
	}
	filtered, err := store.ListProjectSchedulersPage(ctx, "alpha", 0, 10)
	if err != nil || len(filtered) != 2 {
		t.Fatalf("filtered page = %#v, err = %v", filtered, err)
	}
}
