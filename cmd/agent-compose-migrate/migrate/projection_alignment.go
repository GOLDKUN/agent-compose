package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func alignManagedProjectionRevisions(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT project_id FROM (
		SELECT agent.project_id
		FROM project_agent AS agent
		JOIN agent_definition AS definition ON definition.id = agent.managed_agent_id
		WHERE definition.managed_project_id <> agent.project_id
		   OR definition.managed_project_revision <> agent.revision
		   OR definition.managed_agent_name <> agent.agent_name
		UNION
		SELECT scheduler.project_id
		FROM project_scheduler AS scheduler
		JOIN loader ON loader.id = scheduler.managed_loader_id
		WHERE loader.managed_project_id <> scheduler.project_id
		   OR loader.managed_project_revision <> scheduler.revision
		   OR loader.managed_agent_name <> scheduler.agent_name
		   OR loader.managed_scheduler_id <> scheduler.scheduler_id
	) ORDER BY project_id`)
	if err != nil {
		return nil, fmt.Errorf("find inconsistent managed projections: %w", err)
	}
	var projectIDs []string
	for rows.Next() {
		var projectID string
		if err := rows.Scan(&projectID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan inconsistent managed projection: %w", err)
		}
		projectIDs = append(projectIDs, projectID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close inconsistent managed projections: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inconsistent managed projections: %w", err)
	}

	warnings := make([]string, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		revision, err := appendProjectionRevision(ctx, db, projectID)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, fmt.Sprintf("appended project %s revision %d to preserve inconsistent managed projections", projectID, revision))
	}
	return warnings, nil
}

func appendProjectionRevision(ctx context.Context, db *sql.DB, projectID string) (int64, error) {
	var projectName string
	var createdAt int64
	var currentSpecJSON string
	if err := db.QueryRowContext(ctx, `SELECT project.name,project.updated_at,revision.spec_json
		FROM project JOIN project_revision AS revision
		ON revision.project_id=project.id AND revision.revision=project.current_revision
		WHERE project.id=?`, projectID).Scan(&projectName, &createdAt, &currentSpecJSON); err != nil {
		return 0, fmt.Errorf("load projected project %s: %w", projectID, err)
	}
	revision, err := nextLegacyRevision(ctx, db, projectID)
	if err != nil {
		return 0, err
	}
	agents, err := projectedRevisionAgents(ctx, db, projectID)
	if err != nil {
		return 0, err
	}
	if len(agents) == 0 {
		return 0, fmt.Errorf("project %s has inconsistent projections but no project agents", projectID)
	}
	spec, err := preservedProjectionSpec(currentSpecJSON, projectName, agents)
	if err != nil {
		return 0, fmt.Errorf("preserve current revision for project %s: %w", projectID, err)
	}
	specData, err := json.Marshal(spec)
	if err != nil {
		return 0, fmt.Errorf("marshal projection revision for project %s: %w", projectID, err)
	}
	specSum := sha256.Sum256(specData)
	specHash := hex.EncodeToString(specSum[:])

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin projection revision for project %s: %w", projectID, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES(?,?,?,?,?)`, projectID, revision, specHash, string(specData), createdAt); err != nil {
		return 0, fmt.Errorf("append projection revision for project %s: %w", projectID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_definition SET managed_project_id=?, managed_project_revision=?, managed_agent_name=(SELECT agent_name FROM project_agent WHERE managed_agent_id=agent_definition.id) WHERE id IN (SELECT managed_agent_id FROM project_agent WHERE project_id=?)`, projectID, revision, projectID); err != nil {
		return 0, fmt.Errorf("align agent projections for project %s: %w", projectID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_agent SET revision=? WHERE project_id=?`, revision, projectID); err != nil {
		return 0, fmt.Errorf("advance agent projections for project %s: %w", projectID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE loader SET
		managed_project_id=?, managed_project_revision=?,
		managed_agent_name=(SELECT agent_name FROM project_scheduler WHERE managed_loader_id=loader.id),
		managed_scheduler_id=(SELECT scheduler_id FROM project_scheduler WHERE managed_loader_id=loader.id)
		WHERE id IN (SELECT managed_loader_id FROM project_scheduler WHERE project_id=?)`, projectID, revision, projectID); err != nil {
		return 0, fmt.Errorf("align scheduler projections for project %s: %w", projectID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_scheduler SET revision=? WHERE project_id=?`, revision, projectID); err != nil {
		return 0, fmt.Errorf("advance scheduler projections for project %s: %w", projectID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project SET current_revision=?, spec_hash=?, updated_at=? WHERE id=?`, revision, specHash, createdAt, projectID); err != nil {
		return 0, fmt.Errorf("activate projection revision for project %s: %w", projectID, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit projection revision for project %s: %w", projectID, err)
	}
	return revision, nil
}

func preservedProjectionSpec(currentSpecJSON, projectName string, agents []map[string]any) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(currentSpecJSON))
	decoder.UseNumber()
	var spec map[string]any
	if err := decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode current project revision: %w", err)
	}
	if spec == nil {
		return nil, fmt.Errorf("current project revision must be a JSON object")
	}
	if name, ok := spec["name"].(string); !ok || strings.TrimSpace(name) == "" {
		spec["name"] = projectName
	}
	spec["agents"] = agents
	return spec, nil
}

func projectedRevisionAgents(ctx context.Context, db *sql.DB, projectID string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `SELECT definition.id,definition.name,definition.description,definition.enabled,definition.deleted_at,
		definition.provider,definition.model,definition.system_prompt,definition.driver,definition.guest_image,definition.workspace_id,
		definition.env_json,definition.volumes_json,definition.config_json,definition.capset_ids,definition.skills,
		definition.created_at,definition.updated_at,agent.agent_name
		FROM project_agent AS agent JOIN agent_definition AS definition ON definition.id=agent.managed_agent_id
		WHERE agent.project_id=? ORDER BY agent.agent_name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("load agent projections for project %s: %w", projectID, err)
	}
	type projectedAgent struct {
		definition legacyAgentDefinition
		name       string
	}
	var projected []projectedAgent
	for rows.Next() {
		var item legacyAgentDefinition
		var agentName string
		if err := rows.Scan(&item.id, &item.name, &item.description, &item.enabled, &item.deletedAt, &item.provider, &item.model, &item.systemPrompt, &item.driver, &item.image, &item.workspaceID, &item.envJSON, &item.volumesJSON, &item.configJSON, &item.capsetIDs, &item.skills, &item.createdAt, &item.updatedAt, &agentName); err != nil {
			return nil, fmt.Errorf("scan agent projection for project %s: %w", projectID, err)
		}
		projected = append(projected, projectedAgent{definition: item, name: agentName})
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close agent projections for project %s: %w", projectID, err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent projections for project %s: %w", projectID, err)
	}
	agents := make([]map[string]any, 0, len(projected))
	for _, item := range projected {
		scheduler, err := projectedAgentScheduler(ctx, db, projectID, item.name)
		if err != nil {
			return nil, err
		}
		agents = append(agents, legacyAgentJSON(item.definition, item.name, scheduler))
	}
	return agents, nil
}

func projectedAgentScheduler(ctx context.Context, db *sql.DB, projectID, agentName string) (map[string]any, error) {
	rows, err := db.QueryContext(ctx, `SELECT loader.id,loader.name,loader.description,loader.runtime,loader.script,loader.workspace_id,loader.agent_id,
		loader.driver,loader.guest_image,loader.default_agent,loader.sandbox_policy,loader.concurrency_policy,loader.capset_ids,
		loader.env_json,loader.volumes_json,loader.last_error,loader.enabled,loader.created_at,loader.updated_at
		FROM project_scheduler JOIN loader ON loader.id=project_scheduler.managed_loader_id
		WHERE project_scheduler.project_id=? AND project_scheduler.agent_name=? ORDER BY project_scheduler.scheduler_id`, projectID, agentName)
	if err != nil {
		return nil, fmt.Errorf("load scheduler projection for project %s agent %s: %w", projectID, agentName, err)
	}
	defer func() { _ = rows.Close() }()
	var schedulers []legacySchedulerDefinition
	for rows.Next() {
		var item legacySchedulerDefinition
		if err := rows.Scan(&item.id, &item.name, &item.description, &item.runtime, &item.script, &item.workspaceID, &item.agentID, &item.driver, &item.image, &item.defaultAgent, &item.sandboxPolicy, &item.concurrencyPolicy, &item.capsetIDs, &item.envJSON, &item.volumesJSON, &item.lastError, &item.enabled, &item.createdAt, &item.updatedAt); err != nil {
			return nil, fmt.Errorf("scan scheduler projection for project %s agent %s: %w", projectID, agentName, err)
		}
		schedulers = append(schedulers, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduler projections for project %s agent %s: %w", projectID, agentName, err)
	}
	if len(schedulers) > 1 {
		return nil, fmt.Errorf("project %s agent %s has multiple legacy scheduler projections", projectID, agentName)
	}
	if len(schedulers) == 0 {
		return nil, nil
	}
	return legacySchedulerJSON(schedulers[0]), nil
}
