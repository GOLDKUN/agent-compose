package sandboxes_test

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sandboxes"
	"agent-compose/pkg/storage/sandboxstore"
	"agent-compose/pkg/workspaces"
)

func TestWorkspaceCleanerReclaimsStoppedSandboxAndBlocksProvisioning(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	workspaceFile := filepath.Join(sandbox.Summary.WorkspacePath, "result.txt")
	if err := os.WriteFile(workspaceFile, []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleaner := &sandboxes.WorkspaceCleaner{Store: store, Locks: sandboxes.NewLifecycleLocks(), Now: func() time.Time { return now }}
	result, err := cleaner.Clean(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 1 || result.Removed != 1 || result.Failed != 0 {
		t.Fatalf("cleanup result = %#v", result)
	}
	if _, err := os.Stat(sandbox.Summary.WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists: %v", err)
	}
	reclaimed, err := store.GetSandbox(ctx, sandbox.Summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.WorkspaceReclamation == nil || reclaimed.WorkspaceReclamation.State != domain.SandboxWorkspaceReclamationStateReclaimed || reclaimed.WorkspaceReclamation.CompletedAt.IsZero() {
		t.Fatalf("workspace reclamation = %#v", reclaimed.WorkspaceReclamation)
	}
	provisioner := workspaces.NewProvisionerWithMaterializer(store, noopWorkspaceMaterializer{})
	if err := provisioner.Ensure(ctx, reclaimed); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("Ensure error = %v, want failed precondition", err)
	}
	if err := store.RemoveSandbox(ctx, sandbox.Summary.ID); err != nil {
		t.Fatalf("RemoveSandbox after reclamation: %v", err)
	}
}

func TestWorkspaceCleanerArchivesAuditDataAndArchiveSurvivesSandboxRemoval(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	sandboxDir := store.SandboxDir(sandbox.Summary.ID)
	markers := map[string]string{
		"state/archive-state.txt": "state",
		"home/archive-home.txt":   "home",
		"logs/archive-log.txt":    "logs",
		"context/context.txt":     "context",
		"runtime/runtime.txt":     "runtime",
	}
	for name, contents := range markers {
		if err := os.WriteFile(filepath.Join(sandboxDir, filepath.FromSlash(name)), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sandbox.Summary.WorkspacePath, "excluded.txt"), []byte("workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	archiveRoot := filepath.Join(t.TempDir(), "archives")
	sandboxRoot := workspaceCleanupSandboxRoot(sandboxDir)
	locks := sandboxes.NewLifecycleLocks()
	removal := &sandboxes.RemovalCoordinator{SandboxRoot: sandboxRoot, Store: store, Runtime: workspaceCleanupTestRuntime{}, Locks: locks}
	cleaner := &sandboxes.WorkspaceCleaner{
		Store: store, Locks: locks, ArchiveRoot: archiveRoot, SandboxRoot: sandboxRoot, Removal: removal,
		Now: func() time.Time { return now },
	}
	result, err := cleaner.Clean(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 1 || result.Removed != 1 || result.Failed != 0 {
		t.Fatalf("cleanup result = %#v", result)
	}
	archives, err := filepath.Glob(filepath.Join(archiveRoot, sandbox.Summary.ID, "*.tar.zst"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archives = %v, error %v", archives, err)
	}
	archivePath := archives[0]
	entries := readArchiveEntries(t, archivePath)
	for name := range markers {
		archiveName := "sandbox/" + name
		if _, ok := entries[archiveName]; !ok {
			t.Fatalf("archive missing %q; entries=%v", name, entries)
		}
	}
	if _, ok := entries["sandbox/workspace/excluded.txt"]; ok {
		t.Fatal("workspace was included in audit archive")
	}
	for _, name := range []string{"sandbox/metadata.json", "sandbox/vm/runtime.json", "sandbox/proxy/jupyter.json", ".lifecycle/ownership.json"} {
		if _, ok := entries[name]; !ok {
			t.Fatalf("complete archive missing %q", name)
		}
	}
	if _, err := os.Stat(sandboxDir); !os.IsNotExist(err) {
		t.Fatalf("original sandbox directory remains: %v", err)
	}
	ownershipPath, err := sandboxes.OwnershipRecordPath(sandboxRoot, sandbox.Summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ownershipPath); !os.IsNotExist(err) {
		t.Fatalf("original ownership record remains: %v", err)
	}
	if _, err := store.GetSandbox(ctx, sandbox.Summary.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sandbox remains in listing store: %v", err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive did not survive sandbox removal: %v", err)
	}
	assertArchiveManifestMatches(t, archivePath)
}

func TestWorkspaceCleanerArchiveFailureDoesNotBlockWorkspaceRemoval(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	archiveRoot := filepath.Join(t.TempDir(), "archive-file")
	if err := os.WriteFile(archiveRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleaner := &sandboxes.WorkspaceCleaner{
		Store: store, Locks: sandboxes.NewLifecycleLocks(), ArchiveRoot: archiveRoot,
		Now: func() time.Time { return now },
	}
	result, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err == nil || result.Removed != 1 || result.Failed != 1 {
		t.Fatalf("cleanup result/error = %#v/%v", result, err)
	}
	if _, err := os.Stat(sandbox.Summary.WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("workspace remains after independent archive failure: %v", err)
	}
	loaded, loadErr := store.GetSandbox(context.Background(), sandbox.Summary.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.Archive == nil || loaded.Archive.State != domain.SandboxArchiveStateArchiving || loaded.Archive.LastError == "" {
		t.Fatalf("archive failure state = %#v", loaded.Archive)
	}
	if _, err := os.Stat(filepath.Join(store.SandboxDir(sandbox.Summary.ID), "state")); err != nil {
		t.Fatalf("archive failure removed hot state: %v", err)
	}
}

func TestWorkspaceCleanerRejectsArchiveRootInsideSandboxTree(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	sandboxRoot := workspaceCleanupSandboxRoot(store.SandboxDir(sandbox.Summary.ID))
	cleaner := &sandboxes.WorkspaceCleaner{
		Store: store, Locks: sandboxes.NewLifecycleLocks(), SandboxRoot: sandboxRoot,
		ArchiveRoot: filepath.Join(sandboxRoot, ".archives"), Now: func() time.Time { return now },
	}
	result, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err == nil || result.Failed != 1 {
		t.Fatalf("cleanup result/error = %#v/%v", result, err)
	}
	if _, err := os.Stat(sandbox.Summary.WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("workspace remains after archive validation failure: %v", err)
	}
	if _, err := os.Stat(store.SandboxDir(sandbox.Summary.ID)); err != nil {
		t.Fatalf("archive validation failure removed remaining originals: %v", err)
	}
}

func TestWorkspaceCleanerRejectsSymlinkSandboxArchiveDirectory(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	sandboxDir := store.SandboxDir(sandbox.Summary.ID)
	sandboxRoot := workspaceCleanupSandboxRoot(sandboxDir)
	archiveRoot := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(archiveRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sandboxDir, filepath.Join(archiveRoot, sandbox.Summary.ID)); err != nil {
		t.Fatal(err)
	}
	cleaner := &sandboxes.WorkspaceCleaner{
		Store: store, Locks: sandboxes.NewLifecycleLocks(), SandboxRoot: sandboxRoot,
		ArchiveRoot: archiveRoot, Now: func() time.Time { return now },
	}
	result, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err == nil || result.Failed != 1 {
		t.Fatalf("cleanup result/error = %#v/%v", result, err)
	}
	if _, err := os.Stat(sandbox.Summary.WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("workspace remains after unsafe archive rejection: %v", err)
	}
	archives, globErr := filepath.Glob(filepath.Join(sandboxDir, "*.tar.zst"))
	if globErr != nil || len(archives) != 0 {
		t.Fatalf("archive escaped into sandbox directory: %v, error %v", archives, globErr)
	}
	if _, err := os.Stat(filepath.Join(sandboxDir, "metadata.json")); err != nil {
		t.Fatalf("unsafe archive path removed remaining originals: %v", err)
	}
}

func TestWorkspaceCleanerRetriesFormalRemovalAfterArchive(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	sandboxDir := store.SandboxDir(sandbox.Summary.ID)
	removal := &workspaceCleanupTestRemoval{store: store, failures: 1}
	cleaner := &sandboxes.WorkspaceCleaner{
		Store: store, Locks: sandboxes.NewLifecycleLocks(), ArchiveRoot: filepath.Join(t.TempDir(), "archives"), Removal: removal,
		Now: func() time.Time { return now },
	}

	first, err := cleaner.Clean(ctx, now.Add(-24*time.Hour))
	if err == nil || first.Failed != 1 {
		t.Fatalf("first cleanup result/error = %#v/%v", first, err)
	}
	if _, err := os.Stat(sandbox.Summary.WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("workspace remains after archive: %v", err)
	}
	if _, err := os.Stat(sandboxDir); err != nil {
		t.Fatalf("formal removal failure unexpectedly removed sandbox: %v", err)
	}

	second, err := cleaner.Clean(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.Matched != 1 || second.Removed != 1 || second.Failed != 0 {
		t.Fatalf("second cleanup result = %#v", second)
	}
	if removal.calls != 2 {
		t.Fatalf("formal removal calls = %d, want 2", removal.calls)
	}
	if _, err := os.Stat(sandboxDir); !os.IsNotExist(err) {
		t.Fatalf("original sandbox directory remains after retry: %v", err)
	}
}

func TestWorkspaceCleanerRejectsSymlinkWorkspace(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	external := t.TempDir()
	if err := os.RemoveAll(sandbox.Summary.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, sandbox.Summary.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	cleaner := &sandboxes.WorkspaceCleaner{Store: store, Locks: sandboxes.NewLifecycleLocks(), Now: func() time.Time { return now }}
	result, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err == nil || result.Failed != 1 {
		t.Fatalf("result/error = %#v/%v", result, err)
	}
	loaded, loadErr := store.GetSandbox(context.Background(), sandbox.Summary.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.WorkspaceReclamation != nil || domain.SandboxWorkspaceUnavailable(loaded) {
		t.Fatalf("unsafe path committed reclamation intent: %#v", loaded.WorkspaceReclamation)
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatalf("external target was affected: %v", err)
	}
}

func TestWorkspaceCleanerRequiresRecordedStop(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, time.Time{})
	cleaner := &sandboxes.WorkspaceCleaner{Store: store, Locks: sandboxes.NewLifecycleLocks(), Now: func() time.Time { return now }}
	result, err := cleaner.Clean(context.Background(), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 0 || result.Removed != 0 {
		t.Fatalf("cleanup without recorded stop = %#v", result)
	}
	if _, err := os.Stat(sandbox.Summary.WorkspacePath); err != nil {
		t.Fatalf("workspace without recorded stop was removed: %v", err)
	}
	loaded, err := store.GetSandbox(context.Background(), sandbox.Summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WorkspaceReclamation != nil {
		t.Fatalf("cleanup without recorded stop persisted intent: %#v", loaded.WorkspaceReclamation)
	}
}

func TestWorkspaceCleanerDoesNotReclaimFailedSandboxWithStaleStop(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	sandbox.Summary.VMStatus = domain.VMStatusFailed
	if err := store.UpdateSandbox(context.Background(), sandbox); err != nil {
		t.Fatal(err)
	}

	cleaner := &sandboxes.WorkspaceCleaner{Store: store, Locks: sandboxes.NewLifecycleLocks(), Now: func() time.Time { return now }}
	result, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 0 || result.Removed != 0 {
		t.Fatalf("cleanup failed sandbox with stale stop = %#v", result)
	}
	if _, err := os.Stat(sandbox.Summary.WorkspacePath); err != nil {
		t.Fatalf("failed sandbox workspace was removed: %v", err)
	}
	loaded, err := store.GetSandbox(context.Background(), sandbox.Summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WorkspaceReclamation != nil {
		t.Fatalf("failed sandbox persisted reclamation intent: %#v", loaded.WorkspaceReclamation)
	}
}

func TestWorkspaceCleanerRequiresStopAfterLatestStart(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	vmState, err := store.GetVMState(sandbox.Summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	vmState.StartedAt = now.Add(-time.Hour)
	if err := store.SaveVMState(sandbox.Summary.ID, vmState); err != nil {
		t.Fatal(err)
	}

	cleaner := &sandboxes.WorkspaceCleaner{Store: store, Locks: sandboxes.NewLifecycleLocks(), Now: func() time.Time { return now }}
	result, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 0 || result.Removed != 0 {
		t.Fatalf("cleanup stale stop before latest start = %#v", result)
	}
	if _, err := os.Stat(sandbox.Summary.WorkspacePath); err != nil {
		t.Fatalf("workspace with stale stop marker was removed: %v", err)
	}
}

func TestWorkspaceCleanerRequiresStopAfterLatestStartAttempt(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store, sandbox := newWorkspaceCleanupSandbox(t, now.Add(-48*time.Hour))
	vmState, err := store.GetVMState(sandbox.Summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	vmState.StartAttemptedAt = now.Add(-time.Hour)
	if err := store.SaveVMState(sandbox.Summary.ID, vmState); err != nil {
		t.Fatal(err)
	}

	cleaner := &sandboxes.WorkspaceCleaner{Store: store, Locks: sandboxes.NewLifecycleLocks(), Now: func() time.Time { return now }}
	result, err := cleaner.Clean(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 0 || result.Removed != 0 {
		t.Fatalf("cleanup stop before latest start attempt = %#v", result)
	}
	if _, err := os.Stat(sandbox.Summary.WorkspacePath); err != nil {
		t.Fatalf("workspace with newer start attempt was removed: %v", err)
	}
}

func newWorkspaceCleanupSandbox(t *testing.T, stoppedAt time.Time) (*sandboxstore.Store, *domain.Sandbox) {
	t.Helper()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot: root, SandboxRoot: filepath.Join(root, "sandboxes"), RuntimeDriver: "docker",
		DefaultImage: "guest:latest", DockerDefaultImage: "guest:latest", JupyterProxyBasePath: "/jupyter",
	}
	store, err := sandboxstore.NewWithConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	sandbox, err := store.CreateSandbox(context.Background(), "cleanup", "", "docker", "guest:latest", "", "test", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sandbox.Summary.VMStatus = domain.VMStatusStopped
	if err := store.UpdateSandbox(context.Background(), sandbox); err != nil {
		t.Fatal(err)
	}
	vmState, err := store.GetVMState(sandbox.Summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	vmState.StoppedAt = stoppedAt
	if err := store.SaveVMState(sandbox.Summary.ID, vmState); err != nil {
		t.Fatal(err)
	}
	return store, sandbox
}

func workspaceCleanupSandboxRoot(sandboxDir string) string {
	return filepath.Clean(filepath.Join(sandboxDir, "..", "..", "..", ".."))
}

type noopWorkspaceMaterializer struct{}

func (noopWorkspaceMaterializer) Materialize(context.Context, *domain.Sandbox) error { return nil }

type workspaceCleanupTestRuntime struct{}

func (workspaceCleanupTestRuntime) StopSandboxVM(context.Context, *domain.Sandbox) error { return nil }
func (workspaceCleanupTestRuntime) RemoveSandboxVM(context.Context, *domain.Sandbox) error {
	return nil
}

type workspaceCleanupTestRemoval struct {
	store    *sandboxstore.Store
	calls    int
	failures int
}

func (r *workspaceCleanupTestRemoval) Remove(ctx context.Context, sandboxID string, _ bool) (sandboxes.RemovalResult, error) {
	r.calls++
	sandbox, err := r.store.GetSandbox(ctx, sandboxID)
	if err != nil {
		return sandboxes.RemovalResult{}, err
	}
	if r.failures > 0 {
		r.failures--
		sandbox.Summary.VMStatus = domain.VMStatusDeleting
		if err := r.store.UpdateSandbox(ctx, sandbox); err != nil {
			return sandboxes.RemovalResult{}, err
		}
		return sandboxes.RemovalResult{SandboxID: sandboxID}, errors.New("injected formal removal failure")
	}
	if err := r.store.RemoveSandbox(ctx, sandboxID); err != nil {
		return sandboxes.RemovalResult{}, err
	}
	return sandboxes.RemovalResult{SandboxID: sandboxID, Removed: true}, nil
}

func readArchiveEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	decompressor, err := zstd.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		decompressor.Close()
		_ = file.Close()
	})
	reader := tar.NewReader(decompressor)
	entries := map[string]string{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		entries[strings.TrimSuffix(header.Name, "/")] = string(data)
	}
	return entries
}

func assertArchiveManifestMatches(t *testing.T, archivePath string) {
	t.Helper()
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := strings.TrimSuffix(archivePath, ".tar.zst") + ".json"
	encoded, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SizeBytes int64  `json:"size_bytes"`
		SHA256    string `json:"sha256"`
	}
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if manifest.SizeBytes != int64(len(archive)) || manifest.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("archive manifest size/checksum = %d/%s", manifest.SizeBytes, manifest.SHA256)
	}
	for _, path := range []string{archivePath + ".tmp", manifestPath + ".tmp"} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("archive temporary file remains at %s: %v", path, err)
		}
	}
}
