package sandboxes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sandboxes"
	"agent-compose/pkg/storage/sandboxstore"
)

type releaseRecoveryDriver struct {
	started        atomic.Int32
	stopped        atomic.Int32
	released       atomic.Int32
	removed        atomic.Int32
	releaseStarted chan struct{}
	allowRelease   chan struct{}
	removeEntered  chan struct{}
}

func (d *releaseRecoveryDriver) StartSandboxVM(context.Context, *domain.Sandbox) error {
	d.started.Add(1)
	return nil
}

func (d *releaseRecoveryDriver) StopSandboxVM(context.Context, *domain.Sandbox) error {
	d.stopped.Add(1)
	return nil
}

func (d *releaseRecoveryDriver) ReleaseSandboxRuntime(context.Context, *domain.Sandbox) error {
	d.released.Add(1)
	if d.releaseStarted != nil {
		select {
		case <-d.releaseStarted:
		default:
			close(d.releaseStarted)
		}
	}
	if d.allowRelease != nil {
		<-d.allowRelease
	}
	return nil
}

func (d *releaseRecoveryDriver) RemoveSandboxVM(context.Context, *domain.Sandbox) error {
	d.removed.Add(1)
	if d.removeEntered != nil {
		close(d.removeEntered)
	}
	return nil
}

type releaseWorkspaceEnsurer struct{}

func (releaseWorkspaceEnsurer) Ensure(context.Context, *domain.Sandbox) error { return nil }

func TestIntegrationLifecycleRecoversInterruptedStoppedRuntimeRelease(t *testing.T) {
	config, store, sandbox := newReleaseRecoverySandbox(t)
	driver := &releaseRecoveryDriver{}
	lifecycle := sandboxes.Lifecycle{Config: config, Store: store, Driver: driver, Locks: sandboxes.NewLifecycleLocks()}

	if warnings := lifecycle.RecoverStoppedRuntimeReleases(context.Background()); len(warnings) != 0 {
		t.Fatalf("RecoverStoppedRuntimeReleases warnings = %#v", warnings)
	}
	loaded, err := store.GetSandbox(context.Background(), sandbox.Summary.ID)
	if err != nil {
		t.Fatalf("GetSandbox returned error: %v", err)
	}
	if loaded.Summary.VMStatus != domain.VMStatusStopped || domain.EffectiveStoppedRuntimeState(loaded) != domain.StoppedRuntimeStateReleased ||
		driver.stopped.Load() != 1 || driver.released.Load() != 1 {
		t.Fatalf("sandbox=%#v release=%#v calls=%d/%d", loaded.Summary, loaded.StoppedRuntime, driver.stopped.Load(), driver.released.Load())
	}
}

func TestIntegrationLifecycleRecoversRuntimeRecreatedBeforeRunningStatePersisted(t *testing.T) {
	config, store, sandbox := newReleaseRecoverySandbox(t)
	now := time.Now().UTC()
	sandbox.Summary.VMStatus = domain.VMStatusStopped
	sandbox.StoppedRuntime = &domain.StoppedRuntime{
		State: domain.StoppedRuntimeStateReleased, RequestedAt: now.Add(-2 * time.Minute), ReleasedAt: now.Add(-time.Minute),
	}
	if err := store.UpdateSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("UpdateSandbox returned error: %v", err)
	}
	vmState, err := store.GetVMState(sandbox.Summary.ID)
	if err != nil {
		t.Fatalf("GetVMState returned error: %v", err)
	}
	vmState.BoxID = "recreated-runtime"
	vmState.StartedAt = now
	vmState.StoppedAt = now.Add(-time.Minute)
	if err := store.SaveVMState(sandbox.Summary.ID, vmState); err != nil {
		t.Fatalf("SaveVMState returned error: %v", err)
	}
	driver := &releaseRecoveryDriver{}
	lifecycle := sandboxes.Lifecycle{Config: config, Store: store, Driver: driver, Locks: sandboxes.NewLifecycleLocks()}

	if warnings := lifecycle.RecoverStoppedRuntimeReleases(context.Background()); len(warnings) != 0 {
		t.Fatalf("RecoverStoppedRuntimeReleases warnings = %#v", warnings)
	}
	loaded, err := store.GetSandbox(context.Background(), sandbox.Summary.ID)
	if err != nil {
		t.Fatalf("GetSandbox returned error: %v", err)
	}
	if loaded.Summary.VMStatus != domain.VMStatusStopped || domain.EffectiveStoppedRuntimeState(loaded) != domain.StoppedRuntimeStateReleased ||
		driver.stopped.Load() != 1 || driver.released.Load() != 1 {
		t.Fatalf("sandbox=%#v release=%#v calls=%d/%d", loaded.Summary, loaded.StoppedRuntime, driver.stopped.Load(), driver.released.Load())
	}
}

