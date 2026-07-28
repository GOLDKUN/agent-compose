package configstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
)

func TestLoaderRunPageUsesStableCrossLoaderCursor(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	for _, schedulerID := range []string{"loader-a", "loader-b"} {
		if _, err := upsertNativeTestScheduler(ctx, store, domain.Scheduler{
			Summary: domain.SchedulerSummary{
				ID:                 schedulerID,
				Name:               schedulerID,
				Runtime:            domain.SchedulerRuntimeScheduler,
				ProjectID:          "project-1",
				AgentName:          schedulerID,
				ProjectSchedulerID: "scheduler-" + schedulerID,
			},
			Script: "function main() {}",
		}); err != nil {
			t.Fatalf("upsert loader %s: %v", schedulerID, err)
		}
	}
	newer := time.UnixMilli(1_720_000_000_500).UTC()
	older := newer.Add(-time.Second)
	for _, run := range []domain.SchedulerRunSummary{
		{ID: "run-a", SchedulerID: "loader-a", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: newer},
		{ID: "run-b1", SchedulerID: "loader-b", TriggerID: "trigger-b", Status: domain.SchedulerRunStatusSucceeded, StartedAt: newer},
		{ID: "run-b2", SchedulerID: "loader-b", TriggerID: "trigger-b", Status: domain.SchedulerRunStatusSucceeded, StartedAt: newer},
		{ID: "run-old", SchedulerID: "loader-b", TriggerID: "trigger-b", Status: domain.SchedulerRunStatusSucceeded, StartedAt: older},
	} {
		if err := store.CreateSchedulerRun(ctx, run); err != nil {
			t.Fatalf("create run %s: %v", run.ID, err)
		}
	}

	first, err := store.ListSchedulerRunsPage(ctx, schedulers.SchedulerRunPageFilter{
		SchedulerIDs: []string{" loader-a ", "loader-b", "loader-a"},
		Limit:        2,
	})
	if err != nil || len(first) != 2 || first[0].ID != "run-b2" || first[1].ID != "run-b1" {
		t.Fatalf("first page=%#v err=%v", first, err)
	}
	second, err := store.ListSchedulerRunsPage(ctx, schedulers.SchedulerRunPageFilter{
		SchedulerIDs:      []string{"loader-a", "loader-b"},
		BeforeStartedAt:   first[1].StartedAt,
		BeforeSchedulerID: first[1].SchedulerID,
		BeforeRunID:       first[1].ID,
		Limit:             2,
	})
	if err != nil || len(second) != 2 || second[0].ID != "run-a" || second[1].ID != "run-old" {
		t.Fatalf("second page=%#v err=%v", second, err)
	}
	filtered, err := store.ListSchedulerRunsPage(ctx, schedulers.SchedulerRunPageFilter{SchedulerIDs: []string{"loader-a"}, Limit: 10})
	if err != nil || len(filtered) != 1 || filtered[0].ID != "run-a" {
		t.Fatalf("filtered page=%#v err=%v", filtered, err)
	}
	byID, err := store.GetSchedulerRunForSchedulers(ctx, []string{"loader-b"}, "run-old")
	if err != nil || byID.SchedulerID != "loader-b" {
		t.Fatalf("GetSchedulerRunForSchedulers run=%#v err=%v", byID, err)
	}
	if _, err := store.GetSchedulerRunForSchedulers(ctx, []string{"loader-a"}, "run-old"); err == nil {
		t.Fatal("GetSchedulerRunForSchedulers accepted a run from another loader")
	}
	if _, err := store.GetSchedulerRunForSchedulers(ctx, nil, "missing"); err == nil {
		t.Fatal("GetSchedulerRunForSchedulers missing returned nil error")
	}
}

