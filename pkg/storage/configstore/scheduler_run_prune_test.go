package configstore

import (
	"context"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
)

func TestSchedulerRunPruneFiltersAndDeletesDirectRunData(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	createPruneTestScheduler(t, store, "scheduler-a")
	createPruneTestScheduler(t, store, "scheduler-b")
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	newer := now.Add(-time.Hour)
	for _, run := range []domain.SchedulerRunSummary{
		{ID: "run-old-success", SchedulerID: "scheduler-a", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: old, CompletedAt: old.Add(time.Minute)},
		{ID: "run-new-failed", SchedulerID: "scheduler-a", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusFailed, StartedAt: newer, CompletedAt: newer.Add(time.Minute)},
		{ID: "run-old-other-trigger", SchedulerID: "scheduler-a", TriggerID: "trigger-b", Status: domain.SchedulerRunStatusFailed, StartedAt: old.Add(time.Minute), CompletedAt: old.Add(2 * time.Minute)},
		{ID: "run-running", SchedulerID: "scheduler-a", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusRunning, StartedAt: old},
		{ID: "run-invocation", SchedulerID: "scheduler-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: old, CompletedAt: old.Add(time.Minute)},
		{ID: "run-other-scheduler", SchedulerID: "scheduler-b", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: old, CompletedAt: old.Add(time.Minute)},
	} {
		if err := store.CreateSchedulerRun(ctx, run); err != nil {
			t.Fatalf("create run %s: %v", run.ID, err)
		}
	}
	addPruneTestRelations(t, store, pruneTestRelations{
		SchedulerID: "scheduler-a",
		RunID:       "run-old-success",
		TriggerID:   "trigger-a",
	})
	if _, err := store.CreateEvent(ctx, domain.TopicEventRecord{
		ID: "topic-event", Topic: "topic.test", Source: domain.TopicEventSourceSystem,
		CorrelationID: "correlation-test", PayloadJSON: `{}`, CreatedAt: old,
	}); err != nil {
		t.Fatalf("create topic event: %v", err)
	}

	filtered, err := store.ListSchedulerRunsForPrune(ctx, schedulers.SchedulerRunPruneFilter{
		SchedulerIDs: []string{"scheduler-a"}, TriggerID: "trigger-a",
		Statuses:  []string{domain.SchedulerRunStatusSucceeded, domain.SchedulerRunStatusFailed},
		OlderThan: 24 * time.Hour, Now: now,
	})
	if err != nil || len(filtered) != 1 || filtered[0].ID != "run-old-success" {
		t.Fatalf("filtered runs=%#v err=%v", filtered, err)
	}
	keys := []schedulers.SchedulerRunKey{{SchedulerID: "scheduler-a", RunID: "run-old-success"}}
	counted, err := store.CountSchedulerRunPruneData(ctx, keys)
	if err != nil {
		t.Fatalf("count prune data: %v", err)
	}
	if counted.Runs != 1 || counted.SchedulerEvents != 1 || counted.EventDeliveries != 1 || counted.EventSandboxLinks != 1 {
		t.Fatalf("counted prune data = %#v", counted)
	}
	removed, err := store.DeleteSchedulerRunPruneData(ctx, keys)
	if err != nil {
		t.Fatalf("delete prune data: %v", err)
	}
	if removed.Stats != counted || len(removed.RemovedKeys) != 1 || removed.RemovedKeys[0] != keys[0] {
		t.Fatalf("removed=%#v, want stats %#v and key %#v", removed, counted, keys[0])
	}
	if remaining, err := store.ListSchedulerRunsForPrune(ctx, schedulers.SchedulerRunPruneFilter{SchedulerIDs: []string{"scheduler-a", "scheduler-b"}}); err != nil || len(remaining) != 4 {
		t.Fatalf("remaining trigger runs=%#v err=%v", remaining, err)
	}
	for table, where := range map[string]string{
		"scheduler_event":    "scheduler_id = 'scheduler-a' AND scheduler_run_id = 'run-old-success'",
		"event_delivery":     "scheduler_id = 'scheduler-a' AND scheduler_run_id = 'run-old-success'",
		"event_sandbox_link": "scheduler_id = 'scheduler-a' AND scheduler_run_id = 'run-old-success'",
	} {
		assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
			Table: table,
			Where: where,
			Want:  0,
		})
	}
	assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
		Table: "event",
		Where: "id = 'topic-event'",
		Want:  1,
	})
	assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
		Table: "scheduler_run",
		Where: "run_id = 'run-invocation'",
		Want:  1,
	})
	assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
		Table: "scheduler_run",
		Where: "run_id = 'run-running'",
		Want:  1,
	})
}