func TestLifecycleSerializesRuntimeReleaseAgainstResume(t *testing.T) {
	config, store, sandbox := newReleaseRecoverySandbox(t)
	driver := &releaseRecoveryDriver{releaseStarted: make(chan struct{}), allowRelease: make(chan struct{})}
	locks := sandboxes.NewLifecycleLocks()
	lifecycle := sandboxes.Lifecycle{Config: config, Store: store, Driver: driver, WorkspaceEnsurer: releaseWorkspaceEnsurer{}, Locks: locks}
	stopDone := make(chan error, 1)
	go func() {
		_, _, err := lifecycle.StopLoaded(context.Background(), sandbox)
		stopDone <- err
	}()
	select {
	case <-driver.releaseStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime release did not start")
	}
	resumeDone := make(chan error, 1)
	go func() {
		_, err := lifecycle.ResumeLoaded(context.Background(), sandbox, nil)
		resumeDone <- err
	}()
	select {
	case err := <-resumeDone:
		t.Fatalf("resume completed before release lock was freed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(driver.allowRelease)
	if err := <-stopDone; err != nil {
		t.Fatalf("StopLoaded returned error: %v", err)
	}
	if err := <-resumeDone; err != nil {
		t.Fatalf("ResumeLoaded returned error: %v", err)
	}
	if driver.started.Load() != 1 || driver.released.Load() != 1 {
		t.Fatalf("start/release calls = %d/%d", driver.started.Load(), driver.released.Load())
	}
}

func TestLifecycleSerializesRuntimeReleaseAgainstRemoval(t *testing.T) {
	config, store, sandbox := newReleaseRecoverySandbox(t)
	driver := &releaseRecoveryDriver{
		releaseStarted: make(chan struct{}),
		allowRelease:   make(chan struct{}),
		removeEntered:  make(chan struct{}),
	}
	locks := sandboxes.NewLifecycleLocks()
	lifecycle := sandboxes.Lifecycle{Config: config, Store: store, Driver: driver, Locks: locks}
	removal := &sandboxes.RemovalCoordinator{SandboxRoot: config.SandboxRoot, Store: store, Runtime: driver, Locks: locks}

	stopDone := make(chan error, 1)
	go func() {
		_, _, err := lifecycle.StopLoaded(context.Background(), sandbox)
		stopDone <- err
	}()
	select {
	case <-driver.releaseStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime release did not start")
	}
	removeDone := make(chan error, 1)
	go func() {
		_, err := removal.Remove(context.Background(), sandbox.Summary.ID, true)
		removeDone <- err
	}()
	select {
	case <-driver.removeEntered:
		t.Fatal("full removal reached the runtime while release held the lifecycle lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(driver.allowRelease)
	if err := <-stopDone; err != nil {
		t.Fatalf("StopLoaded returned error: %v", err)
	}
	if err := <-removeDone; err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if driver.released.Load() != 1 || driver.removed.Load() != 1 {
		t.Fatalf("release/remove calls = %d/%d", driver.released.Load(), driver.removed.Load())
	}
	if _, err := store.GetSandbox(context.Background(), sandbox.Summary.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sandbox remains after serialized removal: %v", err)
	}
}

func newReleaseRecoverySandbox(t *testing.T) (*appconfig.Config, *sandboxstore.Store, *domain.Sandbox) {
	t.Helper()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot: root, SandboxRoot: filepath.Join(root, "sandboxes"), RuntimeDriver: "docker",
		DockerDefaultImage: "guest:latest", JupyterProxyBasePath: "/jupyter",
	}
	store, err := sandboxstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	sandbox, err := store.CreateSandboxWithOptions(context.Background(), "release", "", "docker", "guest:latest", "", "test", nil, nil, nil, sandboxstore.CreateSandboxOptions{
		StoppedRuntimePolicy: domain.StoppedRuntimePolicyRemove,
	})
	if err != nil {
		t.Fatalf("CreateSandboxWithOptions returned error: %v", err)
	}
	sandbox.Summary.VMStatus = domain.VMStatusRunning
	sandbox.StoppedRuntime = &domain.StoppedRuntime{State: domain.StoppedRuntimeStateReleasePending, RequestedAt: time.Now().UTC()}
	if err := store.UpdateSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("UpdateSandbox returned error: %v", err)
	}
	return config, store, sandbox
}
