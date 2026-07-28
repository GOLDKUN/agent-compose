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

func ReconcileSchedulers(ctx context.Context, store ReconcileSchedulerStore, project domain.ProjectRecord, schedulers []domain.ProjectSchedulerRecord, definitions []domain.Scheduler, options ReconcileSchedulerOptions) ([]Change, bool, error) {
	// Change resource names and error text are observable CLI output. Historical
	// loader spellings remain stable while internal identifiers use scheduler.
	if store == nil {
		return nil, false, fmt.Errorf("config store is required")
	}
	currentByID := make(map[string]domain.ProjectSchedulerRecord, len(schedulers))
	definitionsByID := make(map[string]domain.Scheduler, len(definitions))
	for _, definition := range definitions {
		definitionsByID[definition.Summary.ID] = definition
	}
	changes := make([]Change, 0, len(schedulers)+len(definitions))
	unchanged := true
	for _, scheduler := range schedulers {
		currentByID[scheduler.SchedulerID] = scheduler
		existing, found, err := projectSchedulerIfExists(ctx, store, scheduler.ProjectID, scheduler.SchedulerID)
		if err != nil {
			return changes, false, fmt.Errorf("load project scheduler %s/%s: %w", scheduler.ProjectID, scheduler.SchedulerID, err)
		}
		definition, ok := definitionsByID[scheduler.ID]
		if !ok {
			return changes, false, fmt.Errorf("managed loader %s for scheduler %s missing", scheduler.ID, scheduler.SchedulerID)
		}
		var existingScheduler domain.Scheduler
		if found {
			existingScheduler, err = store.GetScheduler(ctx, definition.Summary.ID)
			if err != nil {
				return changes, false, fmt.Errorf("load scheduler %s: %w", definition.Summary.ID, err)
			}
		}
		if found && SchedulerRecordUnchanged(existing, scheduler) && SchedulerDefinitionUnchanged(existingScheduler, definition) {
			changes = append(changes, Change{
				Action:       ChangeActionUnchanged,
				ResourceType: "project_scheduler",
				ResourceID:   scheduler.SchedulerID,
				Name:         scheduler.AgentName,
			}, Change{
				Action:       ChangeActionUnchanged,
				ResourceType: "loader",
				ResourceID:   definition.Summary.ID,
				Name:         definition.Summary.Name,
			})
			continue
		}
		stagedScheduler := scheduler
		stagedScheduler.Enabled = false
		saved, err := store.UpsertProjectScheduler(ctx, stagedScheduler)
		if err != nil {
			return changes, false, fmt.Errorf("stage project scheduler %s/%s disabled: %w", scheduler.ProjectID, scheduler.SchedulerID, err)
		}

		if _, err := store.ReplaceSchedulerTriggers(ctx, saved.ID, definition.Triggers); err != nil {
			cleanupFailedScheduler(ctx, options, saved, saved.ID)
			return changes, false, fmt.Errorf("replace scheduler triggers %s: %w", saved.ID, err)
		}
		if scheduler.Enabled {
			saved, err = store.SetProjectSchedulerEnabled(ctx, scheduler.ProjectID, scheduler.SchedulerID, true)
			if err != nil {
				cleanupFailedScheduler(ctx, options, stagedScheduler, saved.ID)
				return changes, false, fmt.Errorf("enable project scheduler %s/%s: %w", scheduler.ProjectID, scheduler.SchedulerID, err)
			}
		} else {
			saved = stagedScheduler
		}
		action := SchedulerChangeAction(existing, found, scheduler)
		if action != ChangeActionUnchanged {
			unchanged = false
		}
		changes = append(changes, Change{
			Action:       action,
			ResourceType: "project_scheduler",
			ResourceID:   saved.SchedulerID,
			Name:         saved.AgentName,
		})
		schedulerAction := SchedulerDefinitionChangeAction(existingScheduler, found, definition)
		if schedulerAction != ChangeActionUnchanged {
			unchanged = false
		}
		changes = append(changes, Change{
			Action:       schedulerAction,
			ResourceType: "loader",
			ResourceID:   definition.Summary.ID,
			Name:         definition.Summary.Name,
		})
	}
	existingSchedulers, err := store.ListProjectSchedulers(ctx, project.ID)
	if err != nil {
		return changes, false, fmt.Errorf("list project schedulers: %w", err)
	}
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
			ResourceType: "project_scheduler",
			ResourceID:   disabled.SchedulerID,
			Name:         disabled.AgentName,
			Message:      "disabled because the scheduler is no longer present in the project spec",
		}, Change{
			Action:       ChangeActionRemoved,
			ResourceType: "loader",
			ResourceID:   existing.ID,
			Name:         existing.AgentName,
			Message:      "disabled because the scheduler is no longer present in the project spec",
		})
	}
	if options.RefreshSchedulers != nil {
		if err := options.RefreshSchedulers(ctx); err != nil {
			return changes, false, fmt.Errorf("refresh loader manager: %w", err)
		}
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
