package sandboxes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
)

func TestDeletionRecoveryRunsPendingDeletionsConcurrentlyAndStops(t *testing.T) {
	root := t.TempDir()
	writeDeletingRecoveryRecords(t, root, "a", "b")
	runtime := &recoveryRuntime{
		blockedID: "a",
		started:   make(chan string, 2),
		completed: make(chan string, 2),
	}
	recovery := newTestDeletionRecovery(root, runtime)
	if err := recovery.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	wantStarted := map[string]bool{"a": false, "b": false}
	for range wantStarted {
		select {
		case id := <-runtime.started:
			wantStarted[id] = true
		case <-time.After(time.Second):
			t.Fatalf("recovery workers did not start both deletions: %v", wantStarted)
		}
	}
	select {
	case id := <-runtime.completed:
		if id != "b" {
			t.Fatalf("completed deletion = %q, want unblocked sandbox b", id)
		}
	case <-time.After(time.Second):
		t.Fatal("unblocked deletion waited behind blocked deletion")
	}

	waitForRecoveryJournalRemoval(t, root, "b")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := recovery.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	if _, err := ReadOwnershipRecord(root, "a"); err != nil {
		t.Fatalf("canceled deletion did not retain its journal: %v", err)
	}
}

func TestDeletionRecoveryReportsFailureAndRetriesOnNextInstance(t *testing.T) {
	root := t.TempDir()
	writeDeletingRecoveryRecords(t, root, "retry")
	failedRuntime := &recoveryRuntime{failureID: "retry", started: make(chan string, 1), completed: make(chan string, 1)}
	first := newTestDeletionRecovery(root, failedRuntime)
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("first Start returned error: %v", err)
	}
	waitForDeletionRecoveryDone(t, first)
	if _, err := ReadOwnershipRecord(root, "retry"); err != nil {
		t.Fatalf("failed deletion did not retain its journal: %v", err)
	}

	successRuntime := &recoveryRuntime{started: make(chan string, 1), completed: make(chan string, 1)}
	second := newTestDeletionRecovery(root, successRuntime)
	if err := second.Start(context.Background()); err != nil {
		t.Fatalf("second Start returned error: %v", err)
	}
	waitForRecoveryJournalRemoval(t, root, "retry")
	waitForDeletionRecoveryDone(t, second)
}

func TestDeletionRecoveryReportsUnreadableJournal(t *testing.T) {
	root := t.TempDir()
	lifecycleRoot := LifecycleRoot(root)
	if err := os.MkdirAll(lifecycleRoot, 0o700); err != nil {
		t.Fatalf("create lifecycle root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lifecycleRoot, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write broken lifecycle journal: %v", err)
	}

	var logs bytes.Buffer
	recovery := NewDeletionRecovery(&RemovalCoordinator{
		SandboxRoot: root,
		Store:       recoveryStore{},
		Runtime:     &recoveryRuntime{},
	}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err := recovery.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	waitForDeletionRecoveryDone(t, recovery)
	if !strings.Contains(logs.String(), "broken.json") {
		t.Fatalf("recovery log = %q, want unreadable journal warning", logs.String())
	}
}

func TestDeletionRecoveryLifecycleEdges(t *testing.T) {
	t.Run("missing coordinator", func(t *testing.T) {
		recovery := NewDeletionRecovery(nil, discardRecoveryLogger())
		if err := recovery.Start(context.Background()); err == nil {
			t.Fatal("Start returned nil error without a coordinator")
		}
		if err := recovery.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown before Start returned error: %v", err)
		}
	})

	t.Run("start is idempotent", func(t *testing.T) {
		recovery := newTestDeletionRecovery(t.TempDir(), &recoveryRuntime{})
		if err := recovery.Start(context.Background()); err != nil {
			t.Fatalf("first Start returned error: %v", err)
		}
		if err := recovery.Start(context.Background()); err != nil {
			t.Fatalf("second Start returned error: %v", err)
		}
		waitForDeletionRecoveryDone(t, recovery)
	})

	t.Run("shutdown honors deadline", func(t *testing.T) {
		root := t.TempDir()
		writeDeletingRecoveryRecords(t, root, "slow")
		release := make(chan struct{})
		runtime := &recoveryRuntime{blockedID: "slow", ignoreCancellation: true, release: release, started: make(chan string, 1)}
		recovery := newTestDeletionRecovery(root, runtime)
		if err := recovery.Start(context.Background()); err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
		select {
		case <-runtime.started:
		case <-time.After(time.Second):
			t.Fatal("slow deletion did not start")
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if err := recovery.Shutdown(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Shutdown error = %v, want deadline exceeded", err)
		}
		close(release)
		if err := recovery.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown after release returned error: %v", err)
		}
	})
}

func newTestDeletionRecovery(root string, runtime RemovalRuntime) *DeletionRecovery {
	return NewDeletionRecovery(&RemovalCoordinator{
		SandboxRoot: root,
		Store:       recoveryStore{},
		Runtime:     runtime,
	}, discardRecoveryLogger())
}

func writeDeletingRecoveryRecords(t *testing.T, root string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := WriteOwnershipRecord(root, OwnershipRecord{
			SandboxID: id, SandboxPath: filepath.Join(root, id), LifecycleState: "deleting",
		}); err != nil {
			t.Fatalf("write ownership record %s: %v", id, err)
		}
	}
}

func waitForDeletionRecoveryDone(t *testing.T, recovery *DeletionRecovery) {
	t.Helper()
	recovery.mu.Lock()
	done := recovery.done
	recovery.mu.Unlock()
	if done == nil {
		t.Fatal("deletion recovery did not initialize its completion signal")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deletion recovery did not finish")
	}
}

func waitForRecoveryJournalRemoval(t *testing.T, root, sandboxID string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := ReadOwnershipRecord(root, sandboxID)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			t.Fatalf("read recovery journal %s: %v", sandboxID, err)
		}
		select {
		case <-deadline.C:
			t.Fatalf("recovery journal %s was not removed", sandboxID)
		case <-ticker.C:
		}
	}
}

func discardRecoveryLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type recoveryStore struct{}

func (recoveryStore) GetSandbox(_ context.Context, id string) (*domain.Sandbox, error) {
	return &domain.Sandbox{Summary: domain.SandboxSummary{ID: id}}, nil
}

func (recoveryStore) ListSandboxes(context.Context, domain.SandboxListOptions) (domain.SandboxListResult, error) {
	return domain.SandboxListResult{}, nil
}

func (recoveryStore) UpdateSandbox(context.Context, *domain.Sandbox) error { return nil }
func (recoveryStore) RemoveSandbox(context.Context, string) error          { return nil }

type recoveryRuntime struct {
	blockedID          string
	failureID          string
	ignoreCancellation bool
	release            <-chan struct{}
	started            chan string
	completed          chan string
}

func (r *recoveryRuntime) StopSandboxVM(context.Context, *domain.Sandbox) error { return nil }

func (r *recoveryRuntime) RemoveSandboxVM(ctx context.Context, sandbox *domain.Sandbox) error {
	if sandbox == nil {
		return errors.New("sandbox is nil")
	}
	if r.started != nil {
		r.started <- sandbox.Summary.ID
	}
	if sandbox.Summary.ID == r.failureID {
		return errors.New("runtime removal failed")
	}
	if sandbox.Summary.ID == r.blockedID {
		if r.ignoreCancellation {
			<-r.release
		} else {
			<-ctx.Done()
			return ctx.Err()
		}
	}
	if r.completed != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r.completed <- sandbox.Summary.ID:
		}
	}
	return nil
}
