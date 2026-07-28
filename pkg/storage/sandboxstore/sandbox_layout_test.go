package sandboxstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sandboxes"
)

func TestCreateSandboxUsesLocalDateWithoutChangingUTCMetadata(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sandboxes")
	store, err := NewWithConfig(&appconfig.Config{
		SandboxRoot:          root,
		RuntimeDriver:        driverpkg.RuntimeDriverDocker,
		DockerDefaultImage:   "guest:latest",
		DefaultImage:         "guest:latest",
		JupyterProxyBasePath: "/jupyter",
	})
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	localZone := time.FixedZone("test-local", 8*60*60)
	createdAt := time.Date(2026, 1, 2, 0, 30, 0, 123, localZone)
	store.now = func() time.Time { return createdAt }
	sandbox, err := store.CreateSandbox(context.Background(), "local date", "", driverpkg.RuntimeDriverDocker, "", "", "", nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	wantDir := filepath.Join(root, "2026", "01", "02", sandboxDirName(sandbox.Summary.ID))
	if got := store.SandboxDir(sandbox.Summary.ID); got != wantDir {
		t.Fatalf("SandboxDir = %q, want %q", got, wantDir)
	}
	if !sandbox.Summary.CreatedAt.Equal(createdAt.UTC()) || sandbox.Summary.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt = %v, want UTC instant %v", sandbox.Summary.CreatedAt, createdAt.UTC())
	}
	record, err := sandboxes.ReadOwnershipRecord(root, sandbox.Summary.ID)
	if err != nil {
		t.Fatalf("ReadOwnershipRecord: %v", err)
	}
	if record.SandboxPath != wantDir {
		t.Fatalf("ownership sandbox path = %q, want %q", record.SandboxPath, wantDir)
	}

	store.now = func() time.Time { return createdAt.Add(48 * time.Hour) }
	if err := store.UpdateSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("UpdateSandbox: %v", err)
	}
	if got := store.SandboxDir(sandbox.Summary.ID); got != wantDir {
		t.Fatalf("SandboxDir after update = %q, want stable path %q", got, wantDir)
	}
}

func TestStoreDiscoversAndManagesMixedSandboxLayouts(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "sandboxes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create sandbox root: %v", err)
	}

	legacyDir := filepath.Join(root, "legacy-sandbox")
	nestedDir := filepath.Join(root, "2026", "07", "22", "nested-sandbox")
	writeLayoutSandbox(t, legacyDir, "legacy-sandbox", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	writeLayoutSandbox(t, nestedDir, "nested-sandbox", time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC))

	config := &appconfig.Config{
		DataRoot:           dataRoot,
		DbAddr:             filepath.Join(dataRoot, "data.db"),
		SandboxRoot:        root,
		RuntimeDriver:      driverpkg.RuntimeDriverDocker,
		DockerDefaultImage: "guest:latest",
		DefaultImage:       "guest:latest",
	}
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}

	assertLayoutSandboxPath(t, store, "legacy-sandbox", legacyDir)
	assertLayoutSandboxPath(t, store, "nested-sandbox", nestedDir)
	result, err := store.ListSandboxes(context.Background(), domain.SandboxListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if got := ids(result.Sandboxes); len(got) != 2 || got[0] != "nested-sandbox" || got[1] != "legacy-sandbox" {
		t.Fatalf("listed sandbox IDs = %v, want mixed layouts", got)
	}
	for id, wantPath := range map[string]string{"legacy-sandbox": legacyDir, "nested-sandbox": nestedDir} {
		record, err := sandboxes.ReadOwnershipRecord(root, id)
		if err != nil {
			t.Fatalf("ReadOwnershipRecord(%s): %v", id, err)
		}
		if record.SandboxPath != wantPath {
			t.Fatalf("ownership path for %s = %q, want %q", id, record.SandboxPath, wantPath)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	assertLayoutSandboxPath(t, reopened, "legacy-sandbox", legacyDir)
	assertLayoutSandboxPath(t, reopened, "nested-sandbox", nestedDir)

	fallback := FromConfig(config)
	assertLayoutSandboxPath(t, fallback, "nested-sandbox", nestedDir)
	fallbackResult, err := fallback.ListSandboxes(context.Background(), domain.SandboxListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("filesystem ListSandboxes: %v", err)
	}
	if got := ids(fallbackResult.Sandboxes); len(got) != 2 || got[0] != "nested-sandbox" || got[1] != "legacy-sandbox" {
		t.Fatalf("filesystem sandbox IDs = %v, want mixed layouts", got)
	}

	for _, id := range []string{"legacy-sandbox", "nested-sandbox"} {
		if err := reopened.RemoveSandbox(context.Background(), id); err != nil {
			t.Fatalf("RemoveSandbox(%s): %v", id, err)
		}
	}
	for _, path := range []string{legacyDir, nestedDir} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed sandbox path %s stat error = %v, want not exist", path, err)
		}
	}
}

