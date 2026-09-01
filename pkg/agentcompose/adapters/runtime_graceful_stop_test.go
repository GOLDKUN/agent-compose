package adapters

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chaitin/agent-compose/pkg/driver"
	domain "github.com/chaitin/agent-compose/pkg/model"
	"github.com/chaitin/agent-compose/pkg/sandboxes"
)

func TestSandboxExecutionsBlockAndRelease(t *testing.T) {
	executions := newSandboxExecutions()
	ctx, _, finish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil || ctx == nil {
		t.Fatalf("begin() ctx=%v err=%v", ctx, err)
	}
	executions.block("sandbox-1")
	if _, _, _, err := executions.begin(context.Background(), "sandbox-1"); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("begin() while blocked error = %v, want ErrFailedPrecondition", err)
	}
	finish()
	if err := executions.wait(context.Background(), "sandbox-1"); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
	executions.release("sandbox-1")
	_, _, finish, err = executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatalf("begin() after release error = %v", err)
	}
	finish()
}

func TestOverlappingSandboxStopsKeepExecutionGateClosedUntilBothFinish(t *testing.T) {
	executions := newSandboxExecutions()
	adapter := driverRuntimeAdapter{runtime: &gracefulStopRuntimeFake{exec: successfulControlExec}, executions: executions}
	sandbox := testGracefulSandbox()

	for stop := 0; stop < 2; stop++ {
		result, err := adapter.PrepareSandboxStop(context.Background(), sandbox, domain.VMState{}, time.Second)
		if err != nil || result.Outcome != sandboxes.StopPreparationGraceful {
			t.Fatalf("PrepareSandboxStop() stop %d result=%#v err=%v", stop+1, result, err)
		}
	}

	if _, err := adapter.StopSandbox(context.Background(), sandbox, domain.VMState{}); err != nil {
		t.Fatalf("first StopSandbox() error = %v", err)
	}
	adapter.FinishSandboxStop(sandbox)
	if _, _, _, err := executions.begin(context.Background(), sandbox.Summary.ID); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("begin() after first stop finished error = %v, want ErrFailedPrecondition", err)
	}

	if _, err := adapter.StopSandbox(context.Background(), sandbox, domain.VMState{}); err != nil {
		t.Fatalf("second StopSandbox() error = %v", err)
	}
	adapter.FinishSandboxStop(sandbox)
	_, _, finish, err := executions.begin(context.Background(), sandbox.Summary.ID)
	if err != nil {
		t.Fatalf("begin() after both stops finished error = %v", err)
	}
	finish()
}

func TestEnsureSandboxDoesNotReopenGateDuringGracefulStop(t *testing.T) {
	executions := newSandboxExecutions()
	_, _, finish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	finishOnce := sync.OnceFunc(finish)
	t.Cleanup(finishOnce)
	controlStarted := make(chan struct{})
	allowSignal := make(chan struct{})
	adapter := driverRuntimeAdapter{
		runtime: &gracefulStopRuntimeFake{signal: func(context.Context, string) error {
			close(controlStarted)
			<-allowSignal
			finishOnce()
			return nil
		}},
		executions: executions,
	}
	sandbox := testGracefulSandbox()
	resultCh := make(chan sandboxes.StopPreparationResult, 1)
	go func() {
		result, _ := adapter.PrepareSandboxStop(context.Background(), sandbox, domain.VMState{}, time.Second)
		resultCh <- result
	}()
	<-controlStarted

	if _, err := adapter.EnsureSandbox(context.Background(), sandbox, domain.VMState{}, domain.ProxyState{}); err != nil {
		t.Fatalf("EnsureSandbox() error = %v", err)
	}
	if _, _, _, err := executions.begin(context.Background(), sandbox.Summary.ID); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("begin() during graceful stop after ensure error = %v, want ErrFailedPrecondition", err)
	}
	close(allowSignal)
	if result := <-resultCh; result.Outcome != sandboxes.StopPreparationGraceful {
		t.Fatalf("PrepareSandboxStop() result = %#v", result)
	}
	adapter.FinishSandboxStop(sandbox)
}

func TestPrepareSandboxStopWaitsForGracefulExecutionExit(t *testing.T) {
	executions := newSandboxExecutions()
	_, _, finish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &gracefulStopRuntimeFake{signal: func(context.Context, string) error {
		finish()
		return nil
	}}
	adapter := driverRuntimeAdapter{runtime: runtime, executions: executions}

	result, err := adapter.PrepareSandboxStop(context.Background(), testGracefulSandbox(), domain.VMState{}, time.Second)
	if err != nil || result.Outcome != sandboxes.StopPreparationGraceful {
		t.Fatalf("PrepareSandboxStop() result=%#v err=%v", result, err)
	}
	if runtime.signalCalls.Load() != 1 {
		t.Fatalf("managed execution signal calls = %d, want 1", runtime.signalCalls.Load())
	}
}

