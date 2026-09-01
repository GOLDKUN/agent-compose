package schedulers

import (
	"context"
	"log/slog"
	"time"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

type SchedulerStore interface {
	MarkSchedulerTriggerFired(ctx context.Context, schedulerID, triggerID string, lastFiredAt, nextFireAt time.Time) error
}

type SchedulerDependencies struct {
	RootCtx       context.Context
	Wake          <-chan struct{}
	Store         SchedulerStore
	Snapshot      func() map[string]domain.Scheduler
	ReplaceCached func(map[string]domain.Scheduler)
	Run           func(ctx context.Context, req RunTriggerRequest, triggerEventAck ...func(context.Context) error) (domain.SchedulerRunSummary, error)
	RunTimeout    func(time.Duration) time.Duration
}

type Scheduler struct {
	deps SchedulerDependencies
}

func NewScheduler(deps SchedulerDependencies) *Scheduler {
	if deps.RootCtx == nil {
		deps.RootCtx = context.Background()
	}
	return &Scheduler{deps: deps}
}

func (s *Scheduler) Loop() {
	for {
		jobs := s.CollectDue(time.Now().UTC())
		if len(jobs) > 0 {
			s.Dispatch(jobs)
			continue
		}

		nextFireAt, ok := s.NextFireAt()
		if !ok {
			select {
			case <-s.rootCtx().Done():
				return
			case <-s.deps.Wake:
				continue
			}
		}

		wait := time.Until(nextFireAt)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-s.rootCtx().Done():
			StopTimer(timer)
			return
		case <-s.deps.Wake:
			StopTimer(timer)
			continue
		case <-timer.C:
		}
	}
}

func (s *Scheduler) Dispatch(jobs []ScheduledRun) {
	for _, job := range jobs {
		runCtx, cancel := context.WithTimeout(s.rootCtx(), s.runTimeoutFor(job.Scheduler, 0))
		go func(job ScheduledRun) {
			defer cancel()
			if _, err := s.deps.Run(runCtx, RunTriggerRequest{Scheduler: job.Scheduler, Trigger: &job.Trigger, PayloadJSON: job.PayloadJSON, Source: job.Source}); err != nil {
				slog.Warn("scheduler run failed", "scheduler_id", job.Scheduler.Summary.ID, "trigger_id", job.Trigger.ID, "trigger_kind", job.Trigger.Kind, "error", err)
			}
		}(job)
	}
}

func (s *Scheduler) NextFireAt() (time.Time, bool) {
	var nextFireAt time.Time
	for _, scheduler := range s.snapshot() {
		if !scheduler.Summary.Enabled {
			continue
		}
		for _, trigger := range scheduler.Triggers {
			if !trigger.Enabled || !TriggerUsesSchedule(trigger.Kind) || trigger.NextFireAt.IsZero() {
				continue
			}
			if nextFireAt.IsZero() || trigger.NextFireAt.Before(nextFireAt) {
				nextFireAt = trigger.NextFireAt
			}
		}
	}
	if nextFireAt.IsZero() {
		return time.Time{}, false
	}
	return nextFireAt, true
}

func (s *Scheduler) CollectDue(now time.Time) []ScheduledRun {
	scheduled, updatedSchedulers, scheduleErrs := CollectDueScheduledRuns(s.snapshot(), now)
	if len(updatedSchedulers) > 0 && s.deps.ReplaceCached != nil {
		s.deps.ReplaceCached(updatedSchedulers)
	}
	for _, item := range scheduleErrs {
		slog.Warn("failed to compute next scheduler fire time", "scheduler_id", item.SchedulerID, "trigger_id", item.TriggerID, "trigger_kind", item.TriggerKind, "error", item.Err)
	}
	for _, job := range scheduled {
		if s.deps.Store == nil {
			continue
		}
		if err := s.deps.Store.MarkSchedulerTriggerFired(s.rootCtx(), job.Scheduler.Summary.ID, job.Trigger.ID, job.Trigger.LastFiredAt, job.Trigger.NextFireAt); err != nil {
			slog.Warn("failed to persist scheduler fire state", "scheduler_id", job.Scheduler.Summary.ID, "trigger_id", job.Trigger.ID, "trigger_kind", job.Trigger.Kind, "error", err)
		}
	}
	return scheduled
}

func StopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (s *Scheduler) snapshot() map[string]domain.Scheduler {
	if s.deps.Snapshot == nil {
		return nil
	}
	return s.deps.Snapshot()
}

func (s *Scheduler) rootCtx() context.Context {
	if s.deps.RootCtx == nil {
		return context.Background()
	}
	return s.deps.RootCtx
}

func (s *Scheduler) runTimeoutFor(scheduler domain.Scheduler, override time.Duration) time.Duration {
	return effectiveSchedulerRunTimeout(scheduler, override, s.deps.RunTimeout)
}
