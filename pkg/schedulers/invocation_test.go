package schedulers

import (
	"context"
	"errors"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestInvocationExecutorUsesEphemeralContextAndSharedConcurrencyGate(t *testing.T) {
	engine := &invocationEngineFake{result: SchedulerExecutionResult{ResultJSON: `{"ok":true}`, Warnings: []string{"warning"}}}
	host := &invocationHostFake{}
	entered := 0
	left := 0
	var execution RuntimeExecutionContext
	executor := NewInvocationExecutor(InvocationExecutorDependencies{
		Engine: engine,
		HostFactory: func(_ domain.Scheduler, current RuntimeExecutionContext, _ TriggerEventMetadata) RunHost {
			execution = current
			return host
		},
		EnterRun: func(domain.Scheduler) bool { entered++; return true },
		LeaveRun: func(string) { left++ },
		NewID:    func() string { return "invocation-correlation" },
	})
	scheduler := domain.Scheduler{Summary: domain.SchedulerSummary{ID: "scheduler-1", Runtime: domain.SchedulerRuntimeScheduler}, Script: "function main() {}"}
	result, err := executor.Invoke(context.Background(), scheduler, ` { "value" : true } `)
	if err != nil || result.ResultJSON != `{"ok":true}` || len(result.Warnings) != 1 {
		t.Fatalf("Invoke result=%#v err=%v", result, err)
	}
	if execution.ID != "invocation-correlation" || execution.TriggerID != "" || execution.Kind != ExecutionKindInvocation {
		t.Fatalf("execution context=%#v", execution)
	}
	if engine.request.PayloadJSON != `{"value":true}` || entered != 1 || left != 1 || host.cleanupCalls != 1 {
		t.Fatalf("request/gate/cleanup=%#v/%d/%d/%d", engine.request, entered, left, host.cleanupCalls)
	}
}

func TestInvocationExecutorBusyAndFailureDoNotCreateRunLifecycle(t *testing.T) {
	scheduler := domain.Scheduler{Summary: domain.SchedulerSummary{ID: "scheduler-1", Runtime: domain.SchedulerRuntimeScheduler}, Script: "function main() {}"}
	busy := NewInvocationExecutor(InvocationExecutorDependencies{
		Engine: &invocationEngineFake{}, HostFactory: func(domain.Scheduler, RuntimeExecutionContext, TriggerEventMetadata) RunHost {
			return &invocationHostFake{}
		},
		EnterRun: func(domain.Scheduler) bool { return false },
	})
	if _, err := busy.Invoke(context.Background(), scheduler, `{}`); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("busy error=%v", err)
	}
	host := &invocationHostFake{}
	left := 0
	failed := NewInvocationExecutor(InvocationExecutorDependencies{
		Engine: &invocationEngineFake{err: errors.New("script failed")}, HostFactory: func(domain.Scheduler, RuntimeExecutionContext, TriggerEventMetadata) RunHost { return host },
		EnterRun: func(domain.Scheduler) bool { return true }, LeaveRun: func(string) { left++ },
	})
	if _, err := failed.Invoke(context.Background(), scheduler, `{}`); err == nil || err.Error() != "script failed" || left != 1 || host.cleanupCalls != 1 {
		t.Fatalf("failure err=%v left=%d cleanup=%d", err, left, host.cleanupCalls)
	}
}

func TestInvocationExecutorFallsBackWhenIDGeneratorReturnsEmpty(t *testing.T) {
	var execution RuntimeExecutionContext
	executor := NewInvocationExecutor(InvocationExecutorDependencies{
		Engine: &invocationEngineFake{},
		HostFactory: func(_ domain.Scheduler, current RuntimeExecutionContext, _ TriggerEventMetadata) RunHost {
			execution = current
			return &invocationHostFake{}
		},
		NewID: func() string { return " " },
	})
	scheduler := domain.Scheduler{Summary: domain.SchedulerSummary{ID: "scheduler-1", Runtime: domain.SchedulerRuntimeScheduler}, Script: "function main() {}"}
	if _, err := executor.Invoke(context.Background(), scheduler, `{}`); err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if execution.ID == "" {
		t.Fatal("invocation execution context has an empty correlation ID")
	}
}

func TestInvocationExecutorPreservesSuccessfulResultWhenContextIsCanceledAfterExecution(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	stopCause := errors.New("late stop request")
	engine := &invocationEngineFake{
		result: SchedulerExecutionResult{ResultJSON: `{"ok":true}`, Warnings: []string{"preserved"}},
		afterExecute: func() {
			cancel(stopCause)
		},
	}
	executor := NewInvocationExecutor(InvocationExecutorDependencies{
		Engine: engine,
		HostFactory: func(domain.Scheduler, RuntimeExecutionContext, TriggerEventMetadata) RunHost {
			return &invocationHostFake{}
		},
	})
	scheduler := domain.Scheduler{Summary: domain.SchedulerSummary{ID: "scheduler-1", Runtime: domain.SchedulerRuntimeScheduler}, Script: "function main() {}"}

	result, err := executor.Invoke(ctx, scheduler, `{}`)
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if result.ResultJSON != `{"ok":true}` || len(result.Warnings) != 1 || result.Warnings[0] != "preserved" {
		t.Fatalf("Invoke result = %#v", result)
	}
}

type invocationEngineFake struct {
	request      SchedulerExecutionRequest
	result       SchedulerExecutionResult
	err          error
	afterExecute func()
}

func (e *invocationEngineFake) Validate(context.Context, string, string) (SchedulerValidationResult, error) {
	return SchedulerValidationResult{}, nil
}

func (e *invocationEngineFake) Execute(_ context.Context, request SchedulerExecutionRequest, _ SchedulerHost) (SchedulerExecutionResult, error) {
	e.request = request
	if e.afterExecute != nil {
		e.afterExecute()
	}
	return e.result, e.err
}

type invocationHostFake struct{ cleanupCalls int }

func (*invocationHostFake) Log(context.Context, string, any) error { return nil }
func (*invocationHostFake) PublishEvent(context.Context, string, string) (domain.TopicEventRecord, error) {
	return domain.TopicEventRecord{}, nil
}
func (*invocationHostFake) Agent(context.Context, string, domain.SchedulerAgentRequest) (domain.SchedulerAgentResult, error) {
	return domain.SchedulerAgentResult{}, nil
}
func (*invocationHostFake) Command(context.Context, domain.SchedulerCommandRequest) (domain.SchedulerCommandResult, error) {
	return domain.SchedulerCommandResult{}, nil
}
func (*invocationHostFake) LLM(context.Context, string, domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	return domain.SchedulerLLMResult{}, nil
}
func (*invocationHostFake) StateGet(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (*invocationHostFake) StateSet(context.Context, string, string) error { return nil }
func (*invocationHostFake) StateDelete(context.Context, string) error      { return nil }
func (*invocationHostFake) CallSandboxRPC(context.Context, string, string) (string, error) {
	return "", nil
}
func (h *invocationHostFake) CleanupCommandSessions(context.Context) { h.cleanupCalls++ }
