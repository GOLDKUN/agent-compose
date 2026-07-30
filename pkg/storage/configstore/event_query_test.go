package configstore

import (
	"context"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
)

func TestEventQueriesFilterAndAggregateBySource(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	created := make([]domain.TopicEventRecord, 0, 3)
	for _, item := range []domain.TopicEventRecord{
		{ID: "webhook-first", Topic: "webhook.github.push", Source: domain.TopicEventSourceWebhook, CorrelationID: "corr-1", PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPending},
		{ID: "webhook-second", Topic: "webhook.gitlab.push", Source: domain.TopicEventSourceWebhook, CorrelationID: "corr-2", PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPending},
		{ID: "scheduler-event", Topic: "runtime.completed", Source: domain.TopicEventSourceScheduler, CorrelationID: "corr-1", PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPending},
	} {
		event, err := store.CreateEvent(ctx, item)
		if err != nil {
			t.Fatalf("create event %s: %v", item.ID, err)
		}
		created = append(created, event)
	}
	items, total, err := store.ListEvents(ctx, domain.TopicEventFilter{Source: domain.TopicEventSourceWebhook, Limit: 10})
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("webhook events = %#v total=%d err=%v", items, total, err)
	}
	if items[0].ID != created[1].ID || items[1].ID != created[0].ID {
		t.Fatalf("webhook event order = %#v", items)
	}
	summaries, summaryTotal, err := store.ListEventSummaries(ctx, domain.TopicEventFilter{Source: domain.TopicEventSourceWebhook, Limit: 10})
	if err != nil || summaryTotal != 2 || len(summaries) != 2 {
		t.Fatalf("webhook event summaries = %#v total=%d err=%v", summaries, summaryTotal, err)
	}
	if summaries[0].ID != created[1].ID || summaries[1].ID != created[0].ID {
		t.Fatalf("webhook event summary order = %#v", summaries)
	}
	topics, topicTotal, err := store.ListEventTopics(ctx, domain.TopicEventSourceWebhook, 0, 10)
	if err != nil || topicTotal != 2 || len(topics) != 2 {
		t.Fatalf("webhook topics = %#v total=%d err=%v", topics, topicTotal, err)
	}
	for _, topic := range topics {
		if topic.EventCount != 1 || topic.Topic == "runtime.completed" {
			t.Fatalf("webhook topic = %#v", topic)
		}
	}
}

func TestEventQueriesIncludeLegacyLoaderRowsInSchedulerFilters(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	for _, item := range []domain.TopicEventRecord{
		{ID: "scheduler-current", Topic: "runtime.completed", Source: domain.TopicEventSourceScheduler, PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPending},
		{ID: "scheduler-legacy-same-topic", Topic: "runtime.completed", Source: domain.TopicEventSourceScheduler, PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPending},
		{ID: "scheduler-legacy-other-topic", Topic: "runtime.started", Source: domain.TopicEventSourceScheduler, PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPending},
		{ID: "webhook-unrelated", Topic: "runtime.completed", Source: domain.TopicEventSourceWebhook, PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPending},
	} {
		if _, err := store.CreateEvent(ctx, item); err != nil {
			t.Fatalf("create event %s: %v", item.ID, err)
		}
	}
	// Normal writes canonicalize loader to scheduler. Rewrite these rows to
	// reproduce the historical representation retained by upgraded databases.
	if _, err := store.db.ExecContext(ctx, `UPDATE event SET source = 'loader' WHERE id IN (?, ?)`,
		"scheduler-legacy-same-topic", "scheduler-legacy-other-topic"); err != nil {
		t.Fatalf("store legacy loader events: %v", err)
	}

	for _, source := range []string{domain.TopicEventSourceScheduler, "loader"} {
		t.Run(source, func(t *testing.T) {
			items, total, err := store.ListEvents(ctx, domain.TopicEventFilter{Source: source, Limit: 10})
			if err != nil || total != 3 || len(items) != 3 {
				t.Fatalf("events = %#v total=%d err=%v", items, total, err)
			}
			if items[0].ID != "scheduler-legacy-other-topic" || items[1].ID != "scheduler-legacy-same-topic" || items[2].ID != "scheduler-current" {
				t.Fatalf("event order = %#v", items)
			}

			summaries, summaryTotal, err := store.ListEventSummaries(ctx, domain.TopicEventFilter{Source: source, Limit: 10})
			if err != nil || summaryTotal != 3 || len(summaries) != 3 {
				t.Fatalf("event summaries = %#v total=%d err=%v", summaries, summaryTotal, err)
			}
			if summaries[0].ID != "scheduler-legacy-other-topic" || summaries[1].ID != "scheduler-legacy-same-topic" || summaries[2].ID != "scheduler-current" {
				t.Fatalf("event summary order = %#v", summaries)
			}

			topics, topicTotal, err := store.ListEventTopics(ctx, source, 0, 10)
			if err != nil || topicTotal != 2 || len(topics) != 2 {
				t.Fatalf("event topics = %#v total=%d err=%v", topics, topicTotal, err)
			}
			counts := make(map[string]int, len(topics))
			for _, topic := range topics {
				counts[topic.Topic] = topic.EventCount
			}
			if counts["runtime.completed"] != 2 || counts["runtime.started"] != 1 {
				t.Fatalf("event topic counts = %#v", counts)
			}
		})
	}
}

