package configstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"agent-compose/internal/projects"
	domain "agent-compose/pkg/model"
)

func (s *projectStore) GetProjectByName(ctx context.Context, name string, includeRemoved bool) (ProjectRecord, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ProjectRecord{}, fmt.Errorf("project name is required")
	}
	where := "name = ? AND removed_at = 0"
	if includeRemoved {
		where = "name = ?"
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, name, short_id, source_path, source_json, current_revision, spec_hash, created_at, updated_at, removed_at
		FROM project WHERE `+where, name)
	item, err := projects.ScanProject(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProjectRecord{}, domain.ResourceError(domain.ErrNotFound, "project", name, fmt.Sprintf("project with name %s not found", name), err)
		}
		return ProjectRecord{}, err
	}
	return item, nil
}

func (s *projectStore) GetProjectBySourcePath(ctx context.Context, sourcePath string, includeRemoved bool) (ProjectRecord, error) {
	sourcePath = projects.NormalizeProjectSourcePath(sourcePath)
	if sourcePath == "" {
		return ProjectRecord{}, fmt.Errorf("project source path is required")
	}
	where := "source_path = ? AND removed_at = 0"
	if includeRemoved {
		where = "source_path = ?"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, short_id, source_path, source_json, current_revision, spec_hash, created_at, updated_at, removed_at
		FROM project WHERE `+where+` ORDER BY updated_at DESC, created_at DESC, id ASC LIMIT 2`, sourcePath)
	if err != nil {
		return ProjectRecord{}, fmt.Errorf("query project by source path %s: %w", sourcePath, err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]ProjectRecord, 0, 2)
	for rows.Next() {
		item, err := projects.ScanProject(rows.Scan)
		if err != nil {
			return ProjectRecord{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ProjectRecord{}, fmt.Errorf("iterate projects by source path %s: %w", sourcePath, err)
	}
	if len(items) == 0 {
		return ProjectRecord{}, domain.ResourceError(domain.ErrNotFound, "project", sourcePath, fmt.Sprintf("project with source path %s not found", sourcePath), sql.ErrNoRows)
	}
	if len(items) > 1 {
		return ProjectRecord{}, domain.ClassifyError(domain.ErrAmbiguous, fmt.Sprintf("project source path %s is ambiguous; use project_id", sourcePath), nil)
	}
	return items[0], nil
}
