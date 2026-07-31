package sandboxes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sandboxes"
	"agent-compose/pkg/storage/sandboxstore"
)

func TestWorkspaceCleanerReusesCommittedArchiveAfterMetadataFailure(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	failingStore := &archiveCompletionFailingStore{Store: store, failures: 1}
	archiveRoot := filepath.Join(t.TempDir(), "archives")
	cleaner := &sandboxes.WorkspaceCleaner{
		Store: failingStore, Locks: sandboxes.NewLifecycleLocks(),
		ArchiveRoot: archiveRoot, SandboxRoot: workspaceCleanupSandboxRoot(store.SandboxDir(sandbox.Summary.ID)),
		Now: func() time.Time { return now },
	}

	first, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err == nil || first.Failed != 1 {
		t.Fatalf("first cleanup result/error = %#v/%v", first, err)
	}
	archives, err := filepath.Glob(filepath.Join(archiveRoot, sandbox.Summary.ID, "*.tar.zst"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("committed archives after metadata failure = %v, error %v", archives, err)
	}
	latePath := filepath.Join(store.SandboxDir(sandbox.Summary.ID), "state", "written-after-commit.txt")
	if err := os.WriteFile(latePath, []byte("must not enter committed archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.Matched != 1 || second.Failed != 0 {
		t.Fatalf("second cleanup result = %#v", second)
	}
	entries := readArchiveEntries(t, archives[0])
	if _, exists := entries["sandbox/state/written-after-commit.txt"]; exists {
		t.Fatal("archive retry rewrote an already committed archive")
	}
	loaded, err := store.GetSandbox(context.Background(), sandbox.Summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Archive == nil || loaded.Archive.State != domain.SandboxArchiveStateArchived {
		t.Fatalf("archive state after retry = %#v", loaded.Archive)
	}
}

func TestWorkspaceCleanerRevalidatesArchiveBeforeRemoval(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	removal := &workspaceCleanupTestRemoval{store: store, failures: 1}
	archiveRoot := filepath.Join(t.TempDir(), "archives")
	cleaner := &sandboxes.WorkspaceCleaner{
		Store: store, Locks: sandboxes.NewLifecycleLocks(), ArchiveRoot: archiveRoot,
		SandboxRoot: workspaceCleanupSandboxRoot(store.SandboxDir(sandbox.Summary.ID)), Removal: removal,
		Now: func() time.Time { return now },
	}

	first, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err == nil || first.Failed != 1 || removal.calls != 1 {
		t.Fatalf("first cleanup result/error/calls = %#v/%v/%d", first, err, removal.calls)
	}
	archives, err := filepath.Glob(filepath.Join(archiveRoot, sandbox.Summary.ID, "*.tar.zst"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("committed archives = %v, error %v", archives, err)
	}
	if err := os.Remove(archives[0]); err != nil {
		t.Fatal(err)
	}

	second, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err == nil || second.Failed != 1 {
		t.Fatalf("second cleanup result/error = %#v/%v", second, err)
	}
	if removal.calls != 1 {
		t.Fatalf("removal calls after archive loss = %d, want 1", removal.calls)
	}
	if _, err := os.Stat(store.SandboxDir(sandbox.Summary.ID)); err != nil {
		t.Fatalf("archive loss removed sandbox originals: %v", err)
	}
}

func TestWorkspaceCleanerRejectsEscapingArchiveSymlinks(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{name: "absolute", target: filepath.Join(string(filepath.Separator), "outside")},
		{name: "parent", target: filepath.Join("..", "..", "outside")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
			store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
			sandboxDir := store.SandboxDir(sandbox.Summary.ID)
			linkPath := filepath.Join(sandboxDir, "state", "outside")
			if err := os.Symlink(test.target, linkPath); err != nil {
				t.Skipf("create test symlink: %v", err)
			}
			archiveRoot := filepath.Join(t.TempDir(), "archives")
			cleaner := &sandboxes.WorkspaceCleaner{
				Store: store, Locks: sandboxes.NewLifecycleLocks(), ArchiveRoot: archiveRoot,
				SandboxRoot: workspaceCleanupSandboxRoot(sandboxDir), Now: func() time.Time { return now },
			}

			result, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
			if err == nil || result.Failed != 1 {
				t.Fatalf("cleanup result/error = %#v/%v", result, err)
			}
			archives, globErr := filepath.Glob(filepath.Join(archiveRoot, sandbox.Summary.ID, "*.tar.zst"))
			if globErr != nil || len(archives) != 0 {
				t.Fatalf("escaping symlink produced committed archives %v, error %v", archives, globErr)
			}
			if _, err := os.Lstat(linkPath); err != nil {
				t.Fatalf("archive rejection removed original symlink: %v", err)
			}
		})
	}
}

func TestWorkspaceCleanerAllowsInternalArchiveSymlink(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	sandboxDir := store.SandboxDir(sandbox.Summary.ID)
	linkPath := filepath.Join(sandboxDir, "state", "logs")
	if err := os.Symlink(filepath.Join("..", "logs"), linkPath); err != nil {
		t.Skipf("create test symlink: %v", err)
	}
	archiveRoot := filepath.Join(t.TempDir(), "archives")
	cleaner := &sandboxes.WorkspaceCleaner{
		Store: store, Locks: sandboxes.NewLifecycleLocks(), ArchiveRoot: archiveRoot,
		SandboxRoot: workspaceCleanupSandboxRoot(sandboxDir), Now: func() time.Time { return now },
	}

	result, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 1 || result.Failed != 0 {
		t.Fatalf("cleanup result = %#v", result)
	}
	archives, globErr := filepath.Glob(filepath.Join(archiveRoot, sandbox.Summary.ID, "*.tar.zst"))
	if globErr != nil || len(archives) != 1 {
		t.Fatalf("internal symlink archives = %v, error %v", archives, globErr)
	}
}

type archiveCompletionFailingStore struct {
	*sandboxstore.Store
	failures int
}

func (s *archiveCompletionFailingStore) UpdateSandbox(ctx context.Context, sandbox *domain.Sandbox) error {
	if sandbox.Archive != nil && sandbox.Archive.State == domain.SandboxArchiveStateArchived && s.failures > 0 {
		s.failures--
		return errors.New("injected archive metadata failure")
	}
	return s.Store.UpdateSandbox(ctx, sandbox)
}
