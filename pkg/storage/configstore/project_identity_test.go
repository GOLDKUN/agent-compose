package configstore

import (
	"context"
	"errors"
	"testing"

	domain "agent-compose/pkg/model"
	"agent-compose/internal/projects"
)

func TestProjectNameIsUniqueAndKeepsIdentityAcrossRenameAndSourceMoves(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	created, err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "legacy-path-id", Name: "demo", SourcePath: "/old/agent-compose.yml"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	byName, err := store.GetProjectByName(ctx, "demo", false)
	if err != nil || byName.ID != created.ID {
		t.Fatalf("GetProjectByName = %#v, %v", byName, err)
	}
	if _, err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "other-id", Name: "demo", SourcePath: "/other/agent-compose.yml"}); err == nil {
		t.Fatal("duplicate project name was accepted")
	}
	renamed, err := store.UpsertProject(ctx, domain.ProjectRecord{ID: created.ID, Name: "renamed", SourcePath: created.SourcePath})
	if err != nil {
		t.Fatalf("rename project: %v", err)
	}
	if renamed.ID != created.ID || renamed.Name != "renamed" {
		t.Fatalf("renamed project = %#v, want stable id %s", renamed, created.ID)
	}
	if _, err := store.GetProjectByName(ctx, "demo", true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("old project name lookup error = %v, want not found", err)
	}
	if _, err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "other-id", Name: "other", SourcePath: "/other/agent-compose.yml"}); err != nil {
		t.Fatalf("create second project: %v", err)
	}
	if _, err := store.UpsertProject(ctx, domain.ProjectRecord{ID: created.ID, Name: "other", SourcePath: created.SourcePath}); err == nil {
		t.Fatal("rename to an existing project name was accepted")
	}
	unchanged, err := store.GetProject(ctx, created.ID)
	if err != nil || unchanged.Name != "renamed" {
		t.Fatalf("project after rejected conflicting rename = %#v, %v", unchanged, err)
	}
	if _, err := store.MarkProjectRemoved(ctx, created.ID); err != nil {
		t.Fatalf("mark project removed: %v", err)
	}
	if _, err := store.GetProjectByName(ctx, "renamed", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("active lookup after down error = %v, want not found", err)
	}
	removed, err := store.GetProjectByName(ctx, "renamed", true)
	if err != nil || removed.ID != created.ID {
		t.Fatalf("removed lookup = %#v, %v", removed, err)
	}
	reactivated, err := store.UpsertProject(ctx, domain.ProjectRecord{ID: created.ID, Name: "renamed", SourcePath: "/moved/agent-compose.yml"})
	if err != nil {
		t.Fatalf("reactivate moved project: %v", err)
	}
	if reactivated.ID != created.ID || reactivated.SourcePath != "/moved/agent-compose.yml" || !reactivated.RemovedAt.IsZero() {
		t.Fatalf("reactivated project = %#v", reactivated)
	}
}

func TestResolveProjectByExactUnicodeSourcePath(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	const sourcePath = "/tmp/Äpp/agent-compose.yml"
	created, err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "unicode-path", Name: "unicode-path", SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	resolved, err := projects.ResolveProjectRef(ctx, store, projects.ProjectRefBySourcePath(sourcePath))
	if err != nil {
		t.Fatalf("resolve exact Unicode source path: %v", err)
	}
	if resolved.ID != created.ID {
		t.Fatalf("resolved project = %#v, want %s", resolved, created.ID)
	}
}

func TestResolveProjectBySourcePathRejectsAmbiguousRows(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	const sourcePath = "/tmp/shared/agent-compose.yml"
	for _, project := range []domain.ProjectRecord{
		{ID: "shared-one", Name: "shared-one", SourcePath: sourcePath},
		{ID: "shared-two", Name: "shared-two", SourcePath: sourcePath},
	} {
		if _, err := store.UpsertProject(ctx, project); err != nil {
			t.Fatalf("create project %s: %v", project.ID, err)
		}
	}

	if _, err := projects.ResolveProjectRef(ctx, store, projects.ProjectRefBySourcePath(sourcePath)); !errors.Is(err, domain.ErrAmbiguous) {
		t.Fatalf("resolve ambiguous source path error = %v, want ErrAmbiguous", err)
	}
}
