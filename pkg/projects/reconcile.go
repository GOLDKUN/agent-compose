package projects

import (
	"context"
	"errors"
	"fmt"

	domain "agent-compose/pkg/model"
)

const (
	ChangeActionCreated   = "created"
	ChangeActionUpdated   = "updated"
	ChangeActionRemoved   = "removed"
	ChangeActionUnchanged = "unchanged"
)

type Change struct {
	Action       string
	ResourceType string
	ResourceID   string
	Name         string
	Message      string
}

type ReconcileSchedulerStore interface {
	GetProjectScheduler(ctx context.Context, projectID, schedulerID string) (domain.ProjectSchedulerRecord, error)
	UpsertProjectScheduler(ctx context.Context, scheduler domain.ProjectSchedulerRecord) (domain.ProjectSchedulerRecord, error)
	SetProjectSchedulerEnabled(ctx context.Context, projectID, schedulerID string, enabled bool) (domain.ProjectSchedulerRecord, error)
	ListProjectSchedulers(ctx context.Context, projectID string) ([]domain.ProjectSchedulerRecord, error)
	GetScheduler(ctx context.Context, schedulerID string) (domain.Scheduler, error)
	ReplaceSchedulerTriggers(ctx context.Context, schedulerID string, triggers []domain.SchedulerTrigger) ([]domain.SchedulerTrigger, error)
}

type ReconcileSchedulerOptions struct {
	CleanupFailedScheduler func(ctx context.Context, scheduler domain.ProjectSchedulerRecord, schedulerID string)
	RefreshSchedulers      func(ctx context.Context) error
}

// ReconcileSchedulersRequest describes the project schedulers to reconcile
// against their scheduler definitions.
type ReconcileSchedulersRequest struct {
	Project     domain.ProjectRecord
	Schedulers  []domain.ProjectSchedulerRecord
	Definitions []domain.Scheduler
}

func ReconcileSchedulers(ctx context.Context, store ReconcileSchedulerStore, req ReconcileSchedulersRequest, options ReconcileSchedulerOptions) ([]Change, bool, error) {
	if store == nil {
		return nil, false, fmt.Errorf("config store is required")
	}
	changes, currentByID, unchanged, err := reconcileCurrentSchedulers(ctx, store, req, options)
	if err != nil {
		return changes, false, err
	}
	removedChanges, removedUnchanged, err := disableRemovedSchedulers(ctx, store, req, currentByID)
	changes = append(changes, removedChanges...)
	if err != nil {
		return changes, false, err
	}
	if !removedUnchanged {
		unchanged = false
	}
	if options.RefreshSchedulers != nil {
		if err := options.RefreshSchedulers(ctx); err != nil {
			return changes, false, fmt.Errorf("refresh scheduler controller: %w", err)
		}
	}
	return changes, unchanged, nil
}

// reconcileCurrentSchedulers upserts each scheduler present in req.Schedulers,
// returning the schedulers seen so disableRemovedSchedulers can identify the
// ones that dropped out of the spec.
func reconcileCurrentSchedulers(ctx context.Context, store ReconcileSchedulerStore, req ReconcileSchedulersRequest, options ReconcileSchedulerOptions) ([]Change, map[string]domain.ProjectSchedulerRecord, bool, error) {
	schedulers := req.Schedulers
	currentByID := make(map[string]domain.ProjectSchedulerRecord, len(schedulers))
	definitionsByID := make(map[string]domain.Scheduler, len(req.Definitions))
	for _, definition := range req.Definitions {
		definitionsByID[definition.Summary.ID] = definition
	}
	changes := make([]Change, 0, len(schedulers))
	unchanged := true
	for _, scheduler := range schedulers {
		currentByID[scheduler.SchedulerID] = scheduler
		existing, found, err := projectSchedulerIfExists(ctx, store, scheduler.ProjectID, scheduler.SchedulerID)
		if err != nil {
			return changes, currentByID, false, fmt.Errorf("load project scheduler %s/%s: %w", scheduler.ProjectID, scheduler.SchedulerID, err)
		}
		definition, ok := definitionsByID[scheduler.ID]
		if !ok {
			return changes, currentByID, false, fmt.Errorf("scheduler definition %s for scheduler %s is missing", scheduler.ID, scheduler.SchedulerID)
		}
		var existingScheduler domain.Scheduler
		if found {
			existingScheduler, err = store.GetScheduler(ctx, definition.Summary.ID)
			if err != nil {
				return changes, currentByID, false, fmt.Errorf("load scheduler %s: %w", definition.Summary.ID, err)
			}
		}
		if found && SchedulerRecordUnchanged(existing, scheduler) && SchedulerDefinitionUnchanged(existingScheduler, definition) {
			changes = append(changes, Change{
				Action:       ChangeActionUnchanged,
				ResourceType: "scheduler",
				ResourceID:   scheduler.ID,
				Name:         scheduler.AgentName,
			})
			continue
		}
		stagedScheduler := scheduler
		stagedScheduler.Enabled = false
		saved, err := store.UpsertProjectScheduler(ctx, stagedScheduler)
		if err != nil {
			return changes, currentByID, false, fmt.Errorf("stage project scheduler %s/%s disabled: %w", scheduler.ProjectID, scheduler.SchedulerID, err)
		}

		if _, err := store.ReplaceSchedulerTriggers(ctx, saved.ID, definition.Triggers); err != nil {
			cleanupFailedScheduler(ctx, options, saved, saved.ID)
			return changes, currentByID, false, fmt.Errorf("replace scheduler triggers %s: %w", saved.ID, err)
		}
		if scheduler.Enabled {
			enabledScheduler, enableErr := store.SetProjectSchedulerEnabled(ctx, scheduler.ProjectID, scheduler.SchedulerID, true)
			if enableErr != nil {
				cleanupFailedScheduler(ctx, options, stagedScheduler, saved.ID)
				return changes, currentByID, false, fmt.Errorf("enable project scheduler %s/%s: %w", scheduler.ProjectID, scheduler.SchedulerID, enableErr)
			}
			saved = enabledScheduler
		} else {
			saved = stagedScheduler
		}
		action := SchedulerChangeAction(existing, found, scheduler)
		schedulerAction := SchedulerDefinitionChangeAction(existingScheduler, found, definition)
		if schedulerAction != ChangeActionUnchanged {
			action = schedulerAction
		}
		if action != ChangeActionUnchanged {
			unchanged = false
		}
		changes = append(changes, Change{
			Action:       action,
			ResourceType: "scheduler",
			ResourceID:   saved.ID,
			Name:         saved.AgentName,
		})
	}
	return changes, currentByID, unchanged, nil
}

