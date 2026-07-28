package sandboxes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	domain "agent-compose/pkg/model"
)

type stoppedRuntimeTestStore struct {
	sandbox *domain.Sandbox
	updates []string
	events  []domain.SandboxEvent
}

func (s *stoppedRuntimeTestStore) UpdateSandbox(_ context.Context, sandbox *domain.Sandbox) error {
	s.sandbox = sandbox
	s.updates = append(s.updates, sandbox.Summary.VMStatus+":"+domain.EffectiveStoppedRuntimeState(sandbox))
	return nil
}

func (s *stoppedRuntimeTestStore) AddEvent(_ context.Context, _ string, event domain.SandboxEvent) error {
	s.events = append(s.events, event)
	return nil
}

type stoppedRuntimeTestDriver struct {
	store         *stoppedRuntimeTestStore
	requireIntent bool
	stopCalls     int
	releaseCalls  int
	releaseErr    error
}

func (d *stoppedRuntimeTestDriver) StopSandboxVM(context.Context, *domain.Sandbox) error {
	d.stopCalls++
	if d.requireIntent && (d.store == nil || domain.EffectiveStoppedRuntimeState(d.store.sandbox) != domain.StoppedRuntimeStateReleasePending) {
		return errors.New("release intent was not persisted before stop")
	}
	return nil
}

func (d *stoppedRuntimeTestDriver) ReleaseSandboxRuntime(context.Context, *domain.Sandbox) error {
	d.releaseCalls++
	return d.releaseErr
}

func TestStopSandboxRuntimeRetainsByDefault(t *testing.T) {
	sandbox := &domain.Sandbox{Summary: domain.SandboxSummary{ID: "sandbox", VMStatus: domain.VMStatusRunning}}
	store := &stoppedRuntimeTestStore{sandbox: sandbox}
	driver := &stoppedRuntimeTestDriver{}
	result, err := StopSandboxRuntime(context.Background(), "", store, driver, sandbox, true)
	if err != nil || !result.Stopped || result.Released || driver.stopCalls != 1 || driver.releaseCalls != 0 {
		t.Fatalf("result=%#v stop/release=%d/%d err=%v", result, driver.stopCalls, driver.releaseCalls, err)
	}
	if domain.EffectiveStoppedRuntimeState(sandbox) != domain.StoppedRuntimeStateRetained {
		t.Fatalf("runtime state = %#v, want retained", sandbox.StoppedRuntime)
	}
	if message := SandboxStoppedEventMessage(result); message != "sandbox stopped and runtime retained" {
		t.Fatalf("stopped event message = %q", message)
	}
}

func TestStopSandboxRuntimePersistsIntentAndReleasesOwnership(t *testing.T) {
	root, sandbox := newStoppedRuntimeTestSandbox(t)
	sandbox.StoppedRuntimePolicy = domain.StoppedRuntimePolicyRemove
	store := &stoppedRuntimeTestStore{sandbox: sandbox}
	driver := &stoppedRuntimeTestDriver{store: store, requireIntent: true}

	result, err := StopSandboxRuntime(context.Background(), root, store, driver, sandbox, true)
	if err != nil || !result.Stopped || !result.Released || driver.stopCalls != 1 || driver.releaseCalls != 1 {
		t.Fatalf("result=%#v stop/release=%d/%d err=%v", result, driver.stopCalls, driver.releaseCalls, err)
	}
	if domain.EffectiveStoppedRuntimeState(sandbox) != domain.StoppedRuntimeStateReleased || sandbox.StoppedRuntime.ReleasedAt.IsZero() {
		t.Fatalf("runtime release = %#v, want released timestamp", sandbox.StoppedRuntime)
	}
	if len(store.events) != 1 || store.events[0].Type != "sandbox.runtime_released" {
		t.Fatalf("release events = %#v", store.events)
	}
	if message := SandboxStoppedEventMessage(result); message != "sandbox stopped and runtime released" {
		t.Fatalf("stopped event message = %q", message)
	}
	record, err := ReadOwnershipRecord(root, sandbox.Summary.ID)
	if err != nil {
		t.Fatalf("ReadOwnershipRecord returned error: %v", err)
	}
	if record.RuntimeID != "" {
		t.Fatalf("runtime ownership = %q, want cleared", record.RuntimeID)
	}
	for _, resource := range record.OwnedResources {
		if resource.Kind == "runtime" {
			t.Fatalf("runtime owned resource remains: %#v", record.OwnedResources)
		}
	}
}

func TestStopSandboxRuntimeReleaseFailureIsRetryable(t *testing.T) {
	root, sandbox := newStoppedRuntimeTestSandbox(t)
	sandbox.StoppedRuntimePolicy = domain.StoppedRuntimePolicyRemove
	store := &stoppedRuntimeTestStore{sandbox: sandbox}
	releaseErr := errors.New("remove failed")
	driver := &stoppedRuntimeTestDriver{store: store, requireIntent: true, releaseErr: releaseErr}

	_, err := StopSandboxRuntime(context.Background(), root, store, driver, sandbox, true)
	if !errors.Is(err, releaseErr) || sandbox.Summary.VMStatus != domain.VMStatusStopped ||
		domain.EffectiveStoppedRuntimeState(sandbox) != domain.StoppedRuntimeStateReleasePending || sandbox.StoppedRuntime.LastError == "" {
		t.Fatalf("sandbox=%#v release=%#v err=%v", sandbox.Summary, sandbox.StoppedRuntime, err)
	}
	if len(store.events) != 1 || store.events[0].Type != "sandbox.runtime_release_failed" {
		t.Fatalf("release failure events = %#v", store.events)
	}
	driver.releaseErr = nil
	result, err := StopSandboxRuntime(context.Background(), root, store, driver, sandbox, false)
	if err != nil || !result.Released || driver.stopCalls != 1 || driver.releaseCalls != 2 {
		t.Fatalf("retry result=%#v stop/release=%d/%d err=%v", result, driver.stopCalls, driver.releaseCalls, err)
	}
}

func newStoppedRuntimeTestSandbox(t *testing.T) (string, *domain.Sandbox) {
	t.Helper()
	root := t.TempDir()
	sandboxDir := filepath.Join(root, "sandbox")
	if err := os.MkdirAll(filepath.Join(sandboxDir, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	sandbox := &domain.Sandbox{Summary: domain.SandboxSummary{
		ID: "sandbox", VMStatus: domain.VMStatusRunning, Driver: "docker", RuntimeRef: "runtime", WorkspacePath: filepath.Join(sandboxDir, "workspace"),
	}}
	if err := WriteOwnershipRecord(root, OwnershipRecord{
		SandboxID: sandbox.Summary.ID, Driver: sandbox.Summary.Driver, RuntimeID: sandbox.Summary.RuntimeRef,
		SandboxPath: sandboxDir, LifecycleState: "active", OwnedResources: []OwnedResource{{Kind: "runtime", Identity: sandbox.Summary.RuntimeRef}},
	}); err != nil {
		t.Fatalf("WriteOwnershipRecord returned error: %v", err)
	}
	return root, sandbox
}