func TestEventTraceIncludesDescendantRunWithoutExposingSeparateRoot(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	scheduler, err := upsertNativeTestScheduler(ctx, store, domain.Scheduler{
		Summary: domain.SchedulerSummary{
			ID: "scheduler-webhook", ProjectID: "project-webhook", AgentName: "worker", Name: "Webhook Automation", Enabled: true,
		},
		Triggers: []domain.SchedulerTrigger{{ID: "trigger-webhook", Kind: domain.SchedulerTriggerKindEvent, Topic: "webhook.github.push", Enabled: true}},
	})
	if err != nil {
		t.Fatalf("upsert scheduler: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE project_scheduler SET spec_json = ? WHERE id = ?`, `{"display_name":"Webhook Automation"}`, scheduler.Summary.ID); err != nil {
		t.Fatalf("update scheduler presentation: %v", err)
	}
	root, err := store.CreateEvent(ctx, domain.TopicEventRecord{
		ID: "webhook-root", Topic: "webhook.github.push", Source: domain.TopicEventSourceWebhook,
		CorrelationID: "corr-trace", PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPublishedToBus,
	})
	if err != nil {
		t.Fatalf("create root event: %v", err)
	}
	child, err := store.CreateEvent(ctx, domain.TopicEventRecord{
		ID: "runtime-child", Topic: "runtime.completed", Source: domain.TopicEventSourceScheduler,
		CorrelationID: root.CorrelationID, ParentEventID: root.ID, PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPublishedToBus,
	})
	if err != nil {
		t.Fatalf("create child event: %v", err)
	}
	descendant, err := store.CreateEvent(ctx, domain.TopicEventRecord{
		ID: "runtime-grandchild", Topic: "runtime.finished", Source: domain.TopicEventSourceScheduler,
		CorrelationID: root.CorrelationID, ParentEventID: child.ID, PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPublishedToBus,
	})
	if err != nil {
		t.Fatalf("create grandchild event: %v", err)
	}
	now := time.Now().UTC().Round(0)
	run := domain.SchedulerRunSummary{
		ID: "run-webhook", SchedulerID: scheduler.Summary.ID, TriggerID: "trigger-webhook",
		TriggerKind: domain.SchedulerTriggerKindEvent, Status: domain.SchedulerRunStatusSucceeded,
		StartedAt: now, CompletedAt: now.Add(time.Second), DurationMs: 1000,
	}
	if err := store.CreateSchedulerRun(ctx, run); err != nil {
		t.Fatalf("create scheduler run: %v", err)
	}
	if err := store.UpsertEventDelivery(ctx, domain.EventDelivery{
		EventID: descendant.ID, SchedulerID: scheduler.Summary.ID, TriggerID: run.TriggerID,
		RunID: run.ID, Status: domain.EventDeliveryStatusRunSucceeded, CreatedAt: now, UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("upsert delivery: %v", err)
	}
	if err := store.AddSchedulerEvent(ctx, domain.SchedulerEvent{
		ID: "scheduler-event", SchedulerID: scheduler.Summary.ID, RunID: run.ID, TriggerID: run.TriggerID,
		Type: "scheduler.completed", Level: "info", Message: "completed", CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("add scheduler event: %v", err)
	}
	if err := store.AddEventSandboxLink(ctx, domain.EventSandboxLink{
		EventID: descendant.ID, SandboxID: "sandbox-webhook", Relation: "created",
		SchedulerID: scheduler.Summary.ID, RunID: run.ID, TriggerID: run.TriggerID, CreatedAt: now,
	}); err != nil {
		t.Fatalf("add sandbox link: %v", err)
	}
	trace, err := store.GetEventTrace(ctx, root.ID, 1000)
	if err != nil {
		t.Fatalf("get event trace: %v", err)
	}
	if trace.Event.ID != root.ID || trace.DescendantsTruncated || len(trace.Runs) != 1 || len(trace.SandboxLinks) != 1 {
		t.Fatalf("trace = %#v", trace)
	}
	got := trace.Runs[0]
	if got.Delivery.EventID != descendant.ID || got.Run == nil || got.Run.ID != run.ID || got.Scheduler == nil || got.Scheduler.Name != "Webhook Automation" || len(got.Events) != 1 {
		t.Fatalf("run trace = %#v", got)
	}
}

func TestEventTraceFallsBackToAgentNameForInvalidSchedulerPresentation(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	scheduler, err := upsertNativeTestScheduler(ctx, store, domain.Scheduler{
		Summary:  domain.SchedulerSummary{ID: "scheduler-invalid-presentation", ProjectID: "project-invalid-presentation", AgentName: "worker", Enabled: true},
		Triggers: []domain.SchedulerTrigger{{ID: "trigger-invalid-presentation", Kind: domain.SchedulerTriggerKindEvent, Topic: "webhook.invalid", Enabled: true}},
	})
	if err != nil {
		t.Fatalf("upsert scheduler: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE project_scheduler SET spec_json = ? WHERE id = ?`, `{`, scheduler.Summary.ID); err != nil {
		t.Fatalf("set invalid scheduler presentation: %v", err)
	}
	event, err := store.CreateEvent(ctx, domain.TopicEventRecord{
		ID: "event-invalid-presentation", Topic: "webhook.invalid", Source: domain.TopicEventSourceWebhook,
		CorrelationID: "corr-invalid-presentation", PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPublishedToBus,
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if err := store.UpsertEventDelivery(ctx, domain.EventDelivery{
		EventID: event.ID, SchedulerID: scheduler.Summary.ID, TriggerID: "trigger-invalid-presentation", Status: domain.EventDeliveryStatusMatched,
	}); err != nil {
		t.Fatalf("upsert delivery: %v", err)
	}

	trace, err := store.GetEventTrace(ctx, event.ID, 1000)
	if err != nil {
		t.Fatalf("get event trace: %v", err)
	}
	if len(trace.Runs) != 1 || trace.Runs[0].Scheduler == nil || trace.Runs[0].Scheduler.Name != "worker" {
		t.Fatalf("trace scheduler = %#v", trace.Runs)
	}
}

func TestEventTraceReportsDescendantTruncation(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	root, err := store.CreateEvent(ctx, domain.TopicEventRecord{
		ID: "trace-root", Topic: "webhook.trace", Source: domain.TopicEventSourceWebhook,
		CorrelationID: "corr-truncated", PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPublishedToBus,
	})
	if err != nil {
		t.Fatalf("create root event: %v", err)
	}
	for _, id := range []string{"trace-child-1", "trace-child-2"} {
		if _, err := store.CreateEvent(ctx, domain.TopicEventRecord{
			ID: id, Topic: "runtime.trace", Source: domain.TopicEventSourceScheduler,
			CorrelationID: root.CorrelationID, ParentEventID: root.ID,
			PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPublishedToBus,
		}); err != nil {
			t.Fatalf("create child event %s: %v", id, err)
		}
	}
	trace, err := store.GetEventTrace(ctx, root.ID, 2)
	if err != nil {
		t.Fatalf("get truncated trace: %v", err)
	}
	if trace.Event.ID != root.ID || !trace.DescendantsTruncated {
		t.Fatalf("truncated trace = %#v", trace)
	}
}
