//go:build linux

package sandboxes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestIntegrationSandboxRetentionExcludesMountedExternalVolume(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	sandboxDir := store.SandboxDir(sandbox.Summary.ID)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "external-secret.txt"), []byte("external secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	mountPoint := filepath.Join(sandboxDir, "volumes", "mount-external")
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mount(source, mountPoint, "", syscall.MS_BIND, ""); err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("bind mount is not permitted: %v", err)
		}
		t.Fatalf("bind mount external volume: %v", err)
	}
	t.Cleanup(func() {
		if err := syscall.Unmount(mountPoint, syscall.MNT_DETACH); err != nil && !errors.Is(err, syscall.EINVAL) {
			t.Errorf("unmount external volume fixture: %v", err)
		}
	})

	archiveRoot := filepath.Join(t.TempDir(), "archives")
	cleaner := &sandboxes.SandboxRetentionCleaner{
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
	archives, err := filepath.Glob(filepath.Join(archiveRoot, sandbox.Summary.ID, "*.tar.zst"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archives = %v, error %v", archives, err)
	}
	entries := readArchiveEntries(t, archives[0])
	if _, exists := entries["sandbox/volumes/mount-external/external-secret.txt"]; exists {
		t.Fatal("mounted external volume data was included in audit archive")
	}
	if _, exists := entries["sandbox/volumes"]; exists {
		t.Fatal("mounted external volume bridge directory was included in audit archive")
	}
}
