package projects

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	domain "agent-compose/pkg/model"
)

// ProjectRef identifies a project through exactly one stable selector.
type ProjectRef struct {
	kind  projectRefKind
	value string
}

type projectRefKind uint8

const (
	projectRefUnset projectRefKind = iota
	projectRefID
	projectRefName
	projectRefSourcePath
)

// ProjectRefByID selects a project by its stable ID.
func ProjectRefByID(projectID string) ProjectRef {
	return ProjectRef{kind: projectRefID, value: projectID}
}

// ProjectRefByName selects a project by its exact name.
func ProjectRefByName(name string) ProjectRef {
	return ProjectRef{kind: projectRefName, value: name}
}

// ProjectRefBySourcePath selects a project by its normalized source path.
func ProjectRefBySourcePath(sourcePath string) ProjectRef {
	return ProjectRef{kind: projectRefSourcePath, value: sourcePath}
}

// ProjectRefStore provides the project lookups required to resolve a reference.
type ProjectRefStore interface {
	GetProject(context.Context, string) (domain.ProjectRecord, error)
	ListProjects(context.Context, domain.ProjectListOptions) (domain.ProjectListResult, error)
}

// ResolveProjectRef resolves one project selector against active projects.
func ResolveProjectRef(ctx context.Context, store ProjectRefStore, ref ProjectRef) (domain.ProjectRecord, error) {
	if store == nil {
		return domain.ProjectRecord{}, fmt.Errorf("project store is required")
	}
	value := strings.TrimSpace(ref.value)
	if value == "" {
		return domain.ProjectRecord{}, domain.ClassifyError(domain.ErrRequired, "project selector is required", nil)
	}
	switch ref.kind {
	case projectRefID:
		return store.GetProject(ctx, value)
	case projectRefName:
		return resolveProjectByExactMatch(ctx, store, value, false, "name", func(project domain.ProjectRecord) string {
			return project.Name
		})
	case projectRefSourcePath:
		value = domain.NormalizeProjectSourcePath(value)
		return resolveProjectByExactMatch(ctx, store, value, false, "source path", func(project domain.ProjectRecord) string {
			return domain.NormalizeProjectSourcePath(project.SourcePath)
		})
	default:
		return domain.ProjectRecord{}, domain.ClassifyError(domain.ErrRequired, "project selector is required", nil)
	}
}

func resolveProjectByExactMatch(
	ctx context.Context,
	store ProjectRefStore,
	value string,
	includeRemoved bool,
	selectorName string,
	projectValue func(domain.ProjectRecord) string,
) (domain.ProjectRecord, error) {
	const pageSize = 200
	var matches []domain.ProjectRecord
	for offset := 0; ; offset += pageSize {
		result, err := store.ListProjects(ctx, domain.ProjectListOptions{Query: value, IncludeRemoved: includeRemoved, Offset: offset, Limit: pageSize})
		if err != nil {
			return domain.ProjectRecord{}, err
		}
		for _, project := range result.Projects {
			if projectValue(project) == value {
				matches = append(matches, project)
			}
		}
		if !result.HasMore {
			break
		}
	}
	if len(matches) == 0 {
		return domain.ProjectRecord{}, domain.ResourceError(domain.ErrNotFound, "project", value, fmt.Sprintf("project with %s %s not found", selectorName, value), sql.ErrNoRows)
	}
	if len(matches) > 1 {
		return domain.ProjectRecord{}, domain.ClassifyError(domain.ErrAmbiguous, fmt.Sprintf("project %s %s is ambiguous; use project_id", selectorName, value), nil)
	}
	return matches[0], nil
}