func TestPrepareSandboxStopRetriesUntilRuntimeShutdownIsReady(t *testing.T) {
	executions := newSandboxExecutions()
	execCtx, _, finish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	finishOnce := sync.OnceFunc(finish)
	go func() {
		<-execCtx.Done()
		finishOnce()
	}()
	controlCalls := 0
	runtime := &gracefulStopRuntimeFake{signal: func(context.Context, string) error {
		controlCalls++
		if controlCalls == 1 {
			return driver.ErrGuestRuntimeNotReady
		}
		finishOnce()
		return nil
	}}
	adapter := driverRuntimeAdapter{runtime: runtime, executions: executions}

	result, err := adapter.PrepareSandboxStop(context.Background(), testGracefulSandbox(), domain.VMState{}, 250*time.Millisecond)
	if err != nil || result.Outcome != sandboxes.StopPreparationGraceful {
		t.Fatalf("PrepareSandboxStop() result=%#v err=%v", result, err)
	}
	if controlCalls != 2 {
		t.Fatalf("signal control exec calls = %d, want 2", controlCalls)
	}
}

func TestGuestRuntimeSignalRetryDelayIsExponentiallyBounded(t *testing.T) {
	want := []time.Duration{
		50 * time.Millisecond,
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		500 * time.Millisecond,
		500 * time.Millisecond,
	}
	for attempt, wantDelay := range want {
		if got := guestRuntimeSignalRetryDelay(attempt); got != wantDelay {
			t.Fatalf("guestRuntimeSignalRetryDelay(%d) = %s, want %s", attempt, got, wantDelay)
		}
	}
}

func TestPrepareSandboxStopSignalsManagedExecutionsConcurrently(t *testing.T) {
	executions := newSandboxExecutions()
	_, firstID, firstFinish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	_, secondID, secondFinish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	finishes := map[string]func(){firstID: firstFinish, secondID: secondFinish}
	started := make(chan string, 2)
	release := make(chan struct{})
	runtime := &gracefulStopRuntimeFake{signal: func(_ context.Context, executionID string) error {
		started <- executionID
		<-release
		finishes[executionID]()
		return nil
	}}
	adapter := driverRuntimeAdapter{runtime: runtime, executions: executions}
	resultCh := make(chan sandboxes.StopPreparationResult, 1)
	go func() {
		result, _ := adapter.PrepareSandboxStop(context.Background(), testGracefulSandbox(), domain.VMState{}, time.Second)
		resultCh <- result
	}()

	seen := map[string]bool{}
	for range 2 {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(250 * time.Millisecond):
			t.Fatal("managed execution signals were serialized")
		}
	}
	if !seen[firstID] || !seen[secondID] {
		t.Fatalf("signaled execution IDs = %v, want both active IDs", seen)
	}
	close(release)
	if result := <-resultCh; result.Outcome != sandboxes.StopPreparationGraceful {
		t.Fatalf("PrepareSandboxStop() result = %#v", result)
	}
}

func TestPrepareSandboxStopTimeoutCancelsExecutionWithoutWaitingForIt(t *testing.T) {
	executions := newSandboxExecutions()
	execCtx, _, finish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finish)
	adapter := driverRuntimeAdapter{
		runtime:    &gracefulStopRuntimeFake{},
		executions: executions,
	}

	result, err := adapter.PrepareSandboxStop(context.Background(), testGracefulSandbox(), domain.VMState{}, 5*time.Millisecond)
	if err != nil || result.Outcome != sandboxes.StopPreparationTimeout || !errors.Is(result.Error, errGracefulStopTimedOut) {
		t.Fatalf("PrepareSandboxStop() result=%#v err=%v", result, err)
	}
	if execCtx.Err() == nil {
		t.Fatal("execution context was not cancelled after grace period")
	}
}

func TestPrepareSandboxStopPreservesRequestCancellation(t *testing.T) {
	executions := newSandboxExecutions()
	execCtx, _, finish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finish)
	controlStarted := make(chan struct{})
	adapter := driverRuntimeAdapter{
		runtime: &gracefulStopRuntimeFake{signal: func(ctx context.Context, _ string) error {
			close(controlStarted)
			<-ctx.Done()
			return ctx.Err()
		}},
		executions: executions,
	}
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan sandboxes.StopPreparationResult, 1)
	go func() {
		result, _ := adapter.PrepareSandboxStop(ctx, testGracefulSandbox(), domain.VMState{}, time.Hour)
		resultCh <- result
	}()
	<-controlStarted
	cancel()

	select {
	case result := <-resultCh:
		if result.Outcome != sandboxes.StopPreparationFailed || !errors.Is(result.Error, context.Canceled) {
			t.Fatalf("PrepareSandboxStop() result = %#v, want cancelled failure", result)
		}
	case <-time.After(time.Second):
		t.Fatal("PrepareSandboxStop() ignored request cancellation")
	}
	if execCtx.Err() == nil {
		t.Fatal("execution context was not cancelled with stop request")
	}
}

