package schedulers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"agent-compose/pkg/identity"
	domain "agent-compose/pkg/model"
)

type RunStore interface {
	CreateSchedulerRun(ctx context.Context, run domain.SchedulerRunSummary) error
	UpdateSchedulerRun(ctx context.Context, run domain.SchedulerRunSummary) error
	UpdateSchedulerLastError(ctx context.Context, schedulerID, lastError string) error
}

type RunHost interface {
	SchedulerHost
	CleanupCommandSessions(ctx context.Context)
}

type ExecutionKind string

const (
	ExecutionKindTrigger    ExecutionKind = "trigger"
	ExecutionKindInvocation ExecutionKind = "invocation"
)

type RuntimeExecutionContext struct {
	ID        string
	TriggerID string
	Kind      ExecutionKind
}

type RunHostFactory func(scheduler domain.Scheduler, execution RuntimeExecutionContext, triggerEvent TriggerEventMetadata) RunHost

type RunOptions struct {
	RetryWhenBusy  bool
	AlreadyEntered bool
}

type PreparedRun struct {
	Scheduler   domain.Scheduler
	Trigger     *domain.SchedulerTrigger
	Run         domain.SchedulerRunSummary
	PayloadJSON string
}

type RunExecutorDependencies struct {
	Store                      RunStore
	Engine                     SchedulerEngine
	HostFactory                RunHostFactory
	ArtifactsDir               func(schedulerID, runID string) string
	WriteArtifact              func(dir, name, content string) error
	EnterRun                   func(scheduler domain.Scheduler) bool
	LeaveRun                   func(schedulerID string)
	AddSchedulerEvent          func(ctx context.Context, schedulerID, runID, triggerID, eventType, level, message string, payload any, linkedSandboxID, linkedCellID, linkedAgentThreadID string) error
	UpdateTriggerEventDelivery func(ctx context.Context, run domain.SchedulerRunSummary)
	Notify                     func(reason string)
	Refresh                    func(ctx context.Context) error
}

type RunExecutor struct {
	deps RunExecutorDependencies
}

var ErrRunBusyForRetry = errors.New("scheduler is already running")

func NewRunExecutor(deps RunExecutorDependencies) *RunExecutor {
	return &RunExecutor{deps: deps}
}

func (e *RunExecutor) Run(ctx context.Context, scheduler domain.Scheduler, trigger *domain.SchedulerTrigger, payloadJSON, source string, options RunOptions, triggerEventAck ...func(context.Context) error) (domain.SchedulerRunSummary, error) {
	prepared, err := e.Prepare(ctx, scheduler, trigger, payloadJSON, source, options)
	if err != nil {
		return domain.SchedulerRunSummary{}, err
	}
	if len(triggerEventAck) > 0 && triggerEventAck[0] != nil {
		if err := triggerEventAck[0](ctx); err != nil {
			slog.Warn("failed to mark scheduler topic event published", "topic", source, "error", err)
		}
	}
	return e.Execute(ctx, prepared)
}

