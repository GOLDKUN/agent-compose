package schedulers

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
)

func TestRunExecutorCancellationWritesCanceledTerminalState(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	store := &cancelRunStore{}
	engine := &cancelRunEngine{started: make(chan struct{})}
	var events []string
	artifactsDir := t.TempDir()
	executor := NewRunExecutor(RunExecutorDependencies{
		Store:       store,
		Engine:      engine,
		HostFactory: func(domain.Scheduler, RuntimeExecutionContext, TriggerEventMetadata) RunHost { return nil },
		ArtifactsDir: func(schedulerID, runID string) string {
			return filepath.Join(artifactsDir, schedulerID, runID)
		},
		WriteArtifact: func(string, string, string) error { return nil },
		AddSchedulerEvent: func(_ context.Context, event SchedulerEventInput) error {
			eventType := event.EventType
			events = append(events, eventType)
			return nil
		},
	})
	result := make(chan domain.SchedulerRunSummary, 1)
	errResult := make(chan error, 1)
	go func() {
		run, err := executor.Run(ctx, RunTriggerRequest{
			Scheduler: domain.Scheduler{
				Summary: domain.SchedulerSummary{ID: "scheduler-1", Runtime: domain.SchedulerRuntimeScheduler},
				Script:  "function main() {}",
			},
			PayloadJSON: `{}`,
			Source:      "manual",
		})
		result <- run
		errResult <- err
	}()

	<-engine.started
	cancel(errors.New("user stop"))
	run := <-result
	if err := <-errResult; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Status != domain.SchedulerRunStatusCanceled || run.Error != "user stop" {
		t.Fatalf("run = %#v", run)
	}
	if len(store.updated) != 1 || store.updated[0].Status != domain.SchedulerRunStatusCanceled {
		t.Fatalf("updated runs = %#v", store.updated)
	}
	if !slices.Contains(events, "scheduler.run.canceled") || slices.Contains(events, "scheduler.run.failed") {
		t.Fatalf("events = %#v", events)
	}
}

func TestSchedulerRunSupervisorRunReturnsFinalResult(t *testing.T) {
	store := newSupervisorRunStore()
	supervisor := newSchedulerRunSupervisor(schedulerRunSupervisorDependencies{
		RootCtx: context.Background(),
		Store:   store,
		LoadSchedulerForRun: func(context.Context, string, string) (domain.Scheduler, *domain.SchedulerTrigger, error) {
			return domain.Scheduler{Summary: domain.SchedulerSummary{ID: "scheduler-1"}}, nil, nil
		},
		Prepare: func(_ context.Context, req RunTriggerRequest) (PreparedRun, error) {
			scheduler := req.Scheduler
			return PreparedRun{Scheduler: scheduler, Run: domain.SchedulerRunSummary{ID: "run-success", SchedulerID: scheduler.Summary.ID, Status: domain.SchedulerRunStatusRunning}}, nil
		},
		Execute: func(_ context.Context, prepared PreparedRun) (domain.SchedulerRunSummary, error) {
			run := prepared.Run
			run.Status = domain.SchedulerRunStatusSucceeded
			run.ResultJSON = `{"ok":true}`
			store.set(run)
			return run, nil
		},
	})

	run, err := supervisor.RunScheduler(context.Background(), SchedulerRunRequest{SchedulerID: "scheduler-1", TriggerID: "trigger-1"})
	if err != nil || run.Status != domain.SchedulerRunStatusSucceeded || run.ResultJSON != `{"ok":true}` {
		t.Fatalf("Run run=%#v err=%v", run, err)
	}
}

func TestSchedulerRunSupervisorRejectsEmptyTriggerWithoutPreparingRun(t *testing.T) {
	prepareCalls := 0
	supervisor := newSchedulerRunSupervisor(schedulerRunSupervisorDependencies{
		LoadSchedulerForRun: func(context.Context, string, string) (domain.Scheduler, *domain.SchedulerTrigger, error) {
			t.Fatal("empty trigger must be rejected before loading")
			return domain.Scheduler{}, nil, nil
		},
		Prepare: func(context.Context, RunTriggerRequest) (PreparedRun, error) {
			prepareCalls++
			return PreparedRun{}, nil
		},
	})
	if _, err := supervisor.RunScheduler(context.Background(), SchedulerRunRequest{SchedulerID: "scheduler-1"}); !errors.Is(err, domain.ErrRequired) || prepareCalls != 0 {
		t.Fatalf("empty trigger err=%v prepareCalls=%d", err, prepareCalls)
	}
}