func TestPrepareSandboxStopSignalErrorForcesExecution(t *testing.T) {
	executions := newSandboxExecutions()
	execCtx, _, finish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		<-execCtx.Done()
		finish()
	}()
	wantErr := errors.New("control exec failed")
	adapter := driverRuntimeAdapter{
		runtime: &gracefulStopRuntimeFake{signal: func(context.Context, string) error {
			return wantErr
		}},
		executions: executions,
	}

	result, err := adapter.PrepareSandboxStop(context.Background(), testGracefulSandbox(), domain.VMState{}, time.Second)
	if err != nil || result.Outcome != sandboxes.StopPreparationFailed || !errors.Is(result.Error, wantErr) {
		t.Fatalf("PrepareSandboxStop() result=%#v err=%v", result, err)
	}
}

func TestPrepareSandboxStopIntegrationSignalUsesGraceDeadline(t *testing.T) {
	executions := newSandboxExecutions()
	execCtx, _, finish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		<-execCtx.Done()
		finish()
	}()
	adapter := driverRuntimeAdapter{
		runtime: &gracefulStopRuntimeFake{signal: func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		}},
		executions: executions,
	}

	result, err := adapter.PrepareSandboxStop(context.Background(), testGracefulSandbox(), domain.VMState{}, 5*time.Millisecond)
	if err != nil || result.Outcome != sandboxes.StopPreparationTimeout || !errors.Is(result.Error, errGracefulStopTimedOut) {
		t.Fatalf("PrepareSandboxStop() result=%#v err=%v", result, err)
	}
	if err := executions.wait(context.Background(), "sandbox-1"); err != nil {
		t.Fatalf("active execution remained after signal timeout: %v", err)
	}
}

func TestBeginSandboxForceStopCancelsActiveExecutionAndClosesGate(t *testing.T) {
	executions := newSandboxExecutions()
	execCtx, _, finish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	adapter := driverRuntimeAdapter{runtime: &gracefulStopRuntimeFake{exec: successfulControlExec}, executions: executions}
	adapter.BeginSandboxForceStop(testGracefulSandbox())

	select {
	case <-execCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("force stop did not cancel the active execution")
	}
	if _, _, _, err := executions.begin(context.Background(), "sandbox-1"); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("begin() after force stop error = %v, want ErrFailedPrecondition", err)
	}
	finish()
}

func TestTrackedRuntimeInteractionFinishesOnEOF(t *testing.T) {
	finished := 0
	interaction := &trackedRuntimeInteraction{
		RuntimeInteraction: eofRuntimeInteraction{},
		finish:             func() { finished++ },
		done:               make(chan struct{}),
	}
	if _, err := interaction.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv() error = %v, want EOF", err)
	}
	if _, err := interaction.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if finished != 1 {
		t.Fatalf("finish calls = %d, want 1", finished)
	}
}

func TestTrackedRuntimeInteractionFinishesOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	interaction := &trackedRuntimeInteraction{
		RuntimeInteraction: eofRuntimeInteraction{},
		finish:             func() { close(finished) },
		done:               make(chan struct{}),
	}
	go interaction.finishWhenContextEnds(ctx)
	cancel()

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("interaction remained tracked after its context was cancelled")
	}
}

func TestRuntimeAdapterKeepsExecutionGateClosedUntilStopFinalizedAndAcrossEnsure(t *testing.T) {
	executions := newSandboxExecutions()
	adapter := driverRuntimeAdapter{runtime: &gracefulStopRuntimeFake{exec: successfulControlExec}, executions: executions}
	sandbox := testGracefulSandbox()
	if _, err := adapter.StopSandbox(context.Background(), sandbox, domain.VMState{}); err != nil {
		t.Fatalf("StopSandbox() error = %v", err)
	}
	if _, _, _, err := executions.begin(context.Background(), sandbox.Summary.ID); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("execution gate after driver stop error = %v, want ErrFailedPrecondition", err)
	}
	adapter.FinishSandboxStop(sandbox)
	_, _, finish, err := executions.begin(context.Background(), sandbox.Summary.ID)
	if err != nil {
		t.Fatalf("begin() after stop finalization error = %v", err)
	}
	finish()

	executions.block(sandbox.Summary.ID)
	executions.block(sandbox.Summary.ID)
	if _, err := adapter.EnsureSandbox(context.Background(), sandbox, domain.VMState{}, domain.ProxyState{}); err != nil {
		t.Fatalf("EnsureSandbox() error = %v", err)
	}
	if _, _, _, err := executions.begin(context.Background(), sandbox.Summary.ID); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("begin() after ensure error = %v, want ErrFailedPrecondition", err)
	}
	executions.release(sandbox.Summary.ID)
	if _, _, _, err := executions.begin(context.Background(), sandbox.Summary.ID); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("begin() after first stop finalization error = %v, want ErrFailedPrecondition", err)
	}
	executions.release(sandbox.Summary.ID)
	_, _, finish, err = executions.begin(context.Background(), sandbox.Summary.ID)
	if err != nil {
		t.Fatalf("begin() after both stops finalized error = %v", err)
	}
	finish()
}

