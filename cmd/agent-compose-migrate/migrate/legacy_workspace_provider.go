package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"agent-compose/pkg/compose"
	"agent-compose/pkg/projects"
)

type legacyWorkspaceRevision struct {
	projectID string
	revision  int64
	specJSON  string
}

func normalizeLegacyWorkspaceProviders(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT project_id,revision,spec_json FROM project_revision ORDER BY project_id,revision`)
	if err != nil {
		return fmt.Errorf("list project revisions with legacy workspace providers: %w", err)
	}
	var revisions []legacyWorkspaceRevision
	for rows.Next() {
		var revision legacyWorkspaceRevision
		if err := rows.Scan(&revision.projectID, &revision.revision, &revision.specJSON); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan project revision with legacy workspace providers: %w", err)
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close project revisions with legacy workspace providers: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate project revisions with legacy workspace providers: %w", err)
	}

	for _, revision := range revisions {
		if err := normalizeLegacyWorkspaceRevision(ctx, db, revision); err != nil {
			return err
		}
	}
	return nil
}

func normalizeLegacyWorkspaceRevision(ctx context.Context, db *sql.DB, revision legacyWorkspaceRevision) error {
	spec, err := compose.ParseCanonicalJSON([]byte(revision.specJSON))
	if err != nil {
		return fmt.Errorf("decode project revision %s/%d for workspace provider migration: %w", revision.projectID, revision.revision, err)
	}
	if !replaceLegacyLocalWorkspaceProviders(spec) {
		return nil
	}
	specJSON, err := spec.MarshalCanonicalJSON(false)
	if err != nil {
		return fmt.Errorf("encode project revision %s/%d after workspace provider migration: %w", revision.projectID, revision.revision, err)
	}
	specHash, err := spec.Hash()
	if err != nil {
		return fmt.Errorf("hash project revision %s/%d after workspace provider migration: %w", revision.projectID, revision.revision, err)
	}
	agents, err := projects.NewAgentRecordsFromSpec(revision.projectID, revision.revision, spec)
	if err != nil {
		return fmt.Errorf("derive project agents for revision %s/%d after workspace provider migration: %w", revision.projectID, revision.revision, err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin workspace provider migration for project revision %s/%d: %w", revision.projectID, revision.revision, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE project_revision SET spec_hash=?,spec_json=? WHERE project_id=? AND revision=?`, specHash, string(specJSON), revision.projectID, revision.revision); err != nil {
		return fmt.Errorf("update workspace providers for project revision %s/%d: %w", revision.projectID, revision.revision, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project SET spec_hash=? WHERE id=? AND current_revision=?`, specHash, revision.projectID, revision.revision); err != nil {
		return fmt.Errorf("update project hash for workspace provider migration %s/%d: %w", revision.projectID, revision.revision, err)
	}
	for _, agent := range agents {
		if _, err := tx.ExecContext(ctx, `UPDATE project_agent SET spec_json=? WHERE project_id=? AND revision=? AND agent_name=?`, agent.SpecJSON, revision.projectID, revision.revision, agent.AgentName); err != nil {
			return fmt.Errorf("update workspace provider for project agent %s/%d/%s: %w", revision.projectID, revision.revision, agent.AgentName, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workspace provider migration for project revision %s/%d: %w", revision.projectID, revision.revision, err)
	}
	return nil
}

func replaceLegacyLocalWorkspaceProviders(spec *compose.NormalizedProjectSpec) bool {
	if spec == nil {
		return false
	}
	changed := false
	for name, workspace := range spec.Workspaces {
		if isLegacyLocalWorkspaceProvider(workspace.Provider) {
			workspace.Provider = "file"
			spec.Workspaces[name] = workspace
			changed = true
		}
	}
	for index := range spec.Agents {
		workspace := spec.Agents[index].Workspace
		if workspace != nil && isLegacyLocalWorkspaceProvider(workspace.Provider) {
			workspace.Provider = "file"
			changed = true
		}
	}
	return changed
}

func isLegacyLocalWorkspaceProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "local")
}