func (e *RunExecutor) Prepare(ctx context.Context, scheduler domain.Scheduler, trigger *domain.SchedulerTrigger, payloadJSON, source string, options RunOptions) (PreparedRun, error) {
	payloadJSON, err := domain.NormalizeJSONDocument(payloadJSON)
	if err != nil {
		if options.AlreadyEntered {
			e.leaveRun(scheduler.Summary.ID)
		}
		return PreparedRun{}, err
	}
	now := time.Now().UTC()
	run := domain.SchedulerRunSummary{
		ID:               identity.NewRandomID(identity.ResourceRun),
		SchedulerID:      scheduler.Summary.ID,
		TriggerSource:    strings.TrimSpace(source),
		Status:           domain.SchedulerRunStatusRunning,
		StartedAt:        now,
		PayloadJSON:      payloadJSON,
		SourceScriptHash: domain.SchedulerSourceSHA(scheduler.Script),
		ArtifactsDir:     e.artifactsDir(scheduler.Summary.ID, ""),
	}
	if trigger != nil {
		run.TriggerID = trigger.ID
		run.TriggerKind = trigger.Kind
	}
	run.ArtifactsDir = e.artifactsDir(scheduler.Summary.ID, run.ID)

	entered := options.AlreadyEntered
	if !entered && !e.enterRun(scheduler) {
		if options.RetryWhenBusy {
			return PreparedRun{}, ErrRunBusyForRetry
		}
		if err := os.MkdirAll(run.ArtifactsDir, 0o755); err != nil {
			return PreparedRun{}, fmt.Errorf("create scheduler run artifacts dir: %w", err)
		}
		_ = e.writeArtifact(run.ArtifactsDir, "payload.json", payloadJSON)
		run.Status = domain.SchedulerRunStatusSkipped
		run.CompletedAt = now
		run.Error = "scheduler is already running"
		if err := e.deps.Store.CreateSchedulerRun(ctx, run); err != nil {
			return PreparedRun{}, err
		}
		e.updateTriggerEventDelivery(ctx, run)
		e.notify("scheduler_run_updated")
		_ = e.deps.Store.UpdateSchedulerLastError(ctx, scheduler.Summary.ID, run.Error)
		_ = e.addSchedulerEvent(ctx, scheduler.Summary.ID, run.ID, run.TriggerID, "scheduler.run.skipped", "warn", run.Error, nil, "", "", "")
		_ = e.writeArtifact(run.ArtifactsDir, "error.txt", run.Error)
		return PreparedRun{Scheduler: scheduler, Trigger: trigger, Run: run, PayloadJSON: payloadJSON}, nil
	}

	if err := os.MkdirAll(run.ArtifactsDir, 0o755); err != nil {
		e.leaveRun(scheduler.Summary.ID)
		return PreparedRun{}, fmt.Errorf("create scheduler run artifacts dir: %w", err)
	}
	_ = e.writeArtifact(run.ArtifactsDir, "payload.json", payloadJSON)

	if err := e.deps.Store.CreateSchedulerRun(ctx, run); err != nil {
		e.leaveRun(scheduler.Summary.ID)
		return PreparedRun{}, err
	}
	e.updateTriggerEventDelivery(ctx, run)
	e.notify("scheduler_run_updated")
	_ = e.addSchedulerEvent(ctx, scheduler.Summary.ID, run.ID, run.TriggerID, "scheduler.run.started", "info", "scheduler run started", map[string]any{"source": run.TriggerSource}, "", "", "")
	return PreparedRun{Scheduler: scheduler, Trigger: trigger, Run: run, PayloadJSON: payloadJSON}, nil
}

func (e *RunExecutor) Execute(ctx context.Context, prepared PreparedRun) (domain.SchedulerRunSummary, error) {
	if prepared.Run.Status == domain.SchedulerRunStatusSkipped {
		return prepared.Run, nil
	}
	defer e.leaveRun(prepared.Scheduler.Summary.ID)
	run := prepared.Run
	host := e.deps.HostFactory(prepared.Scheduler, RuntimeExecutionContext{
		ID:        run.ID,
		TriggerID: run.TriggerID,
		Kind:      ExecutionKindTrigger,
	}, ParseTriggerEventMetadata(prepared.PayloadJSON))
	execution, execErr := e.deps.Engine.Execute(ctx, SchedulerExecutionRequest{
		Runtime:     prepared.Scheduler.Summary.Runtime,
		Script:      prepared.Scheduler.Script,
		Trigger:     prepared.Trigger,
		PayloadJSON: prepared.PayloadJSON,
	}, host)

	writeCtx := context.WithoutCancel(ctx)
	if host != nil {
		host.CleanupCommandSessions(writeCtx)
	}
	for _, warning := range execution.Warnings {
		_ = e.addSchedulerEvent(writeCtx, prepared.Scheduler.Summary.ID, run.ID, run.TriggerID, "scheduler.deprecated_alias.warning", "warning", warning, nil, "", "", "")
	}

	completedAt := time.Now().UTC()
	run.CompletedAt = completedAt
	run.DurationMs = completedAt.Sub(run.StartedAt).Milliseconds()
	if cancelCause := context.Cause(ctx); cancelCause != nil {
		run.Status = domain.SchedulerRunStatusCanceled
		run.Error = cancelCause.Error()
		_ = e.writeArtifact(run.ArtifactsDir, "error.txt", run.Error)
		_ = e.deps.Store.UpdateSchedulerLastError(writeCtx, prepared.Scheduler.Summary.ID, run.Error)
		_ = e.addSchedulerEvent(writeCtx, prepared.Scheduler.Summary.ID, run.ID, run.TriggerID, "scheduler.run.canceled", "warn", run.Error, nil, "", "", "")
	} else if execErr != nil {
		run.Status = domain.SchedulerRunStatusFailed
		run.Error = execErr.Error()
		_ = e.writeArtifact(run.ArtifactsDir, "error.txt", run.Error)
		_ = e.deps.Store.UpdateSchedulerLastError(writeCtx, prepared.Scheduler.Summary.ID, run.Error)
		_ = e.addSchedulerEvent(writeCtx, prepared.Scheduler.Summary.ID, run.ID, run.TriggerID, "scheduler.run.failed", "error", run.Error, nil, "", "", "")
	} else {
		run.Status = domain.SchedulerRunStatusSucceeded
		run.ResultJSON = execution.ResultJSON
		if execution.ResultJSON != "" {
			_ = e.writeArtifact(run.ArtifactsDir, "result.json", execution.ResultJSON)
		}
		_ = e.deps.Store.UpdateSchedulerLastError(writeCtx, prepared.Scheduler.Summary.ID, "")
		_ = e.addSchedulerEvent(writeCtx, prepared.Scheduler.Summary.ID, run.ID, run.TriggerID, "scheduler.run.completed", "info", "scheduler run completed", map[string]any{"resultJson": execution.ResultJSON}, "", "", "")
	}
	if err := e.deps.Store.UpdateSchedulerRun(writeCtx, run); err != nil {
		return domain.SchedulerRunSummary{}, err
	}
	e.updateTriggerEventDelivery(writeCtx, run)
	e.notify("scheduler_run_updated")
	if e.deps.Refresh != nil {
		if err := e.deps.Refresh(writeCtx); err != nil {
			slog.Warn("failed to refresh schedulers after run", "scheduler_id", prepared.Scheduler.Summary.ID, "error", err)
		}
	}
	return run, nil
}

