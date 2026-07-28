package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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
	deleting := make([]OwnershipRecord, 0, len(records))
	for _, record := range records {
		if record.LifecycleState == "deleting" {
			deleting = append(deleting, record)
		}
	}
	for _, warning := range warnings {
		r.logger.Warn("failed to read sandbox deletion journal", "warning", warning)
	}
	if len(deleting) == 0 || ctx.Err() != nil {
		return
	}

	jobs := make(chan OwnershipRecord)
	var workers sync.WaitGroup
	workerCount := min(deletionRecoveryWorkers, len(deleting))
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for record := range jobs {
				result, err := r.coordinator.Remove(ctx, record.SandboxID, true)
				if err == nil && !result.Removed {
					err = fmt.Errorf("sandbox deletion did not complete")
				}
				r.logFailure(ctx, record.SandboxID, err)
			}
		}()
	}

sendRecords:
	for _, record := range deleting {
		select {
		case jobs <- record:
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