func TestSchedulerRunSupervisorTimeoutCancelsExecution(t *testing.T) {
	store := newSupervisorRunStore()
	supervisor := newSchedulerRunSupervisor(schedulerRunSupervisorDependencies{
		RootCtx: context.Background(),
		Store:   store,
		LoadSchedulerForRun: func(context.Context, string, string) (domain.Scheduler, *domain.SchedulerTrigger, error) {
			return domain.Scheduler{Summary: domain.SchedulerSummary{ID: "scheduler-1"}}, nil, nil
		},
		Prepare: func(_ context.Context, req RunTriggerRequest) (PreparedRun, error) {
			scheduler := req.Scheduler
			return PreparedRun{Scheduler: scheduler, Run: domain.SchedulerRunSummary{ID: "run-timeout", SchedulerID: scheduler.Summary.ID, Status: domain.SchedulerRunStatusRunning}}, nil
		},
		Execute: func(ctx context.Context, prepared PreparedRun) (domain.SchedulerRunSummary, error) {
			<-ctx.Done()
			run := prepared.Run
			run.Status = domain.SchedulerRunStatusCanceled
			run.Error = context.Cause(ctx).Error()
			store.set(run)
			return run, nil
		},
	})

	run, err := supervisor.RunScheduler(context.Background(), SchedulerRunRequest{SchedulerID: "scheduler-1", TriggerID: "trigger-1", Timeout: 10 * time.Millisecond})
	if err != nil || run.Status != domain.SchedulerRunStatusCanceled || run.Error != errSchedulerRunTimedOut.Error() {
		t.Fatalf("Run run=%#v err=%v", run, err)
	}
}

func TestSchedulerRunSupervisorStopWaitsForExecutorTerminalState(t *testing.T) {
	store := newSupervisorRunStore()
	started := make(chan struct{})
	supervisor := newSchedulerRunSupervisor(schedulerRunSupervisorDependencies{
		RootCtx: context.Background(),
		Store:   store,
		LoadSchedulerForRun: func(context.Context, string, string) (domain.Scheduler, *domain.SchedulerTrigger, error) {
			return domain.Scheduler{Summary: domain.SchedulerSummary{ID: "scheduler-1"}}, nil, nil
		},
		Prepare: func(_ context.Context, req RunTriggerRequest) (PreparedRun, error) {
			scheduler, payloadJSON, source := req.Scheduler, req.PayloadJSON, req.Source
			run := domain.SchedulerRunSummary{ID: "run-1", SchedulerID: scheduler.Summary.ID, Status: domain.SchedulerRunStatusRunning, PayloadJSON: payloadJSON, TriggerSource: source}
			store.set(run)
			return PreparedRun{Scheduler: scheduler, Run: run, PayloadJSON: payloadJSON}, nil
		},
		Execute: func(ctx context.Context, prepared PreparedRun) (domain.SchedulerRunSummary, error) {
			close(started)
			<-ctx.Done()
			run := prepared.Run
			run.Status = domain.SchedulerRunStatusCanceled
			run.Error = context.Cause(ctx).Error()
			store.set(run)
			return run, nil
		},
	})

	created, err := supervisor.StartSchedulerRun(context.Background(), SchedulerRunRequest{SchedulerID: "scheduler-1", TriggerID: "trigger-1", PayloadJSON: `{"key":true}`})
	if err != nil || created.Status != domain.SchedulerRunStatusRunning {
		t.Fatalf("Start run=%#v err=%v", created, err)
	}
	<-started
	stopped, requested, err := supervisor.StopSchedulerRun(context.Background(), "scheduler-1", created.ID, "user stop")
	if err != nil || !requested || stopped.Status != domain.SchedulerRunStatusCanceled || stopped.Error != "user stop" {
		t.Fatalf("Stop run=%#v requested=%v err=%v", stopped, requested, err)
	}
	current, requested, err := supervisor.StopSchedulerRun(context.Background(), "scheduler-1", created.ID, "stop again")
	if err != nil || requested || current.Status != domain.SchedulerRunStatusCanceled || current.Error != "user stop" {
		t.Fatalf("second Stop run=%#v requested=%v err=%v", current, requested, err)
	}
	runs, err := supervisor.ListSchedulerRuns(context.Background(), "scheduler-1", 10)
	if err != nil || len(runs) != 1 || runs[0].ID != created.ID {
		t.Fatalf("List runs=%#v err=%v", runs, err)
	}
}

