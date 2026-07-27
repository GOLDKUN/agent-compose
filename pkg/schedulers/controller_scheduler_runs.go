package schedulers

import (
	"context"

	domain "agent-compose/pkg/model"
)

func (c *Controller) RunScheduler(ctx context.Context, request SchedulerRunRequest) (domain.SchedulerRunSummary, error) {
	return c.schedulerRuns.Run(ctx, request)
}

func (c *Controller) InvokeScheduler(ctx context.Context, schedulerID, payloadJSON string) (InvocationResult, error) {
	scheduler, _, err := c.LoadSchedulerForRun(ctx, schedulerID, "")
	if err != nil {
		return InvocationResult{}, err
	}
	return c.invocations.Invoke(ctx, scheduler, payloadJSON)
}

func (c *Controller) StartSchedulerRun(ctx context.Context, request SchedulerRunRequest) (domain.SchedulerRunSummary, error) {
	return c.schedulerRuns.Start(ctx, request)
}

func (c *Controller) GetSchedulerRun(ctx context.Context, schedulerID, runID string) (domain.SchedulerRunSummary, error) {
	return c.schedulerRuns.Get(ctx, schedulerID, runID)
}

func (c *Controller) ListSchedulerRuns(ctx context.Context, schedulerID string, limit int) ([]domain.SchedulerRunSummary, error) {
	return c.schedulerRuns.List(ctx, schedulerID, limit)
}

func (c *Controller) StopSchedulerRun(ctx context.Context, schedulerID, runID, reason string) (domain.SchedulerRunSummary, bool, error) {
	return c.schedulerRuns.Stop(ctx, schedulerID, runID, reason)
}
