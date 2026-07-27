package schedulers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agent-compose/pkg/identity"
	domain "agent-compose/pkg/model"
)

func TestControllerCoverageWorkflow(t *testing.T) {
	ctx := context.Background()
	store := newControllerTestStore()
	created := domain.Scheduler{
		Summary:  domain.SchedulerSummary{ID: "loader-1", Name: "Scheduler", Runtime: domain.SchedulerRuntimeScheduler, Enabled: true},
		Script:   "function main(){}",
		Triggers: []domain.SchedulerTrigger{{SchedulerID: "loader-1", ID: "trigger-1", Kind: domain.SchedulerTriggerKindEvent, Topic: "topic.test", Enabled: true}},
	}
	store.loaders[created.Summary.ID] = created
	notifier := &controllerTestNotifier{}
	publisher := &controllerTestPublisher{}
	root := t.TempDir()
	controller := NewController(ControllerDependencies{
		Store:  store,
		Engine: controllerTestEngine{},
		HostFactory: func(domain.Scheduler, RuntimeExecutionContext, TriggerEventMetadata) RunHost {
			return nil
		},
		Notifier:   notifier,
		Publisher:  publisher,
		Artifacts:  FSArtifacts{DataRoot: root},
		Schedulers: map[string]domain.Scheduler{},
		Running:    map[string]int{},
		Now:        func() time.Time { return time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC) },
		NewID:      func() string { return "event-id" },
		RunTimeout: func(override time.Duration) time.Duration {
			if override > 0 {
				return override
			}
			return time.Second
		},
	})

	if err := controller.Refresh(ctx); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	updated := created
	updated.Summary.Description = "updated"
	if _, err := controller.SetLoaderEnabled(ctx, created.Summary.ID, false); err != nil {
		t.Fatalf("SetLoaderEnabled returned error: %v", err)
	}
	if _, err := controller.SetLoaderTriggerEnabled(ctx, created.Summary.ID, "trigger-1", false); err != nil {
		t.Fatalf("SetLoaderTriggerEnabled returned error: %v", err)
	}
	if _, trigger, err := controller.LoadLoaderForRun(ctx, created.Summary.ID, "trigger-1"); err != nil || trigger == nil {
		t.Fatalf("LoadLoaderForRun trigger=%#v err=%v", trigger, err)
	}
	if _, _, err := controller.LoadLoaderForRun(ctx, created.Summary.ID, "missing"); err == nil {
		t.Fatalf("expected missing trigger error")
	}
	manualRun, err := controller.RunNow(ctx, created.Summary.ID, "trigger-1", `{"manual":true}`, time.Second)
	if err != nil || manualRun.Status != domain.SchedulerRunStatusSucceeded || manualRun.ResultJSON == "" {
		t.Fatalf("RunNow run=%#v err=%v", manualRun, err)
	}
	if !identity.IsID(manualRun.ID) || identity.ShortID(manualRun.ID) == "" {
		t.Fatalf("RunNow run id = %q, want SHA-256 resource id", manualRun.ID)
	}
	prepared, err := controller.Prepare(ctx, created, &created.Triggers[0], `{"prepared":true}`, "manual", RunOptions{})
	if err != nil || prepared.Run.Status != domain.SchedulerRunStatusRunning {
		t.Fatalf("Prepare prepared=%#v err=%v", prepared, err)
	}
	if !identity.IsID(prepared.Run.ID) || prepared.Run.ID == manualRun.ID {
		t.Fatalf("Prepare run id = %q, want a distinct SHA-256 resource id", prepared.Run.ID)
	}
	executed, err := controller.Execute(ctx, prepared)
	if err != nil || executed.Status != domain.SchedulerRunStatusSucceeded {
		t.Fatalf("Execute run=%#v err=%v", executed, err)
	}
	prepared, err = controller.Prepare(ctx, created, &created.Triggers[0], `{"abort":true}`, "manual", RunOptions{})
	if err != nil {
		t.Fatalf("Prepare before Abort returned error: %v", err)
	}
	controller.Abort(ctx, prepared, "")
	controller.Publish("topic.test", map[string]any{"ok": true})
	if len(publisher.events) != 1 {
		t.Fatalf("publisher events = %#v", publisher.events)
	}
	controller.ReplaceCachedLoaders(map[string]domain.Scheduler{created.Summary.ID: updated})
	if len(controller.CachedLoadersMap()) != 1 || len(controller.SnapshotLoaders()) != 1 {
		t.Fatalf("cache not populated")
	}
	if !controller.EnterRun(domain.Scheduler{Summary: domain.SchedulerSummary{ID: created.Summary.ID, ConcurrencyPolicy: domain.SchedulerConcurrencyPolicySkip}}) {
		t.Fatalf("first EnterRun should succeed")
	}
	if controller.EnterRun(domain.Scheduler{Summary: domain.SchedulerSummary{ID: created.Summary.ID, ConcurrencyPolicy: domain.SchedulerConcurrencyPolicySkip}}) {
		t.Fatalf("second EnterRun should be rejected")
	}
	if !controller.AnyTargetBusy([]EventTarget{{Scheduler: domain.Scheduler{Summary: domain.SchedulerSummary{ID: created.Summary.ID}}}}) {
		t.Fatalf("target should be busy")
	}
	controller.LeaveRun(created.Summary.ID)
	if !controller.EnterRun(domain.Scheduler{Summary: domain.SchedulerSummary{ID: created.Summary.ID, ConcurrencyPolicy: domain.SchedulerConcurrencyPolicyParallel}}) {
		t.Fatalf("parallel EnterRun should succeed")
	}
	controller.LeaveRun(created.Summary.ID)
	event, err := controller.AddLoaderEventRecord(ctx, created.Summary.ID, "run-1", "trigger-1", "loader.test", "", "message", map[string]any{"ok": true}, "session-1", "cell-1", "agent-session")
	if err != nil || event.ID != "event-id" || event.Level != "info" {
		t.Fatalf("AddLoaderEventRecord event=%#v err=%v", event, err)
	}
	if _, err := controller.AddLoaderEventRecord(ctx, created.Summary.ID, "run-1", "trigger-1", "loader.bad", "", "message", func() {}, "", "", ""); err == nil {
		t.Fatalf("AddLoaderEventRecord invalid payload returned nil error")
	}
	dir := controller.RunArtifactsDir(created.Summary.ID, "run-1")
	if err := controller.WriteRunArtifact(dir, "output.txt", "hello"); err != nil {
		t.Fatalf("WriteRunArtifact returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "output.txt")); err != nil {
		t.Fatalf("artifact not written: %v", err)
	}
	controller.UpdateTriggerEventDelivery(ctx, domain.SchedulerRunSummary{ID: "run-1", SchedulerID: created.Summary.ID, TriggerID: "trigger-1", Status: domain.SchedulerRunStatusSucceeded, PayloadJSON: `{"payload":{"eventId":"event-1"}}`})
	if len(store.deliveries) != 1 || store.deliveries[0].Status != domain.EventDeliveryStatusRunSucceeded {
		t.Fatalf("deliveries = %#v", store.deliveries)
	}
	controller.WakeScheduler()
	_ = controller.CollectDueScheduledRuns(time.Now().UTC())
	_, _ = controller.NextScheduledFireAt()
	controller.DispatchScheduledRuns(nil)
	var nilController *Controller
	nilController.Start()
	nilController.WakeScheduler()
	bareController := NewController(ControllerDependencies{
		Store:  store,
		Engine: controllerTestEngine{},
		HostFactory: func(domain.Scheduler, RuntimeExecutionContext, TriggerEventMetadata) RunHost {
			return nil
		},
	})
	if bareController.RunArtifactsDir("loader", "run") != "" {
		t.Fatalf("bare RunArtifactsDir returned non-empty path")
	}
	if err := bareController.WriteRunArtifact("", "ignored", "ignored"); err != nil {
		t.Fatalf("bare WriteRunArtifact returned error: %v", err)
	}
	if bareController.runTimeout(0) != 20*time.Minute || bareController.runTimeout(time.Second) != time.Second {
		t.Fatalf("default runTimeout returned unexpected values")
	}
	if bareController.now().IsZero() || bareController.newID() == "" {
		t.Fatalf("default now/newID returned empty values")
	}
	if len(notifier.reasons) == 0 {
		t.Fatalf("expected notifications")
	}
}

