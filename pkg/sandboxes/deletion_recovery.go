package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	domain "agent-compose/pkg/model"
)

const deletionRecoveryWorkers = 2

// DeletionRecovery owns asynchronous recovery of sandbox deletion journals.
type DeletionRecovery struct {
	coordinator *RemovalCoordinator
	logger      *slog.Logger

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
}

// NewDeletionRecovery creates a one-shot deletion recovery component.
func NewDeletionRecovery(coordinator *RemovalCoordinator, logger *slog.Logger) *DeletionRecovery {
	if logger == nil {
		logger = slog.Default()
	}
	return &DeletionRecovery{coordinator: coordinator, logger: logger}
}

// Start begins recovery and returns without waiting for the lifecycle scan or
// any pending deletion.
func (r *DeletionRecovery) Start(parent context.Context) error {
	if r == nil || r.coordinator == nil {
		return fmt.Errorf("deletion recovery is not configured")
	}
	if parent == nil {
		parent = context.Background()
	}

	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	r.started = true
	r.cancel = cancel
	r.done = done
	r.mu.Unlock()

	go r.run(ctx, done)
	return nil
}

// Shutdown cancels recovery and waits for all workers to exit.
func (r *DeletionRecovery) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	r.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *DeletionRecovery) run(ctx context.Context, done chan struct{}) {
	defer close(done)

	records, warnings := ListOwnershipRecords(r.coordinator.SandboxRoot)
	pending := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record.LifecycleState == "deleting" {
			pending[record.SandboxID] = struct{}{}
		}
	}
	listed, listErr := r.coordinator.Store.ListSandboxes(ctx, domain.SandboxListOptions{Limit: 1 << 30})
	if listErr != nil {
		warnings = append(warnings, fmt.Sprintf("list archived sandboxes for deletion recovery: %v", listErr))
	} else {
		for _, sandbox := range listed.Sandboxes {
			if sandbox.Archive != nil && sandbox.Archive.State == domain.SandboxArchiveStateArchived {
				pending[sandbox.Summary.ID] = struct{}{}
			}
		}
	}
	for _, warning := range warnings {
		r.logger.Warn("sandbox deletion recovery warning", "warning", warning)
	}
	if len(pending) == 0 || ctx.Err() != nil {
		return
	}
	ids := make([]string, 0, len(pending))
	for sandboxID := range pending {
		ids = append(ids, sandboxID)
	}
	sort.Strings(ids)

	jobs := make(chan string)
	var workers sync.WaitGroup
	workerCount := min(deletionRecoveryWorkers, len(ids))
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for sandboxID := range jobs {
				result, err := r.coordinator.Remove(ctx, sandboxID, true)
				if err == nil && !result.Removed {
					err = fmt.Errorf("sandbox deletion did not complete")
				}
				r.logFailure(ctx, sandboxID, err)
			}
		}()
	}

sendRecords:
	for _, sandboxID := range ids {
		select {
		case jobs <- sandboxID:
		case <-ctx.Done():
			break sendRecords
		}
	}
	close(jobs)
	workers.Wait()
}

func (r *DeletionRecovery) logFailure(ctx context.Context, sandboxID string, err error) {
	if err == nil {
		return
	}
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return
	}

	r.logger.Warn("failed to recover sandbox deletion", "sandbox_id", sandboxID, "error", err)
}
