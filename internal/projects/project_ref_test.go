package projects

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestResolveProjectRefSelectors(t *testing.T) {
	t.Parallel()
	sourcePath := filepath.Join(t.TempDir(), "agent-compose.yml")
	store := projectRefStoreFake{projects: []domain.ProjectRecord{
		{ID: "project-1", Name: "alpha", SourcePath: sourcePath},
		{ID: "project-2", Name: "alphabet", SourcePath: "/projects/other.yml"},
	}}

	tests := []struct {
		name string
		ref  ProjectRef
	}{
		{name: "project id", ref: ProjectRefByID(" project-1 ")},
		{name: "exact name", ref: ProjectRefByName(" alpha ")},
		{name: "normalized source path", ref: ProjectRefBySourcePath(filepath.Join(filepath.Dir(sourcePath), ".", filepath.Base(sourcePath)))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project, err := ResolveProjectRef(context.Background(), store, test.ref)
			if err != nil || project.ID != "project-1" {
				t.Fatalf("ResolveProjectRef() = %#v, %v; want project-1", project, err)
			}
		})
	}
}

func TestResolveProjectRefRejectsEmptySelectors(t *testing.T) {
	t.Parallel()
	store := projectRefStoreFake{}
	for _, ref := range []ProjectRef{
		{},
		ProjectRefByID(" "),
		ProjectRefByName(" "),
		ProjectRefBySourcePath(" "),
	} {
		if _, err := ResolveProjectRef(context.Background(), store, ref); !errors.Is(err, domain.ErrRequired) {
			t.Fatalf("ResolveProjectRef(%#v) error = %v, want ErrRequired", ref, err)
		}
	}
}

func TestResolveProjectRefRejectsAmbiguousSelectors(t *testing.T) {
	t.Parallel()
	store := projectRefStoreFake{projects: []domain.ProjectRecord{
		{ID: "project-1", Name: "duplicate", SourcePath: "/projects/shared.yml"},
		{ID: "project-2", Name: "duplicate", SourcePath: "/projects/shared.yml"},
	}}
	for _, ref := range []ProjectRef{
		ProjectRefByName("duplicate"),
		ProjectRefBySourcePath("/projects/shared.yml"),
	} {
		if _, err := ResolveProjectRef(context.Background(), store, ref); !errors.Is(err, domain.ErrAmbiguous) {
			t.Fatalf("ResolveProjectRef(%#v) error = %v, want ErrAmbiguous", ref, err)
		}
	}
}

type projectRefStoreFake struct {
	projects []domain.ProjectRecord
}

func (s projectRefStoreFake) GetProject(_ context.Context, projectID string) (domain.ProjectRecord, error) {
	for _, project := range s.projects {
		if project.ID == projectID {
			return project, nil
		}
	}
	return domain.ProjectRecord{}, domain.ResourceError(domain.ErrNotFound, "project", projectID, "", nil)
}

func (s projectRefStoreFake) ListProjects(_ context.Context, options domain.ProjectListOptions) (domain.ProjectListResult, error) {
	query := strings.ToLower(strings.TrimSpace(options.Query))
	var matches []domain.ProjectRecord
	for _, project := range s.projects {
		if query == "" || RecordMatchesQuery(project, query) {
			matches = append(matches, project)
		}
	}
	start := min(options.Offset, len(matches))
	end := min(start+options.Limit, len(matches))
	return domain.ProjectListResult{Projects: matches[start:end], HasMore: end < len(matches)}, nil
}