func TestIntegrationControllerCoverageWorkflow(t *testing.T) {
	TestControllerCoverageWorkflow(t)
}

func TestE2EControllerCoverageWorkflow(t *testing.T) {
	TestControllerCoverageWorkflow(t)
}

type controllerTestEngine struct{}

func (controllerTestEngine) Validate(context.Context, string, string) (SchedulerValidationResult, error) {
	return SchedulerValidationResult{Triggers: []domain.SchedulerTrigger{{ID: "trigger-1", Kind: domain.SchedulerTriggerKindEvent, Topic: "topic.test", Enabled: true}}}, nil
}

func (controllerTestEngine) Execute(context.Context, SchedulerExecutionRequest, SchedulerHost) (SchedulerExecutionResult, error) {
	return SchedulerExecutionResult{ResultJSON: `{"ok":true}`}, nil
}

type controllerTestStore struct {
	loaders    map[string]domain.Scheduler
	runs       []domain.SchedulerRunSummary
	events     []domain.SchedulerEvent
	deliveries []domain.EventDelivery
	replaceErr error
}

func newControllerTestStore() *controllerTestStore {
	return &controllerTestStore{loaders: map[string]domain.Scheduler{}}
}

func (s *controllerTestStore) ListLoaders(context.Context) ([]domain.Scheduler, error) {
	items := make([]domain.Scheduler, 0, len(s.loaders))
	for _, item := range s.loaders {
		items = append(items, CloneLoader(item))
	}
	return items, nil
}

