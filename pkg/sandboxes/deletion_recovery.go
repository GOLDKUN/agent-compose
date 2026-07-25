package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

const deletionRecoveryWorkers = 2

// DeletionRecoveryStatus describes durable sandbox deletions being resumed
// after daemon startup. Remaining includes deletions that failed and must be
// retried by a future daemon instance.
type DeletionRecoveryStatus struct {
	InProgress       bool     `json:"in_progress"`
	Total            int      `json:"total"`
	Completed        int      `json:"completed"`
	Failed           int      `json:"failed"`
	Remaining        int      `json:"remaining"`
	ActiveSandboxIDs []string `json:"active_sandbox_ids,omitempty"`
	LastError        string   `json:"last_error,omitempty"`
}

// DeletionRecovery owns asynchronous recovery of sandbox deletion journals.
type DeletionRecovery struct {
	coordinator *RemovalCoordinator
	logger      *slog.Logger

	mu      sync.RWMutex
	status  DeletionRecoveryStatus
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
	r.status = DeletionRecoveryStatus{InProgress: true}
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

	r.mu.RLock()
	cancel := r.cancel
	done := r.done
	r.mu.RUnlock()
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

// Status returns an immutable snapshot of the current recovery state.
func (r *DeletionRecovery) Status() DeletionRecoveryStatus {
	if r == nil {
		return DeletionRecoveryStatus{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	status := r.status
	status.ActiveSandboxIDs = append([]string(nil), r.status.ActiveSandboxIDs...)
	return status
}

func (r *DeletionRecovery) run(ctx context.Context, done chan struct{}) {
	defer func() {
		r.mu.Lock()
		r.status.InProgress = false
		r.status.ActiveSandboxIDs = nil
		r.mu.Unlock()
		close(done)
	}()

	records, warnings := ListOwnershipRecords(r.coordinator.SandboxRoot)
	deleting := make([]OwnershipRecord, 0, len(records))
	for _, record := range records {
		if record.LifecycleState == "deleting" {
			deleting = append(deleting, record)
		}
	}
	r.mu.Lock()
	r.status.Total = len(deleting)
	r.status.Remaining = len(deleting)
	for _, warning := range warnings {
		r.status.LastError = warning
	}
	r.mu.Unlock()
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
				r.setActive(record.SandboxID, true)
				result, err := r.coordinator.Remove(ctx, record.SandboxID, true)
				r.setActive(record.SandboxID, false)
				if err == nil && !result.Removed {
					err = fmt.Errorf("sandbox deletion did not complete")
				}
				r.recordResult(ctx, record.SandboxID, err)
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

func (r *DeletionRecovery) setActive(sandboxID string, active bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if active {
		r.status.ActiveSandboxIDs = append(r.status.ActiveSandboxIDs, sandboxID)
		sort.Strings(r.status.ActiveSandboxIDs)
		return
	}
	for index, current := range r.status.ActiveSandboxIDs {
		if current == sandboxID {
			r.status.ActiveSandboxIDs = append(r.status.ActiveSandboxIDs[:index], r.status.ActiveSandboxIDs[index+1:]...)
			return
		}
	}
}

func (r *DeletionRecovery) recordResult(ctx context.Context, sandboxID string, err error) {
	if err == nil {
		r.mu.Lock()
		r.status.Completed++
		r.status.Remaining = r.status.Total - r.status.Completed
		r.mu.Unlock()
		return
	}
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return
	}

	wrapped := fmt.Errorf("resume sandbox deletion %s: %w", sandboxID, err)
	r.logger.Warn("failed to recover sandbox deletion", "sandbox_id", sandboxID, "error", err)
	r.mu.Lock()
	r.status.Failed++
	r.status.LastError = wrapped.Error()
	r.mu.Unlock()
}