func TestSandboxLayoutRejectsDuplicateIDsAcrossLayouts(t *testing.T) {
	root := t.TempDir()
	writeLayoutSandbox(t, filepath.Join(root, "duplicate"), "duplicate", time.Now().UTC())
	writeLayoutSandbox(t, filepath.Join(root, "2026", "07", "22", "duplicate"), "duplicate", time.Now().UTC())

	_, err := NewWithConfig(&appconfig.Config{SandboxRoot: root, RuntimeDriver: driverpkg.RuntimeDriverDocker})
	if err == nil || !strings.Contains(err.Error(), "exists in multiple directories") {
		t.Fatalf("NewWithConfig duplicate error = %v", err)
	}
}

func TestSandboxLayoutIgnoresInvalidCalendarDirectories(t *testing.T) {
	root := t.TempDir()
	writeLayoutSandbox(t, filepath.Join(root, "2026", "02", "30", "invalid-date"), "invalid-date", time.Now().UTC())
	store := FromConfig(&appconfig.Config{SandboxRoot: root, RuntimeDriver: driverpkg.RuntimeDriverDocker})
	result, err := store.ListSandboxes(context.Background(), domain.SandboxListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(result.Sandboxes) != 0 {
		t.Fatalf("invalid date layout listed sandboxes = %v", ids(result.Sandboxes))
	}
}

func TestSandboxLayoutSkipsUnreadableDateSubtrees(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "legacy")
	validDir := filepath.Join(root, "2026", "07", "22", "valid")
	writeLayoutSandbox(t, legacyDir, "legacy", time.Now().UTC())
	writeLayoutSandbox(t, validDir, "valid", time.Now().UTC())

	unreadable := map[string]struct{}{
		filepath.Join(root, "2023"):             {},
		filepath.Join(root, "2024", "01"):       {},
		filepath.Join(root, "2025", "01", "01"): {},
	}
	for path := range unreadable {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create unreadable fixture %s: %v", path, err)
		}
	}

	layout := newSandboxLayout(root)
	readDir := layout.readDir
	layout.readDir = func(path string) ([]os.DirEntry, error) {
		if _, ok := unreadable[path]; ok {
			return nil, os.ErrPermission
		}
		return readDir(path)
	}
	locations, err := layout.discover()
	if err != nil {
		t.Fatalf("discover with unreadable date subtrees: %v", err)
	}
	got := make(map[string]string, len(locations))
	for _, location := range locations {
		got[location.id] = location.path
	}
	want := map[string]string{"legacy": legacyDir, "valid": validDir}
	if len(got) != len(want) {
		t.Fatalf("discovered locations = %v, want %v", got, want)
	}
	for id, path := range want {
		if got[id] != path {
			t.Fatalf("discovered path for %s = %q, want %q", id, got[id], path)
		}
	}
}

func TestSandboxLayoutStillRejectsUnreadableRoot(t *testing.T) {
	layout := newSandboxLayout(t.TempDir())
	layout.readDir = func(string) ([]os.DirEntry, error) { return nil, os.ErrPermission }
	_, err := layout.discover()
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("discover unreadable root error = %v, want permission error", err)
	}
}

func TestSandboxLayoutDiscoveryPreservesPendingAllocation(t *testing.T) {
	root := t.TempDir()
	layout := newSandboxLayout(root)
	createdAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.Local)
	want, err := layout.allocate("new-sandbox", createdAt)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if _, err := layout.discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	if got := layout.path("new-sandbox"); got != want {
		t.Fatalf("allocated path after discovery = %q, want %q", got, want)
	}
}

func writeLayoutSandbox(t *testing.T, sandboxDir, id string, createdAt time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(sandboxDir, "workspace"), 0o755); err != nil {
		t.Fatalf("create sandbox directory: %v", err)
	}
	sandbox := Sandbox{Summary: SandboxSummary{
		ID:            id,
		Driver:        driverpkg.RuntimeDriverDocker,
		VMStatus:      VMStatusStopped,
		RuntimeRef:    "agent-compose-" + id,
		WorkspacePath: filepath.Join(sandboxDir, "workspace"),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}}
	data, err := json.Marshal(sandbox)
	if err != nil {
		t.Fatalf("marshal sandbox metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sandboxDir, "metadata.json"), data, 0o600); err != nil {
		t.Fatalf("write sandbox metadata: %v", err)
	}
}

func assertLayoutSandboxPath(t *testing.T, store *Store, id, wantDir string) {
	t.Helper()
	sandbox, err := store.GetSandbox(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSandbox(%s): %v", id, err)
	}
	if got := store.SandboxDir(id); got != wantDir {
		t.Fatalf("SandboxDir(%s) = %q, want %q", id, got, wantDir)
	}
	if sandbox.Summary.WorkspacePath != filepath.Join(wantDir, "workspace") {
		t.Fatalf("WorkspacePath(%s) = %q, want under %q", id, sandbox.Summary.WorkspacePath, wantDir)
	}
}
