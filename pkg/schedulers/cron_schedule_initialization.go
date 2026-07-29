package schedulers

import (
	"context"
	"fmt"

	domain "agent-compose/pkg/model"
)

func (c *Controller) initializeCronSchedules(ctx context.Context, items []domain.Scheduler) error {
	now := c.deps.Now().UTC()
	for schedulerIndex := range items {
		scheduler := &items[schedulerIndex]
		if !scheduler.Summary.Enabled {
			continue
		}
		for triggerIndex := range scheduler.Triggers {
			trigger := &scheduler.Triggers[triggerIndex]
			if !trigger.Enabled || trigger.Kind != domain.SchedulerTriggerKindCron || !trigger.NextFireAt.IsZero() {
				continue
			}
			nextFireAt, err := SchedulerTriggerNextFireAt(now, *trigger, false)
			if err != nil {
				return fmt.Errorf("initialize loader cron trigger %s/%s: %w", scheduler.Summary.ID, trigger.ID, err)
			}
			if err := c.deps.Store.SetSchedulerTriggerNextFireAt(ctx, scheduler.Summary.ID, trigger.ID, nextFireAt); err != nil {
				return fmt.Errorf("persist loader cron trigger schedule %s/%s: %w", scheduler.Summary.ID, trigger.ID, err)
			}
			trigger.NextFireAt = nextFireAt
		}
	}
	return nil
}
