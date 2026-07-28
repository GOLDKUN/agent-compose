package configstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"agent-compose/pkg/compose"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/projects"
)

func (s *schedulerStore) loadSchedulerDefinition(ctx context.Context, schedulerID string) (domain.Scheduler, error) {
	schedulerID = strings.TrimSpace(schedulerID)
	if schedulerID == "" {
		return domain.Scheduler{}, fmt.Errorf("scheduler id is required")
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		s.id, s.short_id, s.project_id, s.id, s.agent_name,
		s.revision, s.enabled, s.trigger_count, s.spec_json, s.last_error,
		s.created_at, s.updated_at,
		p.name, p.short_id, p.source_path, p.source_json, p.current_revision,
		p.spec_hash, p.created_at, p.updated_at, p.removed_at, r.spec_json
		FROM project_scheduler s
		JOIN project p ON p.id = s.project_id
		JOIN project_revision r ON r.project_id = s.project_id AND r.revision = s.revision
		WHERE s.id = ?`, schedulerID)
	var scheduler domain.ProjectSchedulerRecord
	var project domain.ProjectRecord
	var enabled int
	var schedulerCreated, schedulerUpdated any
	var projectCreated, projectUpdated, projectRemoved any
	var revisionJSON string
	if err := row.Scan(
		&scheduler.ID, &scheduler.ShortID, &scheduler.ProjectID, &scheduler.SchedulerID,
		&scheduler.AgentName, &scheduler.Revision, &enabled, &scheduler.TriggerCount,
		&scheduler.SpecJSON, &scheduler.LastError, &schedulerCreated, &schedulerUpdated,
		&project.Name, &project.ShortID, &project.SourcePath, &project.SourceJSON,
		&project.CurrentRevision, &project.SpecHash, &projectCreated, &projectUpdated,
		&projectRemoved, &revisionJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Scheduler{}, domain.ResourceError(domain.ErrNotFound, "scheduler", schedulerID, fmt.Sprintf("scheduler %s not found", schedulerID), err)
		}
		return domain.Scheduler{}, fmt.Errorf("load scheduler definition %s: %w", schedulerID, err)
	}
	scheduler.Enabled = enabled != 0
	scheduler.CreatedAt = ParseStoredTime(schedulerCreated)
	scheduler.UpdatedAt = ParseStoredTime(schedulerUpdated)
	project.ID = scheduler.ProjectID
	project.CreatedAt = ParseStoredTime(projectCreated)
	project.UpdatedAt = ParseStoredTime(projectUpdated)
	project.RemovedAt = ParseStoredTime(projectRemoved)

	spec, err := compose.ParseCanonicalJSON([]byte(revisionJSON))
	if err != nil {
		return domain.Scheduler{}, fmt.Errorf("decode scheduler revision %s/%d: %w", scheduler.ProjectID, scheduler.Revision, err)
	}
	for _, agent := range spec.Agents {
		if agent.Name != scheduler.AgentName {
			continue
		}
		if agent.Scheduler == nil {
			return domain.Scheduler{}, fmt.Errorf("scheduler revision %s/%d agent %s has no scheduler", scheduler.ProjectID, scheduler.Revision, scheduler.AgentName)
		}
		definition, err := projects.NewSchedulerDefinition(project, scheduler, agent)
		if err != nil {
			return domain.Scheduler{}, fmt.Errorf("compile scheduler %s: %w", schedulerID, err)
		}
		definition.Summary.CreatedAt = scheduler.CreatedAt
		definition.Summary.UpdatedAt = scheduler.UpdatedAt
		definition.Summary.LastError = scheduler.LastError
		definition.Triggers, err = s.listSchedulerTriggers(ctx, schedulerID)
		if err != nil {
			return domain.Scheduler{}, err
		}
		if err := s.hydrateSchedulerSummaryCounts(ctx, &definition.Summary); err != nil {
			return domain.Scheduler{}, err
		}
		return definition, nil
	}
	return domain.Scheduler{}, fmt.Errorf("scheduler revision %s/%d missing agent %s", scheduler.ProjectID, scheduler.Revision, scheduler.AgentName)
}
