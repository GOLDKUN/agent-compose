package schedulers_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-compose/pkg/events/webhooks"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
)

func TestRunExecutorLifecycleWorkflows(t *testing.T) {
	ctx := context.Background()
	loader := domain.Scheduler{Summary: domain.SchedulerSummary{ID: "loader-1", Runtime: domain.SchedulerRuntimeScheduler}, Script: "script"}
	trigger := domain.SchedulerTrigger{ID: "trigger-1", Kind: domain.SchedulerTriggerKindEvent}
	store := &runStoreFake{}
	engine := &loaderEngineFake{result: schedulers.SchedulerExecutionResult{ResultJSON: `{"ok":true}`, Warnings: []string{"scheduler.session.getSession is deprecated; use scheduler.sandbox.getSandbox"}}}
	var events []string
	var deliveries []domain.SchedulerRunSummary
	var notifications []string
	var refreshes int
	var leaves []string
	executor := schedulers.NewRunExecutor(schedulers.RunExecutorDependencies{
		Store:  store,
		Engine: engine,
		HostFactory: func(domain.Scheduler, schedulers.RuntimeExecutionContext, schedulers.TriggerEventMetadata) schedulers.RunHost {
			return &runHostFake{}
		},
		ArtifactsDir: func(schedulerID, runID string) string {
			return filepath.Join(t.TempDir(), schedulerID, runID)
		},
		WriteArtifact: func(dir, name, content string) error {
			if strings.TrimSpace(dir) == "" || strings.TrimSpace(name) == "" {
				t.Fatalf("write artifact received empty path: %q/%q", dir, name)
			}
			return nil
		},
		EnterRun: func(domain.Scheduler) bool { return true },
		LeaveRun: func(schedulerID string) {
			leaves = append(leaves, schedulerID)
		},
		AddSchedulerEvent: func(_ context.Context, _, _, _, eventType, _, _ string, _ any, _, _, _ string) error {
			events = append(events, eventType)
			return nil
		},
		UpdateTriggerEventDelivery: func(_ context.Context, run domain.SchedulerRunSummary) {
			deliveries = append(deliveries, run)
		},
		Notify: func(reason string) {
			notifications = append(notifications, reason)
		},
		Refresh: func(context.Context) error {
			refreshes++
			return nil
		},
	})

	run, err := executor.Run(ctx, loader, &trigger, `{"eventId":"evt-1"}`, "manual", schedulers.RunOptions{})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Status != domain.SchedulerRunStatusSucceeded || run.ResultJSON != `{"ok":true}` || run.TriggerID != trigger.ID {
		t.Fatalf("run = %#v", run)
	}
	if len(store.created) != 1 || len(store.updated) != 1 || store.lastError[loader.Summary.ID] != "" {
		t.Fatalf("store state = %#v/%#v/%#v", store.created, store.updated, store.lastError)
	}
	if !containsString(events, "loader.run.started") || !containsString(events, "loader.run.completed") || !containsString(events, "loader.deprecated_alias.warning") {
		t.Fatalf("events = %#v", events)
	}
	if len(deliveries) != 2 || len(notifications) != 2 || refreshes != 1 || len(leaves) != 1 {
		t.Fatalf("deliveries/notifications/refreshes/leaves = %d/%d/%d/%d", len(deliveries), len(notifications), refreshes, len(leaves))
	}

	busyStore := &runStoreFake{}
	busyExecutor := schedulers.NewRunExecutor(schedulers.RunExecutorDependencies{
		Store:  busyStore,
		Engine: engine,
		HostFactory: func(domain.Scheduler, schedulers.RuntimeExecutionContext, schedulers.TriggerEventMetadata) schedulers.RunHost {
			return &runHostFake{}
		},
		ArtifactsDir:  func(schedulerID, runID string) string { return filepath.Join(t.TempDir(), schedulerID, runID) },
		WriteArtifact: func(string, string, string) error { return nil },
		EnterRun:      func(domain.Scheduler) bool { return false },
		AddSchedulerEvent: func(_ context.Context, _, _, _, eventType, _, _ string, _ any, _, _, _ string) error {
			events = append(events, eventType)
			return nil
		},
		UpdateTriggerEventDelivery: func(context.Context, domain.SchedulerRunSummary) {},
		Notify:                     func(string) {},
	})
	skipped, err := busyExecutor.Run(ctx, loader, nil, `{}`, "manual", schedulers.RunOptions{})
	if err != nil {
		t.Fatalf("busy Run returned error: %v", err)
	}
	if skipped.Status != domain.SchedulerRunStatusSkipped || skipped.Error == "" || busyStore.lastError[loader.Summary.ID] == "" {
		t.Fatalf("skipped run/store = %#v/%#v", skipped, busyStore.lastError)
	}
	if _, err := busyExecutor.Run(ctx, loader, nil, `{}`, "manual", schedulers.RunOptions{RetryWhenBusy: true}); !errors.Is(err, schedulers.ErrRunBusyForRetry) {
		t.Fatalf("busy retry err = %v", err)
	}
}