// disableRemovedSchedulers disables any project scheduler that is no longer
// present in currentByID (i.e. dropped from the project spec).
func disableRemovedSchedulers(ctx context.Context, store ReconcileSchedulerStore, req ReconcileSchedulersRequest, currentByID map[string]domain.ProjectSchedulerRecord) ([]Change, bool, error) {
	existingSchedulers, err := store.ListProjectSchedulers(ctx, req.Project.ID)
	if err != nil {
		return nil, false, fmt.Errorf("list project schedulers: %w", err)
	}
	changes := make([]Change, 0, len(existingSchedulers))
	unchanged := true
	for _, existing := range existingSchedulers {
		if _, ok := currentByID[existing.SchedulerID]; ok {
			continue
		}
		if !existing.Enabled {
			continue
		}
		disabled, err := store.SetProjectSchedulerEnabled(ctx, existing.ProjectID, existing.SchedulerID, false)
		if err != nil {
			return changes, false, fmt.Errorf("disable removed project scheduler %s/%s: %w", existing.ProjectID, existing.SchedulerID, err)
		}
		unchanged = false
		changes = append(changes, Change{
			Action:       ChangeActionRemoved,
			ResourceType: "scheduler",
			ResourceID:   disabled.ID,
			Name:         disabled.AgentName,
			Message:      "disabled because the scheduler is no longer present in the project spec",
		})
	}
	return changes, unchanged, nil
}

func cleanupFailedScheduler(ctx context.Context, options ReconcileSchedulerOptions, scheduler domain.ProjectSchedulerRecord, schedulerID string) {
	if options.CleanupFailedScheduler != nil {
		options.CleanupFailedScheduler(ctx, scheduler, schedulerID)
	}
}

func projectSchedulerIfExists(ctx context.Context, store ReconcileSchedulerStore, projectID, schedulerID string) (domain.ProjectSchedulerRecord, bool, error) {
	scheduler, err := store.GetProjectScheduler(ctx, projectID, schedulerID)
	if err == nil {
		return scheduler, true, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ProjectSchedulerRecord{}, false, nil
	}
	return domain.ProjectSchedulerRecord{}, false, err
}

func SchedulerChangeAction(existing domain.ProjectSchedulerRecord, found bool, current domain.ProjectSchedulerRecord) string {
	if !found {
		return ChangeActionCreated
	}
	if SchedulerRecordUnchanged(existing, current) {
		return ChangeActionUnchanged
	}
	return ChangeActionUpdated
}

func SchedulerDefinitionChangeAction(existing domain.Scheduler, found bool, current domain.Scheduler) string {
	if !found {
		return ChangeActionCreated
	}
	if SchedulerDefinitionUnchanged(existing, current) {
		return ChangeActionUnchanged
	}
	return ChangeActionUpdated
}

func ProjectAgentChangeAction(existing domain.ProjectAgentRecord, found bool, current domain.ProjectAgentRecord) string {
	if !found {
		return ChangeActionCreated
	}
	if ProjectAgentRecordUnchanged(existing, current) {
		return ChangeActionUnchanged
	}
	return ChangeActionUpdated
}
