package projects

import (
	"context"
	"fmt"
	"testing"

	"github.com/chaitin/agent-compose/pkg/compose"
	domain "github.com/chaitin/agent-compose/pkg/model"
)

type projectVolumeManagerStub struct {
	ensured      map[string]domain.VolumeRecord
	inspected    map[string]domain.VolumeRecord
	listed       map[string]domain.VolumeRecord
	replaced     map[string]domain.ProjectVolumeLink
	replacedProj string
	removeCalls  []string
}

func (m *projectVolumeManagerStub) ListProjectVolumes(_ context.Context, _ string) (map[string]domain.VolumeRecord, error) {
	result := make(map[string]domain.VolumeRecord, len(m.listed))
	for key, record := range m.listed {
		result[key] = record
	}
	return result, nil
}

func (m *projectVolumeManagerStub) Ensure(_ context.Context, item domain.VolumeRecord) (domain.VolumeRecord, bool, error) {
	if record, ok := m.ensured[item.Name]; ok {
		return record, false, nil
	}
	return domain.VolumeRecord{}, false, fmt.Errorf("unexpected Ensure %s", item.Name)
}

func (m *projectVolumeManagerStub) Inspect(_ context.Context, nameOrID string) (domain.VolumeRecord, error) {
	if record, ok := m.inspected[nameOrID]; ok {
		return record, nil
	}
	return domain.VolumeRecord{}, fmt.Errorf("unexpected Inspect %s", nameOrID)
}

func (m *projectVolumeManagerStub) ReplaceProjectVolumes(_ context.Context, projectID string, links map[string]domain.ProjectVolumeLink) error {
	m.replacedProj = projectID
	m.replaced = make(map[string]domain.ProjectVolumeLink, len(links))
	for key, link := range links {
		m.replaced[key] = link
	}
	return nil
}

func (m *projectVolumeManagerStub) RemoveProjectVolumes(_ context.Context, projectID string) error {
	m.removeCalls = append(m.removeCalls, projectID)
	return nil
}

func TestEnsureProjectVolumesReplacesCurrentLinks(t *testing.T) {
	ctx := context.Background()
	manager := &projectVolumeManagerStub{ensured: map[string]domain.VolumeRecord{
		"demo_cache":  {ID: "vol-cache", Name: "demo_cache"},
		"custom-logs": {ID: "vol-logs", Name: "custom-logs"},
	}}
	controller := &Controller{volumes: manager}
	project := domain.ProjectRecord{ID: "project-1", Name: "demo"}
	spec := &compose.NormalizedProjectSpec{
		Name: "demo",
		Volumes: map[string]compose.NormalizedVolumeSpec{
			"cache": {Driver: domain.VolumeDriverLocal},
			"logs":  {Name: "custom-logs", Driver: domain.VolumeDriverLocal},
		},
	}
	if err := controller.ensureProjectVolumes(ctx, project, spec); err != nil {
		t.Fatalf("ensureProjectVolumes: %v", err)
	}
	if manager.replacedProj != project.ID {
		t.Fatalf("replaced project = %q, want %q", manager.replacedProj, project.ID)
	}
	if len(manager.replaced) != 2 || manager.replaced["cache"].VolumeID != "vol-cache" || manager.replaced["logs"].VolumeID != "vol-logs" {
		t.Fatalf("replaced links = %#v", manager.replaced)
	}
}

func TestEnsureProjectVolumesClearsRemovedLinksWhenSpecEmpty(t *testing.T) {
	ctx := context.Background()
	manager := &projectVolumeManagerStub{}
	controller := &Controller{volumes: manager}
	project := domain.ProjectRecord{ID: "project-1", Name: "demo"}
	if err := controller.ensureProjectVolumes(ctx, project, &compose.NormalizedProjectSpec{Name: "demo"}); err != nil {
		t.Fatalf("ensureProjectVolumes empty spec: %v", err)
	}
	if manager.replacedProj != project.ID {
		t.Fatalf("replaced project = %q, want %q", manager.replacedProj, project.ID)
	}
	if len(manager.replaced) != 0 {
		t.Fatalf("replaced links = %#v, want empty", manager.replaced)
	}
}

func TestEnsureProjectVolumesReusesImplicitVolumeAfterProjectRename(t *testing.T) {
	ctx := context.Background()
	manager := &projectVolumeManagerStub{
		listed: map[string]domain.VolumeRecord{
			"cache": {ID: "vol-cache", Name: "demo_cache"},
		},
		ensured: map[string]domain.VolumeRecord{
			"demo_cache": {ID: "vol-cache", Name: "demo_cache"},
		},
	}
	controller := &Controller{volumes: manager}
	project := domain.ProjectRecord{ID: "project-1", Name: "demo-2"}
	spec := &compose.NormalizedProjectSpec{
		Name: "demo-2",
		Volumes: map[string]compose.NormalizedVolumeSpec{
			"cache": {Driver: domain.VolumeDriverLocal},
		},
	}

	if err := controller.ensureProjectVolumes(ctx, project, spec); err != nil {
		t.Fatalf("ensureProjectVolumes after rename: %v", err)
	}
	if link := manager.replaced["cache"]; link.VolumeID != "vol-cache" {
		t.Fatalf("cache link = %#v, want preserved volume vol-cache", link)
	}
}