func (e *RunExecutor) Abort(ctx context.Context, prepared PreparedRun, reason string) {
	if prepared.Run.Status == domain.SchedulerRunStatusSkipped {
		return
	}
	defer e.leaveRun(prepared.Scheduler.Summary.ID)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "scheduler run aborted before execution"
	}
	run := prepared.Run
	completedAt := time.Now().UTC()
	run.Status = domain.SchedulerRunStatusFailed
	run.CompletedAt = completedAt
	run.DurationMs = completedAt.Sub(run.StartedAt).Milliseconds()
	run.Error = reason
	_ = e.writeArtifact(run.ArtifactsDir, "error.txt", run.Error)
	_ = e.deps.Store.UpdateSchedulerLastError(ctx, prepared.Scheduler.Summary.ID, run.Error)
	_ = e.addSchedulerEvent(ctx, prepared.Scheduler.Summary.ID, run.ID, run.TriggerID, "scheduler.run.failed", "error", run.Error, nil, "", "", "")
	if err := e.deps.Store.UpdateSchedulerRun(ctx, run); err != nil {
		slog.Warn("failed to abort prepared scheduler run", "scheduler_id", prepared.Scheduler.Summary.ID, "run_id", run.ID, "error", err)
	}
	e.updateTriggerEventDelivery(ctx, run)
	e.notify("scheduler_run_updated")
}

func (e *RunExecutor) artifactsDir(schedulerID, runID string) string {
	if e.deps.ArtifactsDir == nil {
		return ""
	}
	return e.deps.ArtifactsDir(schedulerID, runID)
}

func (e *RunExecutor) writeArtifact(dir, name, content string) error {
	if e.deps.WriteArtifact == nil {
		return nil
	}
	return e.deps.WriteArtifact(dir, name, content)
}

func (e *RunExecutor) enterRun(scheduler domain.Scheduler) bool {
	if e.deps.EnterRun == nil {
		return true
	}
	return e.deps.EnterRun(scheduler)
}

func (e *RunExecutor) leaveRun(schedulerID string) {
	if e.deps.LeaveRun != nil {
		e.deps.LeaveRun(schedulerID)
	}
}

func (e *RunExecutor) addSchedulerEvent(ctx context.Context, schedulerID, runID, triggerID, eventType, level, message string, payload any, linkedSandboxID, linkedCellID, linkedAgentThreadID string) error {
	if e.deps.AddSchedulerEvent == nil {
		return nil
	}
	return e.deps.AddSchedulerEvent(ctx, schedulerID, runID, triggerID, eventType, level, message, payload, linkedSandboxID, linkedCellID, linkedAgentThreadID)
}

func (e *RunExecutor) updateTriggerEventDelivery(ctx context.Context, run domain.SchedulerRunSummary) {
	if e.deps.UpdateTriggerEventDelivery != nil {
		e.deps.UpdateTriggerEventDelivery(ctx, run)
	}
}

func (e *RunExecutor) notify(reason string) {
	if e.deps.Notify != nil {
		e.deps.Notify(reason)
	}
}