func TestEventDispatcherWorkflows(t *testing.T) {
	ctx := context.Background()
	store := &eventDeliveryStoreFake{}
	loader := domain.Scheduler{Summary: domain.SchedulerSummary{ID: "loader-1", Enabled: true}, Triggers: []domain.SchedulerTrigger{{
		ID:      "trigger-1",
		Kind:    domain.SchedulerTriggerKindEvent,
		Topic:   "topic.one",
		Enabled: true,
	}}}

	noSubscriberAcked := false
	dispatcher := schedulers.NewEventDispatcher(schedulers.EventDispatcherDependencies{
		RootCtx: ctx,
		Targets: func(string) []schedulers.EventTarget {
			return nil
		},
	})
	dispatcher.Dispatch(domain.SchedulerTopicEvent{Topic: "missing", CreatedAt: time.Now().UTC(), NoSubscriberAck: func(context.Context) error {
		noSubscriberAcked = true
		return nil
	}})
	if !noSubscriberAcked {
		t.Fatalf("no subscriber ack was not called")
	}

	retryReason := ""
	dispatcher = schedulers.NewEventDispatcher(schedulers.EventDispatcherDependencies{
		RootCtx: ctx,
		Targets: func(string) []schedulers.EventTarget {
			return []schedulers.EventTarget{{Scheduler: loader, Trigger: loader.Triggers[0]}}
		},
		IsBusy: func([]schedulers.EventTarget) bool { return true },
	})
	dispatcher.Dispatch(domain.SchedulerTopicEvent{Topic: "topic.one", Source: domain.TopicEventSourceWebhook, CreatedAt: time.Now().UTC(), Retry: func(_ context.Context, reason string, _ time.Time) error {
		retryReason = reason
		return nil
	}})
	if retryReason != "loader is already running" {
		t.Fatalf("retry reason = %q", retryReason)
	}

	runCalled := make(chan string, 1)
	ackCalled := false
	released := false
	dispatcher = schedulers.NewEventDispatcher(schedulers.EventDispatcherDependencies{
		RootCtx: ctx,
		Store:   store,
		Targets: func(string) []schedulers.EventTarget {
			return []schedulers.EventTarget{{Scheduler: loader, Trigger: loader.Triggers[0]}}
		},
		ReserveSlots: func(domain.SchedulerTopicEvent, int) ([]*webhooks.Reservation, bool) {
			return webhooks.NoopReservations(1), true
		},
		RunTimeout: func(time.Duration) time.Duration { return time.Second },
		Run: func(_ context.Context, _ domain.Scheduler, _ *domain.SchedulerTrigger, payloadJSON, source string, _ schedulers.RunOptions, ack ...func(context.Context) error) (domain.SchedulerRunSummary, error) {
			if len(ack) > 0 && ack[0] != nil {
				_ = ack[0](ctx)
			}
			runCalled <- source + ":" + payloadJSON
			return domain.SchedulerRunSummary{}, nil
		},
	})
	dispatcher.Dispatch(domain.SchedulerTopicEvent{
		EventID:   "evt-1",
		Topic:     "topic.one",
		CreatedAt: time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC),
		Payload:   map[string]any{"value": "x"},
		Ack: func(context.Context) error {
			ackCalled = true
			return nil
		},
		Release: func() {
			released = true
		},
	})
	select {
	case got := <-runCalled:
		if !strings.Contains(got, "topic.one:") || !strings.Contains(got, `"value":"x"`) {
			t.Fatalf("run payload = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for dispatched run")
	}
	if !ackCalled || released {
		t.Fatalf("ack/released = %v/%v", ackCalled, released)
	}
	if len(store.deliveries) != 1 || store.deliveries[0].Status != domain.EventDeliveryStatusMatched {
		t.Fatalf("deliveries = %#v", store.deliveries)
	}
}

