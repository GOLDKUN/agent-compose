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
	updatedSchedulers := make(map[string]domain.Scheduler)
	var errs []ScheduleError
	for id, scheduler := range items {
		if !scheduler.Summary.Enabled {
			continue
		}
		updated := false
		for index := range scheduler.Triggers {
			trigger := &scheduler.Triggers[index]
			if !trigger.Enabled || !domain.SchedulerTriggerUsesSchedule(trigger.Kind) || trigger.NextFireAt.IsZero() || trigger.NextFireAt.After(now) {
				continue
			}
			nextFireAt, err := SchedulerTriggerNextFireAt(now, *trigger, true)
			if err != nil {
				errs = append(errs, ScheduleError{
					SchedulerID: scheduler.Summary.ID,
					TriggerID:   trigger.ID,
					TriggerKind: trigger.Kind,
					Err:         err,
				})
				continue
			}
			trigger.LastFiredAt = now
			trigger.NextFireAt = nextFireAt
			jobs = append(jobs, ScheduledRun{
				Scheduler:   CloneScheduler(scheduler),
				Trigger:     *trigger,
				PayloadJSON: "",
				Source:      SchedulerTriggerSource(*trigger),
			})
			updated = true
		}
		if updated {
			updatedSchedulers[id] = CloneScheduler(scheduler)
		}
	}
	return jobs, updatedSchedulers, errs
}

func CloneScheduler(item domain.Scheduler) domain.Scheduler {
	cloned := item
	if item.Triggers != nil {
		cloned.Triggers = append([]domain.SchedulerTrigger(nil), item.Triggers...)
	}
	if item.EnvItems != nil {
		cloned.EnvItems = append([]domain.SandboxEnvVar(nil), item.EnvItems...)
	}
	return cloned
}