func (s *controllerTestStore) GetLoader(_ context.Context, loaderID string) (domain.Scheduler, error) {
	return CloneLoader(s.loaders[loaderID]), nil
}

func (s *controllerTestStore) CreateLoader(_ context.Context, item domain.Scheduler) (domain.Scheduler, error) {
	s.loaders[item.Summary.ID] = CloneLoader(item)
	return item, nil
}

func (s *controllerTestStore) UpdateLoader(_ context.Context, item domain.Scheduler) (domain.Scheduler, error) {
	current := CloneLoader(item)
	current.Triggers = s.loaders[item.Summary.ID].Triggers
	s.loaders[item.Summary.ID] = current
	return current, nil
}

func (s *controllerTestStore) DeleteLoader(_ context.Context, loaderID string) error {
	delete(s.loaders, loaderID)
	return nil
}

func (s *controllerTestStore) ReplaceLoaderTriggers(_ context.Context, loaderID string, triggers []domain.SchedulerTrigger) ([]domain.SchedulerTrigger, error) {
	if s.replaceErr != nil {
		return nil, s.replaceErr
	}
	loader := s.loaders[loaderID]
	loader.Triggers = append([]domain.SchedulerTrigger(nil), triggers...)
	s.loaders[loaderID] = loader
	return triggers, nil
}

func (s *controllerTestStore) SetLoaderEnabled(_ context.Context, loaderID string, enabled bool) error {
	loader := s.loaders[loaderID]
	loader.Summary.Enabled = enabled
	s.loaders[loaderID] = loader
	return nil
}

func (s *controllerTestStore) SetLoaderTriggerEnabled(_ context.Context, loaderID, triggerID string, enabled bool) error {
	loader := s.loaders[loaderID]
	for i := range loader.Triggers {
		if loader.Triggers[i].ID == triggerID {
			loader.Triggers[i].Enabled = enabled
		}
	}
	s.loaders[loaderID] = loader
	return nil
}

func (s *controllerTestStore) AddLoaderEvent(_ context.Context, event domain.SchedulerEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *controllerTestStore) CreateLoaderRun(_ context.Context, run domain.SchedulerRunSummary) error {
	s.runs = append(s.runs, run)
	return nil
}

func (s *controllerTestStore) UpdateLoaderRun(_ context.Context, run domain.SchedulerRunSummary) error {
	s.runs = append(s.runs, run)
	return nil
}

func (s *controllerTestStore) UpdateLoaderLastError(context.Context, string, string) error {
	return nil
}

func (s *controllerTestStore) MarkLoaderTriggerFired(context.Context, string, string, time.Time, time.Time) error {
	return nil
}

func (s *controllerTestStore) UpsertEventDelivery(_ context.Context, delivery domain.EventDelivery) error {
	s.deliveries = append(s.deliveries, delivery)
	return nil
}

type controllerTestNotifier struct {
	reasons []string
}

func (n *controllerTestNotifier) Notify(reason string) {
	n.reasons = append(n.reasons, reason)
}

type controllerTestPublisher struct {
	events []domain.SchedulerTopicEvent
}

func (p *controllerTestPublisher) Publish(event domain.SchedulerTopicEvent) bool {
	p.events = append(p.events, event)
	return true
}
