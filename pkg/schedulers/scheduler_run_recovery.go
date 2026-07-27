package schedulers

import (
	"context"
	"errors"
	"fmt"
	"time"

	domain "agent-compose/pkg/model"
)

const interruptedSchedulerRunError = "daemon interrupted scheduler trigger run before reaching terminal state"

type interruptedSchedulerRunStore interface {
	ListInterruptedLoaderRuns(context.Context, time.Time) ([]domain.SchedulerRunSummary, error)
}

func (c *Controller) RecoverInterruptedRuns(ctx context.Context, startedAt time.Time) error {
	store, ok := c.deps.Store.(interruptedSchedulerRunStore)
	if !ok || store == nil {
		return fmt.Errorf("scheduler run recovery store is unavailable")
	}
	runs, err := store.ListInterruptedLoaderRuns(ctx, startedAt.UTC())
	if err != nil {
		return err
	}
	completedAt := c.now()
	var recoveryErrors []error
	for _, run := range runs {
		run.Status = domain.SchedulerRunStatusFailed
		run.CompletedAt = completedAt
		run.DurationMs = max(completedAt.Sub(run.StartedAt).Milliseconds(), 0)
		run.Error = interruptedSchedulerRunError
		if err := c.deps.Store.UpdateLoaderRun(ctx, run); err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("recover interrupted scheduler run %s/%s: %w", run.SchedulerID, run.ID, err))
			continue
		}
		if _, err := c.AddLoaderEventRecord(
			ctx, run.SchedulerID, run.ID, run.TriggerID,
			"loader.run.failed", "error", interruptedSchedulerRunError,
			map[string]any{"reason": "daemon_interrupted"}, "", "", "",
		); err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("record interrupted scheduler run event %s/%s: %w", run.SchedulerID, run.ID, err))
		}
	}
	return errors.Join(recoveryErrors...)
}