func TestSchedulerRunSupervisorStopsQJSPendingPromise(t *testing.T) {
	store := newSupervisorRunStore()
	host := &pendingPromiseRunHost{
		engineCancellationHost: engineCancellationHost{started: make(chan struct{})},
	}
	var (
		eventMu sync.Mutex
		events  []string
	)
	artifactsDir := t.TempDir()
	executor := NewRunExecutor(RunExecutorDependencies{
		Store:  store,
		Engine: &QJSSchedulerEngine{},
		HostFactory: func(domain.Scheduler, RuntimeExecutionContext, TriggerEventMetadata) RunHost {
			return host
		},
		ArtifactsDir: func(schedulerID, runID string) string {
			return filepath.Join(artifactsDir, schedulerID, runID)
		},
		WriteArtifact: func(string, string, string) error { return nil },
		AddSchedulerEvent: func(_ context.Context, event SchedulerEventInput) error {
			eventType := event.EventType
			eventMu.Lock()
			defer eventMu.Unlock()
			events = append(events, eventType)
			return nil
		},
	})
	scheduler := domain.Scheduler{
		Summary: domain.SchedulerSummary{ID: "scheduler-qjs-pending", Runtime: domain.SchedulerRuntimeScheduler},
		Script: `
scheduler.interval("pending", async function pending() {
  scheduler.log("callback started");
  await new Promise(function neverResolve() {});
}, 86400000);`,
	}
	trigger := &domain.SchedulerTrigger{ID: "pending", Kind: "interval"}
	supervisor := newSchedulerRunSupervisor(schedulerRunSupervisorDependencies{
		RootCtx: context.Background(),
		Store:   store,
		LoadSchedulerForRun: func(context.Context, string, string) (domain.Scheduler, *domain.SchedulerTrigger, error) {
			return scheduler, trigger, nil
		},
		Prepare: executor.Prepare,
		Execute: executor.Execute,
	})

	created, err := supervisor.StartSchedulerRun(context.Background(), SchedulerRunRequest{
		SchedulerID: scheduler.Summary.ID,
		TriggerID:   trigger.ID,
	})
	if err != nil || created.Status != domain.SchedulerRunStatusRunning {
		t.Fatalf("Start run=%#v err=%v", created, err)
	}
	select {
	case <-host.started:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler callback did not start")
	}

	stopCtx, cancelStop := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelStop()
	stopped, requested, err := supervisor.StopSchedulerRun(stopCtx, scheduler.Summary.ID, created.ID, "operator stop")
	if err != nil || !requested {
		t.Fatalf("Stop run=%#v requested=%v err=%v", stopped, requested, err)
	}
	if stopped.Status != domain.SchedulerRunStatusCanceled || stopped.Error != "operator stop" || stopped.CompletedAt.IsZero() {
		t.Fatalf("stopped run = %#v", stopped)
	}
	eventMu.Lock()
	defer eventMu.Unlock()
	if !slices.Equal(events, []string{"scheduler.run.started", "scheduler.run.canceled"}) {
		t.Fatalf("events = %#v", events)
	}
}

