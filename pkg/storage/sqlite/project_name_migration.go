package sqlite

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"agent-compose/pkg/compose"
)

const uniqueProjectNameMigrationVersion int64 = 9

const uniqueProjectNameMigration = "000009_unique_project_name"

type projectNameMigrationRow struct {
	id              string
	name            string
	currentRevision int64
}

func applyMigrationDataStep(ctx context.Context, conn migrationConn, item migration) error {
	// Released cases are immutable migration steps, just like their SQL asset.
	if item.version != uniqueProjectNameMigrationVersion || item.name != uniqueProjectNameMigration {
		return nil
	}
	return renameDuplicateProjects(ctx, conn)
}

func renameDuplicateProjects(ctx context.Context, conn migrationConn) error {
	rows, err := conn.QueryContext(ctx, `SELECT id, name, current_revision
		FROM project
		ORDER BY name ASC,
			CASE WHEN removed_at = 0 THEN 0 ELSE 1 END,
			updated_at DESC,
			created_at DESC,
			id ASC`)
	if err != nil {
		return fmt.Errorf("list projects before enforcing unique names: %w", err)
	}

	var projects []projectNameMigrationRow
	usedNames := make(map[string]struct{})
	for rows.Next() {
		var project projectNameMigrationRow
		if err := rows.Scan(&project.id, &project.name, &project.currentRevision); err != nil {
			_ = rows.Close() // Preserve the scan error as the primary failure.
			return fmt.Errorf("scan project before enforcing unique names: %w", err)
		}
		projects = append(projects, project)
		usedNames[project.name] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close projects before enforcing unique names: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate projects before enforcing unique names: %w", err)
	}

	seenNames := make(map[string]struct{}, len(usedNames))
	for _, project := range projects {
		if _, seen := seenNames[project.name]; !seen {
			seenNames[project.name] = struct{}{}
			continue
		}
		newName, ok := nextAvailableProjectName(project.name, usedNames, len(projects))
		if !ok {
			return fmt.Errorf("allocate unique name for duplicate project %s", project.id)
		}
		if err := renameDuplicateProject(ctx, conn, project, newName); err != nil {
			return err
		}
		usedNames[newName] = struct{}{}
	}
	return nil
}

func renameDuplicateProject(ctx context.Context, conn migrationConn, project projectNameMigrationRow, newName string) error {
	if project.currentRevision <= 0 {
		return updateMigratedProject(ctx, conn, project, newName, 0, "")
	}

	revision, specHash, err := createRenamedProjectRevision(ctx, conn, project, newName)
	if err != nil {
		return err
	}
	if err := updateMigratedProject(ctx, conn, project, newName, revision, specHash); err != nil {
		return err
	}
	for _, table := range []string{"project_agent", "project_scheduler"} {
		if _, err := conn.ExecContext(ctx, `UPDATE `+table+` SET revision = ? WHERE project_id = ?`, revision, project.id); err != nil {
			return fmt.Errorf("move %s rows for renamed project %s to revision %d: %w", table, project.id, revision, err)
		}
	}
	return nil
}

func createRenamedProjectRevision(ctx context.Context, conn migrationConn, project projectNameMigrationRow, newName string) (int64, string, error) {
	var specJSON string
	if err := conn.QueryRowContext(ctx, `SELECT spec_json FROM project_revision WHERE project_id = ? AND revision = ?`, project.id, project.currentRevision).Scan(&specJSON); err != nil {
		return 0, "", fmt.Errorf("load current revision %s/%d before rename: %w", project.id, project.currentRevision, err)
	}
	spec, err := compose.ParseCanonicalJSON([]byte(specJSON))
	if err != nil {
		return 0, "", fmt.Errorf("decode current revision %s/%d before rename: %w", project.id, project.currentRevision, err)
	}
	spec.Name = newName
	if err := materializeImplicitProjectVolumeNames(ctx, conn, project.id, spec); err != nil {
		return 0, "", err
	}
	canonicalJSON, err := spec.MarshalCanonicalJSON(false)
	if err != nil {
		return 0, "", fmt.Errorf("encode renamed project revision %s: %w", project.id, err)
	}
	specHash, err := spec.Hash()
	if err != nil {
		return 0, "", fmt.Errorf("hash renamed project revision %s: %w", project.id, err)
	}
	var nextRevision int64
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) + 1 FROM project_revision WHERE project_id = ?`, project.id).Scan(&nextRevision); err != nil {
		return 0, "", fmt.Errorf("allocate renamed project revision %s: %w", project.id, err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO project_revision(project_id, revision, spec_hash, spec_json) VALUES(?, ?, ?, ?)`, project.id, nextRevision, specHash, string(canonicalJSON)); err != nil {
		return 0, "", fmt.Errorf("insert renamed project revision %s/%d: %w", project.id, nextRevision, err)
	}
	return nextRevision, specHash, nil
}

func materializeImplicitProjectVolumeNames(ctx context.Context, conn migrationConn, projectID string, spec *compose.NormalizedProjectSpec) error {
	if spec == nil || len(spec.Volumes) == 0 {
		return nil
	}
	rows, err := conn.QueryContext(ctx, `SELECT project_volumes.volume_key, volumes.name
		FROM project_volumes
		JOIN volumes ON volumes.id = project_volumes.volume_id
		WHERE project_volumes.project_id = ?`, projectID)
	if err != nil {
		return fmt.Errorf("list volumes for renamed project %s: %w", projectID, err)
	}
	linkedNames := make(map[string]string)
	for rows.Next() {
		var key, name string
		if err := rows.Scan(&key, &name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan volume for renamed project %s: %w", projectID, err)
		}
		linkedNames[key] = name
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close volumes for renamed project %s: %w", projectID, err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate volumes for renamed project %s: %w", projectID, err)
	}
	for key, volume := range spec.Volumes {
		if strings.TrimSpace(volume.Name) != "" {
			continue
		}
		if name := strings.TrimSpace(linkedNames[key]); name != "" {
			volume.Name = name
			spec.Volumes[key] = volume
		}
	}
	return nil
}

func updateMigratedProject(ctx context.Context, conn migrationConn, project projectNameMigrationRow, newName string, revision int64, specHash string) error {
	query := `UPDATE project SET name = ? WHERE id = ? AND name = ?`
	args := []any{newName, project.id, project.name}
	if revision > 0 {
		query = `UPDATE project SET name = ?, current_revision = ?, spec_hash = ? WHERE id = ? AND name = ? AND current_revision = ?`
		args = []any{newName, revision, specHash, project.id, project.name, project.currentRevision}
	}
	result, err := conn.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("rename duplicate project %s to %s: %w", project.id, newName, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count renamed duplicate project %s: %w", project.id, err)
	}
	if updated != 1 {
		return fmt.Errorf("rename duplicate project %s updated %d rows", project.id, updated)
	}
	return nil
}

func nextAvailableProjectName(name string, used map[string]struct{}, projectCount int) (string, bool) {
	for suffix := 2; suffix <= projectCount+1; suffix++ {
		candidate := name + "-" + strconv.Itoa(suffix)
		if _, exists := used[candidate]; !exists {
			return candidate, true
		}
	}
	return "", false
}