func TestDeleteSchedulerRunPruneDataRollsBackWholeTransaction(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	createPruneTestScheduler(t, store, "scheduler-a")
	now := time.Now().UTC()
	for _, runID := range []string{"run-ok", "run-fail"} {
		if err := store.CreateSchedulerRun(ctx, domain.SchedulerRunSummary{ID: runID, SchedulerID: "scheduler-a", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: now, CompletedAt: now}); err != nil {
			t.Fatalf("create run %s: %v", runID, err)
		}
		addPruneTestRelations(t, store, pruneTestRelations{
			SchedulerID: "scheduler-a",
			RunID:       runID,
			TriggerID:   "trigger-a",
		})
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER block_prune_event BEFORE DELETE ON scheduler_event
		WHEN OLD.scheduler_run_id = 'run-fail' BEGIN SELECT RAISE(ABORT, 'blocked prune'); END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	keys := []schedulers.SchedulerRunKey{{SchedulerID: "scheduler-a", RunID: "run-ok"}, {SchedulerID: "scheduler-a", RunID: "run-fail"}}
	if _, err := store.DeleteSchedulerRunPruneData(ctx, keys); err == nil {
		t.Fatal("DeleteSchedulerRunPruneData returned nil error")
	}
	for _, runID := range []string{"run-ok", "run-fail"} {
		assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
			Table: "scheduler_run",
			Where: "run_id = '" + runID + "'",
			Want:  1,
		})
		assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
			Table: "scheduler_event",
			Where: "scheduler_run_id = '" + runID + "'",
			Want:  1,
		})
		assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
			Table: "event_delivery",
			Where: "scheduler_run_id = '" + runID + "'",
			Want:  1,
		})
		assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
			Table: "event_sandbox_link",
			Where: "scheduler_run_id = '" + runID + "'",
			Want:  1,
		})
	}
}

func TestDeleteSchedulerRunPruneDataRechecksTerminalTriggerRun(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	createPruneTestScheduler(t, store, "scheduler-a")
	for _, run := range []domain.SchedulerRunSummary{
		{ID: "run-running", SchedulerID: "scheduler-a", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusRunning, StartedAt: time.Now().UTC()},
		{ID: "run-empty-trigger", SchedulerID: "scheduler-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()},
	} {
		if err := store.CreateSchedulerRun(ctx, run); err != nil {
			t.Fatalf("create run %s: %v", run.ID, err)
		}
		addPruneTestRelations(t, store, pruneTestRelations{
			SchedulerID: "scheduler-a",
			RunID:       run.ID,
			TriggerID:   run.TriggerID,
		})
	}
	keys := []schedulers.SchedulerRunKey{{SchedulerID: "scheduler-a", RunID: "run-running"}, {SchedulerID: "scheduler-a", RunID: "run-empty-trigger"}}
	if counted, err := store.CountSchedulerRunPruneData(ctx, keys); err != nil || counted != (schedulers.SchedulerRunPruneDatabaseStats{}) {
		t.Fatalf("counted=%#v err=%v", counted, err)
	}
	if removed, err := store.DeleteSchedulerRunPruneData(ctx, keys); err != nil || removed.Stats != (schedulers.SchedulerRunPruneDatabaseStats{}) || len(removed.RemovedKeys) != 0 {
		t.Fatalf("removed=%#v err=%v", removed, err)
	}
	for _, runID := range []string{"run-running", "run-empty-trigger"} {
		assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
			Table: "scheduler_run",
			Where: "run_id = '" + runID + "'",
			Want:  1,
		})
		assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
			Table: "scheduler_event",
			Where: "scheduler_run_id = '" + runID + "'",
			Want:  1,
		})
		assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
			Table: "event_delivery",
			Where: "scheduler_run_id = '" + runID + "'",
			Want:  1,
		})
		assertPruneTestRowCount(t, store, pruneTestRowCountCheck{
			Table: "event_sandbox_link",
			Where: "scheduler_run_id = '" + runID + "'",
			Want:  1,
		})
	}
}

func createPruneTestScheduler(t *testing.T, store *ConfigStore, schedulerID string) {
	t.Helper()
	if _, err := upsertNativeTestScheduler(context.Background(), store, domain.Scheduler{
		Summary: domain.SchedulerSummary{
			ID: schedulerID, Name: schedulerID, Runtime: domain.SchedulerRuntimeScheduler,
			ProjectID: "project-1", AgentName: schedulerID, ProjectSchedulerID: "scheduler-" + schedulerID,
		},
		Script: "function main() {}",
	}); err != nil {
		t.Fatalf("create scheduler %s: %v", schedulerID, err)
	}
}

type pruneTestRelations struct {
	SchedulerID string
	RunID       string
	TriggerID   string
}

func addPruneTestRelations(t *testing.T, store *ConfigStore, rel pruneTestRelations) {
	t.Helper()
	ctx := context.Background()
	if err := store.AddSchedulerEvent(ctx, domain.SchedulerEvent{ID: "scheduler-event-" + rel.RunID, SchedulerID: rel.SchedulerID, RunID: rel.RunID, TriggerID: rel.TriggerID, Type: "scheduler.run.completed", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("add scheduler event: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO event_delivery(event_id, scheduler_id, trigger_id, scheduler_run_id, status, created_at, updated_at) VALUES(?, ?, ?, ?, 'run_succeeded', 1, 1)`, "event-"+rel.RunID, rel.SchedulerID, rel.TriggerID, rel.RunID); err != nil {
		t.Fatalf("add event delivery: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO event_sandbox_link(event_id, sandbox_id, relation, scheduler_id, scheduler_run_id, trigger_id, created_at) VALUES(?, ?, 'used', ?, ?, ?, 1)`, "event-"+rel.RunID, "sandbox-"+rel.RunID, rel.SchedulerID, rel.RunID, rel.TriggerID); err != nil {
		t.Fatalf("add event sandbox link: %v", err)
	}
}

type pruneTestRowCountCheck struct {
	Table string
	Where string
	Want  int
}

func assertPruneTestRowCount(t *testing.T, store *ConfigStore, check pruneTestRowCountCheck) {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+check.Table+" WHERE "+check.Where).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", check.Table, err)
	}
	if count != check.Want {
		t.Fatalf("%s count=%d, want %d", check.Table, count, check.Want)
	}
}
