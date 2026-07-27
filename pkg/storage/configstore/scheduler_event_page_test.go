package configstore

import (
	"context"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
)

func TestSchedulerEventPageOnlyReturnsEventsJoinedToTriggerRuns(t *testing.T) {
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
		{ID: "run-a", SchedulerID: "loader-a", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: startedAt},
		{ID: "run-b", SchedulerID: "loader-a", TriggerID: "trigger-b", Status: domain.SchedulerRunStatusSucceeded, StartedAt: startedAt},
		{ID: "invoke-old", SchedulerID: "loader-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: startedAt},
	} {
		if err := store.CreateSchedulerRun(ctx, run); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}
	for _, event := range []domain.SchedulerEvent{
		{SchedulerID: "loader-a", ID: "event-a2", RunID: "run-a", TriggerID: "wrong-event-trigger", Type: "loader.log", CreatedAt: startedAt.Add(2 * time.Second)},
		{SchedulerID: "loader-a", ID: "event-a1", RunID: "run-a", TriggerID: "trigger-a", Type: "loader.run.started", CreatedAt: startedAt.Add(time.Second)},
		{SchedulerID: "loader-a", ID: "event-b", RunID: "run-b", TriggerID: "trigger-b", Type: "loader.log", CreatedAt: startedAt},
		{SchedulerID: "loader-a", ID: "event-invoke", RunID: "invoke-old", Type: "loader.log", CreatedAt: startedAt.Add(3 * time.Second)},
		{SchedulerID: "loader-a", ID: "event-orphan", RunID: "missing", TriggerID: "trigger-a", Type: "loader.log", CreatedAt: startedAt.Add(4 * time.Second)},
	} {
		if err := store.AddSchedulerEvent(ctx, event); err != nil {
			t.Fatalf("add event %s: %v", event.ID, err)
		}
	}
	first, err := store.ListSchedulerEventsPage(ctx, schedulers.SchedulerEventPageFilter{SchedulerIDs: []string{"loader-a"}, RequireTrigger: true, TriggerID: "trigger-a", Limit: 1})
	if err != nil || len(first) != 1 || first[0].ID != "event-a2" || first[0].TriggerID != "trigger-a" {
		t.Fatalf("first page=%#v err=%v", first, err)
	}
	second, err := store.ListSchedulerEventsPage(ctx, schedulers.SchedulerEventPageFilter{
		SchedulerIDs: []string{"loader-a"}, RequireTrigger: true, TriggerID: "trigger-a", BeforeCreatedAt: first[0].CreatedAt,
		BeforeSchedulerID: first[0].SchedulerID, BeforeEventID: first[0].ID, Limit: 10,
	})
	if err != nil || len(second) != 1 || second[0].ID != "event-a1" {
		t.Fatalf("second page=%#v err=%v", second, err)
	}
	byRun, err := store.ListSchedulerEventsPage(ctx, schedulers.SchedulerEventPageFilter{SchedulerIDs: []string{"loader-a"}, RequireTrigger: true, RunID: "run-b", Limit: 10})
	if err != nil || len(byRun) != 1 || byRun[0].ID != "event-b" {
		t.Fatalf("run filter=%#v err=%v", byRun, err)
	}
}

func TestSchedulerEventPageSupportsTailBoundaryAndAscendingRange(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if _, err := upsertNativeTestScheduler(ctx, store, domain.Scheduler{Summary: domain.SchedulerSummary{ID: "loader-a", Runtime: domain.SchedulerRuntimeScheduler, ProjectID: "project-1", AgentName: "agent-1", ProjectSchedulerID: "scheduler-1"}, Script: "function main() {}"}); err != nil {
		t.Fatalf("upsert loader: %v", err)
	}
	startedAt := time.UnixMilli(1_730_000_000_000).UTC()
	if err := store.CreateSchedulerRun(ctx, domain.SchedulerRunSummary{ID: "run-a", SchedulerID: "loader-a", TriggerID: "trigger-a", Status: domain.SchedulerRunStatusSucceeded, StartedAt: startedAt}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	for index, id := range []string{"event-1", "event-2", "event-3", "event-4"} {
		if err := store.AddSchedulerEvent(ctx, domain.SchedulerEvent{SchedulerID: "loader-a", ID: id, RunID: "run-a", TriggerID: "trigger-a", Type: "loader.log", CreatedAt: startedAt.Add(time.Duration(index) * time.Second)}); err != nil {
			t.Fatalf("add event %s: %v", id, err)
		}
	}
	boundary, err := store.ListSchedulerEventsPage(ctx, schedulers.SchedulerEventPageFilter{SchedulerIDs: []string{"loader-a"}, RequireTrigger: true, Limit: 1, Offset: 1})
	if err != nil || len(boundary) != 1 || boundary[0].ID != "event-3" {
		t.Fatalf("tail boundary = %#v, err=%v", boundary, err)
	}
	page, err := store.ListSchedulerEventsPage(ctx, schedulers.SchedulerEventPageFilter{
		SchedulerIDs: []string{"loader-a"}, RequireTrigger: true, Ascending: true, Limit: 10,
		AfterCreatedAt: boundary[0].CreatedAt, AfterSchedulerID: boundary[0].SchedulerID, AfterEventID: boundary[0].ID,
		ThroughCreatedAt: startedAt.Add(3 * time.Second), ThroughSchedulerID: "loader-a", ThroughEventID: "event-4",
	})
	if err != nil || len(page) != 1 || page[0].ID != "event-4" {
		t.Fatalf("ascending bounded page = %#v, err=%v", page, err)
	}
}