func TestSchedulerRunSupervisorRootContextStopsBackgroundRun(t *testing.T) {
	root, cancelRoot := context.WithCancel(context.Background())
	store := newSupervisorRunStore()
	started := make(chan struct{})
	completed := make(chan struct{})
	supervisor := newSchedulerRunSupervisor(schedulerRunSupervisorDependencies{
		RootCtx: root,
		Store:   store,
		LoadSchedulerForRun: func(context.Context, string, string) (domain.Scheduler, *domain.SchedulerTrigger, error) {
			return domain.Scheduler{Summary: domain.SchedulerSummary{ID: "scheduler-1"}}, nil, nil
		},
		Prepare: func(_ context.Context, req RunTriggerRequest) (PreparedRun, error) {
			scheduler := req.Scheduler
			run := domain.SchedulerRunSummary{ID: "run-root", SchedulerID: scheduler.Summary.ID, Status: domain.SchedulerRunStatusRunning}
			store.set(run)
			return PreparedRun{Scheduler: scheduler, Run: run}, nil
		},
		Execute: func(ctx context.Context, prepared PreparedRun) (domain.SchedulerRunSummary, error) {
			close(started)
			<-ctx.Done()
			run := prepared.Run
			run.Status = domain.SchedulerRunStatusCanceled
			run.Error = context.Cause(ctx).Error()
			store.set(run)
			close(completed)
			return run, nil
		},
	})

	if _, err := supervisor.StartSchedulerRun(context.Background(), SchedulerRunRequest{SchedulerID: "scheduler-1", TriggerID: "trigger-1"}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	<-started
	cancelRoot()
	<-completed
	run, err := supervisor.GetSchedulerRun(context.Background(), "scheduler-1", "run-root")
	if err != nil || run.Status != domain.SchedulerRunStatusCanceled || run.Error != context.Canceled.Error() {
		t.Fatalf("Get run=%#v err=%v", run, err)
	}
}

type cancelRunEngine struct {
	started chan struct{}
}

func (e *cancelRunEngine) Validate(context.Context, string, string) (SchedulerValidationResult, error) {
	return SchedulerValidationResult{}, nil
}

func (e *cancelRunEngine) Execute(ctx context.Context, _ SchedulerExecutionRequest, _ SchedulerHost) (SchedulerExecutionResult, error) {
	close(e.started)
	<-ctx.Done()
	return SchedulerExecutionResult{}, ctx.Err()
}

type cancelRunStore struct {
	created   []domain.SchedulerRunSummary
	updated   []domain.SchedulerRunSummary
	lastError string
}

func (s *cancelRunStore) CreateSchedulerRun(_ context.Context, run domain.SchedulerRunSummary) error {
	s.created = append(s.created, run)
	return nil
}

func (s *cancelRunStore) UpdateSchedulerRun(_ context.Context, run domain.SchedulerRunSummary) error {
	s.updated = append(s.updated, run)
	return nil
}

func (s *cancelRunStore) UpdateSchedulerLastError(_ context.Context, _ string, lastError string) error {
	s.lastError = lastError
	return nil
}

type supervisorRunStore struct {
	mu   sync.Mutex
	runs map[string]domain.SchedulerRunSummary
}

type pendingPromiseRunHost struct {
	engineCancellationHost
}

func (*pendingPromiseRunHost) CleanupCommandSessions(context.Context) {}

func newSupervisorRunStore() *supervisorRunStore {
	return &supervisorRunStore{runs: map[string]domain.SchedulerRunSummary{}}
}

func (s *supervisorRunStore) set(run domain.SchedulerRunSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.SchedulerID+"/"+run.ID] = run
}

func (s *supervisorRunStore) CreateSchedulerRun(_ context.Context, run domain.SchedulerRunSummary) error {
	s.set(run)
	return nil
}

func (s *supervisorRunStore) UpdateSchedulerRun(_ context.Context, run domain.SchedulerRunSummary) error {
	s.set(run)
	return nil
}

func (*supervisorRunStore) UpdateSchedulerLastError(context.Context, string, string) error {
	return nil
}

func (s *supervisorRunStore) GetSchedulerRun(_ context.Context, schedulerID, runID string) (domain.SchedulerRunSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[schedulerID+"/"+runID]
	if !ok {
		return domain.SchedulerRunSummary{}, domain.ResourceError(domain.ErrNotFound, "scheduler run", schedulerID+"/"+runID, "scheduler run not found", nil)
	}
	return run, nil
}

func (s *supervisorRunStore) ListSchedulerRuns(_ context.Context, schedulerID string, limit int) ([]domain.SchedulerRunSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := make([]domain.SchedulerRunSummary, 0, len(s.runs))
	for _, run := range s.runs {
		if run.SchedulerID == schedulerID {
			runs = append(runs, run)
		}
	}
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}
