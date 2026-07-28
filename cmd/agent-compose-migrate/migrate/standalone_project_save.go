package migrate

import (
	"context"
	"database/sql"
	"fmt"

	domain "agent-compose/pkg/model"
)

func saveStandaloneProjectMembers(
	ctx context.Context,
	tx *sql.Tx,
	projectID string,
	revision int64,
	standaloneAgentCount int,
	converted []convertedStandaloneAgent,
) ([]map[string]any, error) {
	specAgents := make([]map[string]any, 0, len(converted))
	for index := range converted {
		item := &converted[index]
		if err := saveStandaloneProjectAgent(ctx, tx, projectID, revision, index >= standaloneAgentCount, item); err != nil {
			return nil, err
		}
		var schedulerJSON map[string]any
		if item.scheduler != nil {
			schedulerJSON = legacySchedulerJSON(*item.scheduler)
		}
		if !item.reuseExisting || item.scheduler != nil {
			specAgents = append(specAgents, legacyAgentJSON(item.definition, item.name, schedulerJSON))
		}
		if item.scheduler != nil {
			if err := saveStandaloneProjectScheduler(ctx, tx, projectID, revision, item, schedulerJSON); err != nil {
				return nil, err
			}
		}
	}
	return specAgents, nil
}

func saveStandaloneProjectAgent(ctx context.Context, tx *sql.Tx, projectID string, revision int64, synthetic bool, item *convertedStandaloneAgent) error {
	if synthetic {
		if err := insertSyntheticAgentDefinition(ctx, tx, projectID, revision, item.name, item.definition); err != nil {
			return err
		}
	} else if !item.reuseExisting {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_definition SET managed_project_id=?, managed_project_revision=?, managed_agent_name=? WHERE id=?`, projectID, revision, item.name, item.nativeID); err != nil {
			return fmt.Errorf("attach standalone agent %s: %w", item.definition.id, err)
		}
	}
	if item.reuseExisting {
		return nil
	}
	return insertLegacyProjectAgent(ctx, tx, projectID, revision, item.name, item.nativeID, item.definition, item.scheduler != nil)
}

func saveStandaloneProjectScheduler(ctx context.Context, tx *sql.Tx, projectID string, revision int64, item *convertedStandaloneAgent, schedulerJSON map[string]any) error {
	scheduler := *item.scheduler
	schedulerID, err := domain.StableProjectSchedulerID(projectID, item.name, "")
	if err != nil {
		return fmt.Errorf("derive standalone scheduler identity %s: %w", scheduler.id, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_scheduler(
		id, short_id, project_id, scheduler_id, agent_name, managed_loader_id, revision, enabled, trigger_count, spec_json, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, (SELECT COUNT(*) FROM loader_trigger WHERE loader_id=?), ?, ?, ?)`,
		schedulerID, shortLegacyID(schedulerID), projectID, schedulerID, item.name, scheduler.id, revision,
		scheduler.enabled, scheduler.id, mustJSON(schedulerJSON), scheduler.createdAt, scheduler.updatedAt); err != nil {
		return fmt.Errorf("project standalone scheduler %s: %w", scheduler.id, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE loader SET managed_project_id=?, managed_project_revision=?, managed_agent_name=?, managed_scheduler_id=? WHERE id=?`, projectID, revision, item.name, schedulerID, scheduler.id); err != nil {
		return fmt.Errorf("attach standalone scheduler %s: %w", scheduler.id, err)
	}
	return nil
}

func advanceLegacyProjectProjectionRevision(ctx context.Context, tx *sql.Tx, projectID string, revision int64) error {
	for _, update := range []struct {
		operation string
		query     string
	}{
		{operation: "advance legacy project agent definitions", query: `UPDATE agent_definition SET managed_project_revision=? WHERE managed_project_id=?`},
		{operation: "advance legacy project agents", query: `UPDATE project_agent SET revision=? WHERE project_id=?`},
		{operation: "advance legacy project schedulers", query: `UPDATE loader SET managed_project_revision=? WHERE managed_project_id=?`},
		{operation: "advance legacy project scheduler projections", query: `UPDATE project_scheduler SET revision=? WHERE project_id=?`},
	} {
		if _, err := tx.ExecContext(ctx, update.query, revision, projectID); err != nil {
			return fmt.Errorf("%s: %w", update.operation, err)
		}
	}
	return nil
}