func TestRuntimeAdapterPreservesStopDeadlineForRuntimeContainment(t *testing.T) {
	executions := newSandboxExecutions()
	_, _, finish, err := executions.begin(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &gracefulStopRuntimeFake{exec: successfulControlExec}
	adapter := driverRuntimeAdapter{runtime: runtime, executions: executions}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	executionFinished := make(chan struct{})
	go func() {
		<-ctx.Done()
		finish()
		close(executionFinished)
	}()

	if _, err := adapter.StopSandbox(ctx, testGracefulSandbox(), domain.VMState{}); err != nil {
		t.Fatalf("StopSandbox() error = %v", err)
	}
	if runtime.stopContextErr != nil {
		t.Fatalf("runtime stop received exhausted context: %v", runtime.stopContextErr)
	}
	cancel()
	select {
	case <-executionFinished:
	case <-time.After(time.Second):
		t.Fatal("active execution did not finish after stop context cancellation")
	}
}

func successfulControlExec(driver.ExecSpec) (driver.ExecResult, error) {
	return driver.ExecResult{Success: true}, nil
}

func testGracefulSandbox() *domain.Sandbox {
	return &domain.Sandbox{Summary: domain.SandboxSummary{ID: "sandbox-1"}}
}

type gracefulStopRuntimeFake struct {
	exec           func(driver.ExecSpec) (driver.ExecResult, error)
	execContext    func(context.Context, driver.ExecSpec) (driver.ExecResult, error)
	execCalls      int
	signal         func(context.Context, string) error
	signalCalls    atomic.Int32
	stopContextErr error
}

func (*gracefulStopRuntimeFake) EnsureSandbox(context.Context, *driver.Sandbox, driver.VMState, driver.ProxyState) (driver.SandboxVMInfo, error) {
	return driver.SandboxVMInfo{}, nil
}

func (r *gracefulStopRuntimeFake) StopSandbox(ctx context.Context, _ *driver.Sandbox, _ driver.VMState) (bool, error) {
	r.stopContextErr = ctx.Err()
	return false, nil
}

func (*gracefulStopRuntimeFake) RemoveSandbox(context.Context, *driver.Sandbox, driver.VMState) error {
	return nil
}

func (r *gracefulStopRuntimeFake) Exec(ctx context.Context, _ *driver.Sandbox, _ driver.VMState, spec driver.ExecSpec) (driver.ExecResult, error) {
	r.execCalls++
	if r.execContext != nil {
		return r.execContext(ctx, spec)
	}
	return r.exec(spec)
}

func (r *gracefulStopRuntimeFake) ExecStream(ctx context.Context, sandbox *driver.Sandbox, state driver.VMState, spec driver.ExecSpec, _ driver.ExecStreamWriter) (driver.ExecResult, error) {
	return r.Exec(ctx, sandbox, state, spec)
}

func (r *gracefulStopRuntimeFake) SignalGuestRuntime(ctx context.Context, _ *driver.Sandbox, _ driver.VMState, executionID string, signal driver.RuntimeSignal) error {
	if signal != driver.RuntimeSignalTerminate {
		return errors.New("unexpected managed execution signal")
	}
	r.signalCalls.Add(1)
	if r.signal != nil {
		return r.signal(ctx, executionID)
	}
	return nil
}

type eofRuntimeInteraction struct{}

func (eofRuntimeInteraction) Send(driver.RuntimeInputFrame) error { return nil }
func (eofRuntimeInteraction) CloseSend() error                    { return nil }
func (eofRuntimeInteraction) Recv() (driver.RuntimeOutputFrame, error) {
	return driver.RuntimeOutputFrame{}, io.EOF
}
func (eofRuntimeInteraction) Wait() (driver.RuntimeResult, error) { return driver.RuntimeResult{}, nil }