func TestGetLoaderRunForLoadersResolvesTriggerRunShortIDs(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if _, err := upsertNativeTestScheduler(ctx, store, domain.Scheduler{Summary: domain.SchedulerSummary{ID: "loader-a", Runtime: domain.SchedulerRuntimeScheduler, ProjectID: "project-1", AgentName: "agent-1", ProjectSchedulerID: "scheduler-1"}, Script: "function main() {}"}); err != nil {
		t.Fatalf("upsert loader: %v", err)
	}
	prefix := "abcdef123456"
	firstID := prefix + strings.Repeat("1", 52)
	secondID := prefix + strings.Repeat("2", 52)
	for _, run := range []domain.SchedulerRunSummary{
		{ID: firstID, SchedulerID: "loader-a", TriggerID: "trigger-1", Status: domain.SchedulerRunStatusSucceeded, StartedAt: time.Now().UTC()},
		{ID: secondID, SchedulerID: "loader-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: time.Now().UTC()},
	} {
		if err := store.CreateSchedulerRun(ctx, run); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}
	resolved, err := store.GetSchedulerRunForSchedulers(ctx, []string{"loader-a"}, prefix)
	if err != nil || resolved.ID != firstID {
		t.Fatalf("resolved run=%#v err=%v", resolved, err)
	}
	if err := store.CreateSchedulerRun(ctx, domain.SchedulerRunSummary{ID: secondID, SchedulerID: "loader-a", TriggerID: "trigger-2", Status: domain.SchedulerRunStatusSucceeded, StartedAt: time.Now().UTC()}); err == nil {
		t.Fatal("expected duplicate run update setup to fail")
	}
	thirdID := prefix + strings.Repeat("3", 52)
	if err := store.CreateSchedulerRun(ctx, domain.SchedulerRunSummary{ID: thirdID, SchedulerID: "loader-a", TriggerID: "trigger-2", Status: domain.SchedulerRunStatusSucceeded, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create ambiguous run: %v", err)
	}
	if _, err := store.GetSchedulerRunForSchedulers(ctx, []string{"loader-a"}, prefix); !errors.Is(err, domain.ErrAmbiguous) {
		t.Fatalf("ambiguous short id error=%v", err)
	}
}

func TestLoaderRunPageFiltersTriggerRunsBeforeLimitAndBatchesSandboxes(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if _, err := upsertNativeTestScheduler(ctx, store, domain.Scheduler{Summary: domain.SchedulerSummary{ID: "loader-a", Runtime: domain.SchedulerRuntimeScheduler, ProjectID: "project-1", AgentName: "agent-1", ProjectSchedulerID: "scheduler-1"}, Script: "function main() {}"}); err != nil {
		t.Fatalf("upsert loader: %v", err)
	}
	startedAt := time.UnixMilli(1_720_000_000_000).UTC()
	for _, run := range []domain.SchedulerRunSummary{
		{ID: "invoke-newest", SchedulerID: "loader-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: startedAt.Add(3 * time.Second)},
		{ID: "run-failed", SchedulerID: "loader-a", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusFailed, StartedAt: startedAt.Add(2 * time.Second)},
		{ID: "run-success", SchedulerID: "loader-a", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: startedAt.Add(time.Second)},
		{ID: "run-other", SchedulerID: "loader-a", TriggerID: "trigger-b", Status: domain.SchedulerRunStatusSucceeded, StartedAt: startedAt},
	} {
		if err := store.CreateSchedulerRun(ctx, run); err != nil {
			t.Fatalf("create run %s: %v", run.ID, err)
		}
	}
	filtered, err := store.ListSchedulerRunsPage(ctx, schedulers.SchedulerRunPageFilter{
		SchedulerIDs: []string{"loader-a"}, RequireTrigger: true, TriggerID: "trigger-a", Status: domain.SchedulerRunStatusSucceeded, Limit: 1,
	})
	if err != nil || len(filtered) != 1 || filtered[0].ID != "run-success" {
		t.Fatalf("filtered runs=%#v err=%v", filtered, err)
	}
	for index, sandboxID := range []string{"sandbox-b", "sandbox-a", "sandbox-a"} {
		if err := store.AddSchedulerEvent(ctx, domain.SchedulerEvent{SchedulerID: "loader-a", ID: fmt.Sprintf("event-%d", index), RunID: "run-success", TriggerID: "trigger-a", Type: "scheduler.test", LinkedSandboxID: sandboxID, CreatedAt: startedAt}); err != nil {
			t.Fatalf("add event: %v", err)
		}
	}
	sandboxes, err := store.ListSchedulerRunSandboxIDs(ctx, []schedulers.SchedulerRunKey{{SchedulerID: "loader-a", RunID: "run-success"}, {SchedulerID: "loader-a", RunID: "run-success"}})
	if err != nil || !reflect.DeepEqual(sandboxes[schedulers.SchedulerRunKey{SchedulerID: "loader-a", RunID: "run-success"}], []string{"sandbox-a", "sandbox-b"}) {
		t.Fatalf("sandbox ids=%#v err=%v", sandboxes, err)
	}

	latest, err := store.BatchGetLatestSchedulerRunsBySandboxIDs(ctx, []string{"loader-a"}, []string{"sandbox-a", "sandbox-b", "sandbox-missing"})
	if err != nil {
		t.Fatalf("list latest runs by sandbox ids: %v", err)
	}
	if len(latest) != 2 || latest["sandbox-a"].ID != "run-success" || latest["sandbox-b"].ID != "run-success" {
		t.Fatalf("latest runs by sandbox ids=%#v", latest)
	}
}

func TestBatchGetLatestLoaderRunsBySandboxIDsSelectsLatestTriggerRun(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	for _, loader := range []domain.Scheduler{
		{Summary: domain.SchedulerSummary{ID: "loader-a", Runtime: domain.SchedulerRuntimeScheduler, ProjectID: "project-1", AgentName: "agent-a", ProjectSchedulerID: "scheduler-a"}, Script: "function main() {}"},
		{Summary: domain.SchedulerSummary{ID: "loader-other", Runtime: domain.SchedulerRuntimeScheduler, ProjectID: "project-2", AgentName: "agent-other", ProjectSchedulerID: "scheduler-other"}, Script: "function main() {}"},
	} {
		if _, err := upsertNativeTestScheduler(ctx, store, loader); err != nil {
			t.Fatalf("upsert loader %s: %v", loader.Summary.ID, err)
		}
	}
	startedAt := time.UnixMilli(1_720_000_000_000).UTC()
	for _, run := range []domain.SchedulerRunSummary{
		{ID: "run-older", SchedulerID: "loader-a", TriggerID: "trigger-a", StartedAt: startedAt},
		{ID: "invoke-newer", SchedulerID: "loader-a", StartedAt: startedAt.Add(time.Second)},
		{ID: "run-newest", SchedulerID: "loader-a", TriggerID: "trigger-a", StartedAt: startedAt.Add(2 * time.Second)},
		{ID: "run-other-project", SchedulerID: "loader-other", TriggerID: "trigger-a", StartedAt: startedAt.Add(3 * time.Second)},
	} {
		if err := store.CreateSchedulerRun(ctx, run); err != nil {
			t.Fatalf("create run %s: %v", run.ID, err)
		}
		if err := store.AddSchedulerEvent(ctx, domain.SchedulerEvent{SchedulerID: run.SchedulerID, ID: "event-" + run.ID, RunID: run.ID, TriggerID: run.TriggerID, Type: "scheduler.test", LinkedSandboxID: "sandbox-a", CreatedAt: run.StartedAt}); err != nil {
			t.Fatalf("add event for run %s: %v", run.ID, err)
		}
	}

	latest, err := store.BatchGetLatestSchedulerRunsBySandboxIDs(ctx, []string{"loader-a"}, []string{"sandbox-a", "sandbox-a", ""})
	if err != nil {
		t.Fatalf("list latest runs by sandbox ids: %v", err)
	}
	if len(latest) != 1 || latest["sandbox-a"].ID != "run-newest" {
		t.Fatalf("latest runs by sandbox ids=%#v, want run-newest", latest)
	}
}
