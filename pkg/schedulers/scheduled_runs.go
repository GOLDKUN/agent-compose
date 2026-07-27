package schedulers

import (
	"time"

	domain "agent-compose/pkg/model"
)

type ScheduledRun struct {
	Scheduler   domain.Scheduler
	Trigger     domain.SchedulerTrigger
	PayloadJSON string
	Source      string
}

type ScheduleError struct {
	SchedulerID string
	TriggerID   string
	TriggerKind string
	Err         error
}

func CollectDueScheduledRuns(items map[string]domain.Scheduler, now time.Time) ([]ScheduledRun, map[string]domain.Scheduler, []ScheduleError) {
	jobs := make([]ScheduledRun, 0)
	updatedLoaders := make(map[string]domain.Scheduler)
	var errs []ScheduleError
	for id, loader := range items {
		if !loader.Summary.Enabled {
			continue
		}
		updated := false
		for index := range loader.Triggers {
			trigger := &loader.Triggers[index]
			if !trigger.Enabled || !domain.SchedulerTriggerUsesSchedule(trigger.Kind) || trigger.NextFireAt.IsZero() || trigger.NextFireAt.After(now) {
				continue
			}
			nextFireAt, err := SchedulerTriggerNextFireAt(now, *trigger, true)
			if err != nil {
				errs = append(errs, ScheduleError{
					SchedulerID: loader.Summary.ID,
					TriggerID:   trigger.ID,
					TriggerKind: trigger.Kind,
					Err:         err,
				})
				continue
			}
			trigger.LastFiredAt = now
			trigger.NextFireAt = nextFireAt
			jobs = append(jobs, ScheduledRun{
				Scheduler:   CloneLoader(loader),
				Trigger:     *trigger,
				PayloadJSON: "",
				Source:      SchedulerTriggerSource(*trigger),
			})
			updated = true
		}
		if updated {
			updatedLoaders[id] = CloneLoader(loader)
		}
	}
	return jobs, updatedLoaders, errs
}

func CloneLoader(item domain.Scheduler) domain.Scheduler {
	cloned := item
	if item.Triggers != nil {
		cloned.Triggers = append([]domain.SchedulerTrigger(nil), item.Triggers...)
	}
	if item.EnvItems != nil {
		cloned.EnvItems = append([]domain.SandboxEnvVar(nil), item.EnvItems...)
	}
	return cloned
}