func TestEventDispatcherWebhookAndWrapperWorkflows(t *testing.T) {
	ctx := context.Background()
	loader := domain.Scheduler{Summary: domain.SchedulerSummary{ID: "loader-1", Enabled: true}, Triggers: []domain.SchedulerTrigger{{
		ID:      "trigger-1",
		Kind:    domain.SchedulerTriggerKindEvent,
		Topic:   "topic.webhook",
		Enabled: true,
	}}}
	target := schedulers.EventTarget{Scheduler: loader, Trigger: loader.Triggers[0]}

	acked := false
	released := false
	dispatcher := schedulers.NewEventDispatcher(schedulers.EventDispatcherDependencies{})
	dispatcher.AckNoSubscriber(domain.SchedulerTopicEvent{Topic: "missing", Ack: func(context.Context) error {
		acked = true
		return nil
	}})
	dispatcher.Retry(domain.SchedulerTopicEvent{Topic: "retry", Release: func() {
		released = true
	}}, "")
	if !acked || !released {
		t.Fatalf("wrapper ack/release = %v/%v", acked, released)
	}

	retryReason := ""
	dispatcher = schedulers.NewEventDispatcher(schedulers.EventDispatcherDependencies{
		RootCtx: ctx,
		Targets: func(string) []schedulers.EventTarget {
			return []schedulers.EventTarget{target}
		},
		ReserveSlots: func(domain.SchedulerTopicEvent, int) ([]*webhooks.Reservation, bool) {
			return nil, false
		},
	})
	dispatcher.Dispatch(domain.SchedulerTopicEvent{Topic: "topic.webhook", Source: domain.TopicEventSourceWebhook, CreatedAt: time.Now().UTC(), Retry: func(_ context.Context, reason string, _ time.Time) error {
		retryReason = reason
		return nil
	}})
	if retryReason != "webhook queue is full" {
		t.Fatalf("queue full retry reason = %q", retryReason)
	}

	executed := make(chan string, 1)
	webhookAcked := false
	dispatcher = schedulers.NewEventDispatcher(schedulers.EventDispatcherDependencies{
		RootCtx: ctx,
		Targets: func(string) []schedulers.EventTarget {
			return []schedulers.EventTarget{target}
		},
		ReserveSlots: func(domain.SchedulerTopicEvent, int) ([]*webhooks.Reservation, bool) {
			return webhooks.NoopReservations(1), true
		},
		RunTimeout: func(time.Duration) time.Duration { return time.Second },
		Prepare: func(_ context.Context, loader domain.Scheduler, trigger *domain.SchedulerTrigger, payloadJSON, source string, options schedulers.RunOptions) (schedulers.PreparedRun, error) {
			if !options.AlreadyEntered || source != "topic.webhook" || !strings.Contains(payloadJSON, `"topic":"topic.webhook"`) {
				t.Fatalf("Prepare source/options/payload = %q/%#v/%q", source, options, payloadJSON)
			}
			return schedulers.PreparedRun{
				Scheduler:   loader,
				Trigger:     trigger,
				Run:         domain.SchedulerRunSummary{SchedulerID: loader.Summary.ID, TriggerID: trigger.ID},
				PayloadJSON: payloadJSON,
			}, nil
		},
		Execute: func(_ context.Context, prepared schedulers.PreparedRun) (domain.SchedulerRunSummary, error) {
			executed <- prepared.Scheduler.Summary.ID + "/" + prepared.Run.TriggerID
			return domain.SchedulerRunSummary{}, nil
		},
	})
	dispatcher.Dispatch(domain.SchedulerTopicEvent{Topic: "topic.webhook", Source: domain.TopicEventSourceWebhook, CreatedAt: time.Now().UTC(), Ack: func(context.Context) error {
		webhookAcked = true
		return nil
	}})
	select {
	case got := <-executed:
		if got != "loader-1/trigger-1" {
			t.Fatalf("executed target = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for webhook execute")
	}
	if !webhookAcked {
		t.Fatalf("webhook ack was not called")
	}

	entered := 0
	left := make([]string, 0)
	retryReason = ""
	dispatcher = schedulers.NewEventDispatcher(schedulers.EventDispatcherDependencies{
		RootCtx: ctx,
		Targets: func(string) []schedulers.EventTarget {
			second := loader
			second.Summary.ID = "loader-2"
			return []schedulers.EventTarget{target, {Scheduler: second, Trigger: second.Triggers[0]}}
		},
		ReserveSlots: func(domain.SchedulerTopicEvent, int) ([]*webhooks.Reservation, bool) {
			return webhooks.NoopReservations(2), true
		},
		EnterRun: func(domain.Scheduler) bool {
			entered++
			return entered == 1
		},
		LeaveRun: func(schedulerID string) {
			left = append(left, schedulerID)
		},
	})
	dispatcher.Dispatch(domain.SchedulerTopicEvent{Topic: "topic.webhook", Source: domain.TopicEventSourceWebhook, CreatedAt: time.Now().UTC(), Retry: func(_ context.Context, reason string, _ time.Time) error {
		retryReason = reason
		return nil
	}})
	if retryReason != "loader is already running" || len(left) != 1 || left[0] != "loader-1" {
		t.Fatalf("enter retry/left = %q/%#v", retryReason, left)
	}

	aborted := make(chan string, 1)
	retryReason = ""
	dispatcher = schedulers.NewEventDispatcher(schedulers.EventDispatcherDependencies{
		RootCtx: ctx,
		Targets: func(string) []schedulers.EventTarget {
			second := loader
			second.Summary.ID = "loader-2"
			return []schedulers.EventTarget{target, {Scheduler: second, Trigger: second.Triggers[0]}}
		},
		ReserveSlots: func(domain.SchedulerTopicEvent, int) ([]*webhooks.Reservation, bool) {
			return webhooks.NoopReservations(2), true
		},
		Prepare: func(_ context.Context, loader domain.Scheduler, trigger *domain.SchedulerTrigger, payloadJSON, _ string, _ schedulers.RunOptions) (schedulers.PreparedRun, error) {
			if loader.Summary.ID == "loader-2" {
				return schedulers.PreparedRun{}, errors.New("prepare failed")
			}
			return schedulers.PreparedRun{
				Scheduler:   loader,
				Trigger:     trigger,
				Run:         domain.SchedulerRunSummary{SchedulerID: loader.Summary.ID, TriggerID: trigger.ID},
				PayloadJSON: payloadJSON,
			}, nil
		},
		Abort: func(_ context.Context, prepared schedulers.PreparedRun, reason string) {
			aborted <- prepared.Scheduler.Summary.ID + ":" + reason
		},
	})
	dispatcher.Dispatch(domain.SchedulerTopicEvent{Topic: "topic.webhook", Source: domain.TopicEventSourceWebhook, CreatedAt: time.Now().UTC(), Retry: func(_ context.Context, reason string, _ time.Time) error {
		retryReason = reason
		return nil
	}})
	select {
	case got := <-aborted:
		if got != "loader-1:prepare failed" {
			t.Fatalf("abort call = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for abort")
	}
	if retryReason != "prepare failed" {
		t.Fatalf("prepare retry reason = %q", retryReason)
	}
}

func TestSchedulerCollectDueAndDispatch(t *testing.T) {
	now := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	store := &schedulerStoreFake{}
	loader := domain.Scheduler{Summary: domain.SchedulerSummary{ID: "loader-1", Enabled: true}, Triggers: []domain.SchedulerTrigger{{
		ID:         "interval-1",
		Kind:       domain.SchedulerTriggerKindInterval,
		Enabled:    true,
		IntervalMs: 1000,
		NextFireAt: now.Add(-time.Second),
	}}}
	cached := map[string]domain.Scheduler{loader.Summary.ID: loader}
	var replaced map[string]domain.Scheduler
	runCalled := make(chan string, 1)
	scheduler := schedulers.NewScheduler(schedulers.SchedulerDependencies{
		RootCtx: context.Background(),
		Store:   store,
		Snapshot: func() map[string]domain.Scheduler {
			return cached
		},
		ReplaceCached: func(updated map[string]domain.Scheduler) {
			replaced = updated
			for id, item := range updated {
				cached[id] = item
			}
		},
		RunTimeout: func(time.Duration) time.Duration { return time.Second },
		Run: func(_ context.Context, loader domain.Scheduler, trigger *domain.SchedulerTrigger, _ string, source string, _ schedulers.RunOptions, _ ...func(context.Context) error) (domain.SchedulerRunSummary, error) {
			runCalled <- loader.Summary.ID + "/" + trigger.ID + "/" + source
			return domain.SchedulerRunSummary{}, nil
		},
	})

	jobs := scheduler.CollectDue(now)
	if len(jobs) != 1 || jobs[0].Trigger.ID != "interval-1" || jobs[0].Source != "interval:1000" {
		t.Fatalf("jobs = %#v", jobs)
	}
	if len(replaced) != 1 || len(store.fired) != 1 {
		t.Fatalf("replaced/fired = %#v/%#v", replaced, store.fired)
	}
	next, ok := scheduler.NextFireAt()
	if !ok || !next.After(now) {
		t.Fatalf("next fire = %s/%v", next, ok)
	}

	scheduler.Dispatch(jobs)
	select {
	case got := <-runCalled:
		if got != "loader-1/interval-1/interval:1000" {
			t.Fatalf("run call = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for scheduled run")
	}
}

type runStoreFake struct {
	created   []domain.SchedulerRunSummary
	updated   []domain.SchedulerRunSummary
	lastError map[string]string
}

func (s *runStoreFake) CreateSchedulerRun(_ context.Context, run domain.SchedulerRunSummary) error {
	s.created = append(s.created, run)
	return nil
}

func (s *runStoreFake) UpdateSchedulerRun(_ context.Context, run domain.SchedulerRunSummary) error {
	s.updated = append(s.updated, run)
	return nil
}

func (s *runStoreFake) UpdateSchedulerLastError(_ context.Context, schedulerID, lastError string) error {
	if s.lastError == nil {
		s.lastError = map[string]string{}
	}
	s.lastError[schedulerID] = lastError
	return nil
}

type loaderEngineFake struct {
	result schedulers.SchedulerExecutionResult
	err    error
}

func (e *loaderEngineFake) Validate(context.Context, string, string) (schedulers.SchedulerValidationResult, error) {
	return schedulers.SchedulerValidationResult{}, nil
}

func (e *loaderEngineFake) Execute(context.Context, schedulers.SchedulerExecutionRequest, schedulers.SchedulerHost) (schedulers.SchedulerExecutionResult, error) {
	return e.result, e.err
}

type runHostFake struct {
	cleanup int
}

func (h *runHostFake) Log(context.Context, string, any) error { return nil }
func (h *runHostFake) PublishEvent(context.Context, string, string) (domain.TopicEventRecord, error) {
	return domain.TopicEventRecord{}, nil
}
func (h *runHostFake) Agent(context.Context, string, domain.SchedulerAgentRequest) (domain.SchedulerAgentResult, error) {
	return domain.SchedulerAgentResult{}, nil
}
func (h *runHostFake) Command(context.Context, domain.SchedulerCommandRequest) (domain.SchedulerCommandResult, error) {
	return domain.SchedulerCommandResult{}, nil
}
func (h *runHostFake) LLM(context.Context, string, domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	return domain.SchedulerLLMResult{}, nil
}
func (h *runHostFake) StateGet(context.Context, string) (string, bool, error) { return "", false, nil }
func (h *runHostFake) StateSet(context.Context, string, string) error         { return nil }
func (h *runHostFake) StateDelete(context.Context, string) error              { return nil }
func (h *runHostFake) CallSandboxRPC(context.Context, string, string) (string, error) {
	return "", nil
}
func (h *runHostFake) CleanupCommandSessions(context.Context) { h.cleanup++ }

type eventDeliveryStoreFake struct {
	deliveries []domain.EventDelivery
}

func (s *eventDeliveryStoreFake) UpsertEventDelivery(_ context.Context, delivery domain.EventDelivery) error {
	s.deliveries = append(s.deliveries, delivery)
	return nil
}

type schedulerStoreFake struct {
	fired []domain.SchedulerTrigger
}

func (s *schedulerStoreFake) MarkSchedulerTriggerFired(_ context.Context, schedulerID, triggerID string, lastFiredAt, nextFireAt time.Time) error {
	s.fired = append(s.fired, domain.SchedulerTrigger{ID: schedulerID + "/" + triggerID, LastFiredAt: lastFiredAt, NextFireAt: nextFireAt})
	return nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
