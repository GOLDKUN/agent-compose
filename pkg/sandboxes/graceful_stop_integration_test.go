package sandboxes

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	appconfig "github.com/chaitin/agent-compose/pkg/config"
	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestIntegrationLifecycleGracefulStopPreparesBeforeDriverStop(t *testing.T) {
	sandbox := lifecycleTestSession("sandbox-graceful", "docker", domain.VMStatusRunning)
	vmState := domain.VMState{BoxID: "container-1"}
	store := &fakeLifecycleStore{session: sandbox, vmState: vmState}
	driver := &gracefulLifecycleDriver{order: []string{}}
	lifecycle := Lifecycle{
		Config: &appconfig.Config{SandboxGracefulStopTimeout: 7 * time.Second},
		Store:  store,
		Driver: driver,
		Locks:  NewLifecycleLocks(),
	}

	outcome, err := lifecycle.StopLoadedWithOptions(context.Background(), sandbox, StopOptions{Mode: StopModeGraceful})
	if err != nil {
		t.Fatalf("StopLoadedWithOptions() error = %v", err)
	}
	if outcome.Preparation.Outcome != StopPreparationGraceful || !outcome.DriverStopped {
		t.Fatalf("StopLoadedWithOptions() outcome = %#v", outcome)
	}
	if driver.gracePeriod != 7*time.Second || driver.vmState != vmState {
		t.Fatalf("preparation grace period/state = %s/%#v", driver.gracePeriod, driver.vmState)
	}
	if got, want := driver.order, []string{"prepare", "stop", "finish"}; !slices.Equal(got, want) {
		t.Fatalf("driver call order = %v, want %v", got, want)
	}
}

func TestIntegrationLifecycleForceStopInitiatesBeforeDriverStop(t *testing.T) {
	sandbox := lifecycleTestSession("sandbox-force", "docker", domain.VMStatusRunning)
	driver := &gracefulLifecycleDriver{order: []string{}}
	lifecycle := Lifecycle{
		Store:  &fakeLifecycleStore{session: sandbox},
		Driver: driver,
		Locks:  NewLifecycleLocks(),
	}

	outcome, err := lifecycle.StopLoaded(context.Background(), sandbox)
	if err != nil {
		t.Fatalf("StopLoaded() error = %v", err)
	}
	if outcome.Preparation.Outcome != StopPreparationSkipped || !outcome.DriverStopped {
		t.Fatalf("StopLoaded() outcome = %#v", outcome)
	}
	if got, want := driver.order, []string{"begin-force", "stop", "finish"}; !slices.Equal(got, want) {
		t.Fatalf("driver call order = %v, want %v", got, want)
	}
}

func TestIntegrationLifecycleLockedStopFinalizesAfterDriverFailure(t *testing.T) {
	stopErr := errors.New("transient runtime stop failure")
	sandbox := lifecycleTestSession("sandbox-locked", "docker", domain.VMStatusRunning)
	driver := &gracefulLifecycleDriver{order: []string{}, stopErr: stopErr}
	lifecycle := Lifecycle{
		Store:  &fakeLifecycleStore{session: sandbox},
		Driver: driver,
	}

	_, err := lifecycle.StopLoadedWhileLocked(context.Background(), sandbox)
	if !errors.Is(err, stopErr) {
		t.Fatalf("StopLoadedWhileLocked() error = %v, want %v", err, stopErr)
	}
	if got, want := driver.order, []string{"begin-force", "stop", "finish"}; !slices.Equal(got, want) {
		t.Fatalf("driver call order = %v, want %v", got, want)
	}
}

func TestIntegrationLifecycleGracefulPreparationFailureDoesNotFinalize(t *testing.T) {
	loadErr := errors.New("load VM state")
	sandbox := lifecycleTestSession("sandbox-preparation-failure", "docker", domain.VMStatusRunning)
	driver := &gracefulLifecycleDriver{order: []string{}}
	lifecycle := Lifecycle{
		Store:  &fakeLifecycleStore{session: sandbox, vmStateErr: loadErr},
		Driver: driver,
		Locks:  NewLifecycleLocks(),
	}

	_, err := lifecycle.StopLoadedWithOptions(context.Background(), sandbox, StopOptions{Mode: StopModeGraceful})
	if !errors.Is(err, loadErr) {
		t.Fatalf("StopLoadedWithOptions() error = %v, want %v", err, loadErr)
	}
	if len(driver.order) != 0 {
		t.Fatalf("driver calls = %v, want none", driver.order)
	}
}

func TestIntegrationLifecycleGracefulStopUsesDaemonContextAfterCancellation(t *testing.T) {
	sandbox := lifecycleTestSession("sandbox-cancelled", "docker", domain.VMStatusRunning)
	ctx, cancel := context.WithCancel(context.Background())
	driver := &cancelDuringGracefulPreparationDriver{
		gracefulLifecycleDriver: gracefulLifecycleDriver{order: []string{}},
		cancel:                  cancel,
	}
	lifecycle := Lifecycle{
		Config: &appconfig.Config{SandboxStopTimeout: time.Second},
		Store:  &fakeLifecycleStore{session: sandbox},
		Driver: driver,
		Locks:  NewLifecycleLocks(),
	}

	outcome, err := lifecycle.StopLoadedWithOptions(ctx, sandbox, StopOptions{Mode: StopModeGraceful})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StopLoadedWithOptions() error = %v, want request cancellation", err)
	}
	if !outcome.DriverStopped || driver.stopCtxErr != nil {
		t.Fatalf("stop outcome/context = %#v/%v, want stopped with a live daemon context", outcome, driver.stopCtxErr)
	}
}

type cancelDuringGracefulPreparationDriver struct {
	gracefulLifecycleDriver
	cancel     context.CancelFunc
	stopCtxErr error
}

func (d *cancelDuringGracefulPreparationDriver) PrepareSandboxStop(context.Context, *domain.Sandbox, domain.VMState, time.Duration) (StopPreparationResult, error) {
	d.order = append(d.order, "prepare")
	d.cancel()
	return StopPreparationResult{Outcome: StopPreparationFailed, Error: context.Canceled}, nil
}

func (d *cancelDuringGracefulPreparationDriver) StopSandboxVM(ctx context.Context, session *domain.Sandbox) error {
	d.stopCtxErr = ctx.Err()
	if d.stopCtxErr != nil {
		return d.stopCtxErr
	}
	return d.gracefulLifecycleDriver.StopSandboxVM(ctx, session)
}

type gracefulLifecycleDriver struct {
	order       []string
	vmState     domain.VMState
	gracePeriod time.Duration
	stopErr     error
}

func (d *gracefulLifecycleDriver) StartSandboxVM(context.Context, *domain.Sandbox) error {
	return nil
}

func (d *gracefulLifecycleDriver) StopSandboxVM(context.Context, *domain.Sandbox) error {
	d.order = append(d.order, "stop")
	return d.stopErr
}

func (d *gracefulLifecycleDriver) PrepareSandboxStop(_ context.Context, _ *domain.Sandbox, vmState domain.VMState, gracePeriod time.Duration) (StopPreparationResult, error) {
	d.order = append(d.order, "prepare")
	d.vmState = vmState
	d.gracePeriod = gracePeriod
	return StopPreparationResult{Outcome: StopPreparationGraceful}, nil
}

func (d *gracefulLifecycleDriver) BeginSandboxForceStop(*domain.Sandbox) error {
	d.order = append(d.order, "begin-force")
	return nil
}

func (d *gracefulLifecycleDriver) FinishSandboxStop(*domain.Sandbox) {
	d.order = append(d.order, "finish")
}
