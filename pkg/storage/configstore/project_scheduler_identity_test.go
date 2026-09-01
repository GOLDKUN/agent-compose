package configstore

import (
	"context"
	"testing"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestProjectSchedulerRuntimeUsesNativeIDWhenCompatibilityColumnDiverges(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	created, err := upsertNativeTestScheduler(ctx, store, domain.Scheduler{
		Summary: domain.SchedulerSummary{
			ID:                 "native-scheduler",
			ProjectID:          "project-1",
			AgentName:          "worker",
			ProjectSchedulerID: "legacy-input-alias",
			Enabled:            true,
			Runtime:            domain.SchedulerRuntimeScheduler,
		},
		Script: "function main() {}",
	})
	if err != nil {
		t.Fatalf("create native scheduler: %v", err)
	}
	if created.Summary.ID != "native-scheduler" || created.Summary.ProjectSchedulerID != created.Summary.ID {
		t.Fatalf("created scheduler identity = %#v", created.Summary)
	}

	if _, err := store.db.ExecContext(ctx, `UPDATE project_scheduler SET scheduler_id = ? WHERE id = ?`, "stale-compatibility-id", created.Summary.ID); err != nil {
		t.Fatalf("diverge compatibility scheduler id: %v", err)
	}
	loaded, err := store.GetScheduler(ctx, created.Summary.ID)
	if err != nil {
		t.Fatalf("get scheduler by native id: %v", err)
	}
	if loaded.Summary.ID != created.Summary.ID || loaded.Summary.ProjectSchedulerID != created.Summary.ID {
		t.Fatalf("loaded scheduler identity = %#v", loaded.Summary)
	}

	record, err := store.GetProjectScheduler(ctx, "project-1", created.Summary.ID)
	if err != nil {
		t.Fatalf("get project scheduler by native id: %v", err)
	}
	if _, err := store.UpsertProjectScheduler(ctx, record); err != nil {
		t.Fatalf("upsert project scheduler by native id: %v", err)
	}
	var compatibilityID string
	if err := store.db.QueryRowContext(ctx, `SELECT scheduler_id FROM project_scheduler WHERE id = ?`, created.Summary.ID).Scan(&compatibilityID); err != nil {
		t.Fatalf("read compatibility scheduler id: %v", err)
	}
	if compatibilityID != created.Summary.ID {
		t.Fatalf("compatibility scheduler id = %q, want %q", compatibilityID, created.Summary.ID)
	}
}
