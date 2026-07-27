package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyAuthoritativeFilesRewritesKnownSandboxJSONPaths(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	runtimeRoot := filepath.Join(string(filepath.Separator), "data")
	sandboxID := "sandbox-1"
	sandboxRoot := filepath.Join(source, "sessions", sandboxID)
	writeMigrationJSON(t, filepath.Join(source, "sessions", ".lifecycle", sandboxID+".json"), map[string]any{
		"version": 1, "sandbox_id": sandboxID, "sandbox_path": "/data/sessions/" + sandboxID,
		"owned_resources": []any{
			map[string]any{"kind": "runtime", "identity": "container-1", "path": "/do/not/rewrite"},
			map[string]any{"kind": "sandbox-directory", "path": "/data/sessions/" + sandboxID},
		},
		"note": "literal /old-mount/sessions/sandbox-1 must remain unchanged",
	})
	writeMigrationJSON(t, filepath.Join(sandboxRoot, "metadata.json"), map[string]any{
		"summary": map[string]any{
			"id": sandboxID, "workspace_path": "/data/sessions/" + sandboxID + "/workspace",
			"tags": []any{
				map[string]any{"name": "source", "value": "agent"},
				map[string]any{"name": "agent_id", "value": "legacy-agent"},
				map[string]any{"name": "custom", "value": "preserved"},
			},
		},
		"future_sequence": json.Number("9007199254740993"),
		"volume_mounts": []any{
			map[string]any{"host_path": "/data/sessions/" + sandboxID + "/state"},
			map[string]any{"host_path": "/external/project"},
			map[string]any{"host_path": "/external/volumes/sessions/backup"},
		},
	})
	writeMigrationJSON(t, filepath.Join(sandboxRoot, "vm", "mount-manifest.json"), map[string]any{
		"version": 1,
		"mounts": []any{
			map[string]any{"hostPath": "/data/sessions/" + sandboxID + "/workspace", "guestPath": "/workspace"},
			map[string]any{"hostPath": "/external/project", "guestPath": "/project"},
			map[string]any{"hostPath": "/external/volumes/sessions/backup", "guestPath": "/backup"},
		},
	})

	agentIDs := map[string]standaloneAgentIdentity{
		"legacy-agent": {NativeID: "native-agent", ProjectID: "legacy-project", AgentName: "worker"},
	}
	files, _, err := copyAuthoritativeFiles(source, target, runtimeRoot, nil, agentIDs)
	if err != nil {
		t.Fatal(err)
	}
	if files != 3 {
		t.Fatalf("copied files = %d, want 3", files)
	}

	lifecycle := readMigrationJSON(t, filepath.Join(target, "sandboxes", ".lifecycle", sandboxID+".json"))
	wantSandboxPath := filepath.Join(runtimeRoot, "sandboxes", sandboxID)
	if lifecycle["sandbox_path"] != wantSandboxPath || lifecycle["note"] != "literal /old-mount/sessions/sandbox-1 must remain unchanged" {
		t.Fatalf("migrated lifecycle = %#v", lifecycle)
	}
	resources := lifecycle["owned_resources"].([]any)
	if resources[0].(map[string]any)["path"] != "/do/not/rewrite" || resources[1].(map[string]any)["path"] != wantSandboxPath {
		t.Fatalf("migrated lifecycle resources = %#v", resources)
	}

	metadata := readMigrationJSON(t, filepath.Join(target, "sandboxes", sandboxID, "metadata.json"))
	if metadata["summary"].(map[string]any)["workspace_path"] != filepath.Join(wantSandboxPath, "workspace") {
		t.Fatalf("migrated metadata = %#v", metadata)
	}
	tagValues := migrationTagValues(metadata["summary"].(map[string]any)["tags"])
	if tagValues["agent_id"] != "native-agent" || tagValues["agent_name"] != "worker" || tagValues["agent"] != "worker" ||
		tagValues["project"] != "legacy-project" || tagValues["project_id"] != "legacy-project" || tagValues["custom"] != "preserved" {
		t.Fatalf("migrated metadata tags = %#v", tagValues)
	}
	metadataBytes, err := os.ReadFile(filepath.Join(target, "sandboxes", sandboxID, "metadata.json"))
	if err != nil || !strings.Contains(string(metadataBytes), "9007199254740993") {
		t.Fatalf("large unknown metadata number was not preserved: %s, err=%v", metadataBytes, err)
	}
	volumeMounts := metadata["volume_mounts"].([]any)
	if volumeMounts[0].(map[string]any)["host_path"] != filepath.Join(wantSandboxPath, "state") ||
		volumeMounts[1].(map[string]any)["host_path"] != "/external/project" ||
		volumeMounts[2].(map[string]any)["host_path"] != "/external/volumes/sessions/backup" {
		t.Fatalf("migrated metadata mounts = %#v", volumeMounts)
	}

	manifest := readMigrationJSON(t, filepath.Join(target, "sandboxes", sandboxID, "vm", "mount-manifest.json"))
	mounts := manifest["mounts"].([]any)
	if mounts[0].(map[string]any)["hostPath"] != filepath.Join(wantSandboxPath, "workspace") ||
		mounts[1].(map[string]any)["hostPath"] != "/external/project" ||
		mounts[2].(map[string]any)["hostPath"] != "/external/volumes/sessions/backup" {
		t.Fatalf("migrated manifest mounts = %#v", mounts)
	}
}

func migrationTagValues(value any) map[string]string {
	result := make(map[string]string)
	for _, item := range value.([]any) {
		tag := item.(map[string]any)
		result[tag["name"].(string)] = tag["value"].(string)
	}
	return result
}

func TestCopyAuthoritativeFilesPreservesWorkspaceSymlinkWithoutFollowingIt(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	target := filepath.Join(parent, "target")
	workspace := filepath.Join(source, "workspaces", "workspace-1", "content")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(parent, "external")
	if err := os.WriteFile(external, []byte("must not be copied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(workspace, "external-link")); err != nil {
		t.Fatal(err)
	}
	legacySandboxFile := filepath.Join(source, "sessions", "sandbox-1", "workspace", "shared.txt")
	if err := os.MkdirAll(filepath.Dir(legacySandboxFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacySandboxFile, []byte("shared"), 0o600); err != nil {
		t.Fatal(err)
	}
	relativeInternalTarget := filepath.Join("..", "..", "..", "sessions", "sandbox-1", "workspace", "shared.txt")
	if err := os.Symlink(relativeInternalTarget, filepath.Join(workspace, "relative-internal-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacySandboxFile, filepath.Join(workspace, "absolute-internal-link")); err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		files, _, err := copyAuthoritativeFiles(source, target, target, nil, nil)
		if err != nil || files != 4 {
			t.Fatalf("copy attempt %d files=%d err=%v", attempt, files, err)
		}
	}
	copied := filepath.Join(target, "workspaces", "workspace-1", "content", "external-link")
	info, err := os.Lstat(copied)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("copied workspace link info=%v err=%v", info, err)
	}
	if linkTarget, err := os.Readlink(copied); err != nil || linkTarget != external {
		t.Fatalf("copied workspace link target=%q err=%v", linkTarget, err)
	}
	if _, err := os.Stat(filepath.Join(target, filepath.Base(external))); !os.IsNotExist(err) {
		t.Fatalf("copy followed external symlink: %v", err)
	}
	relativeLink := filepath.Join(target, "workspaces", "workspace-1", "content", "relative-internal-link")
	wantRelativeTarget := filepath.Join("..", "..", "..", "sandboxes", "sandbox-1", "workspace", "shared.txt")
	if linkTarget, err := os.Readlink(relativeLink); err != nil || linkTarget != wantRelativeTarget {
		t.Fatalf("relative internal link target=%q err=%v, want %q", linkTarget, err, wantRelativeTarget)
	}
	absoluteLink := filepath.Join(target, "workspaces", "workspace-1", "content", "absolute-internal-link")
	wantAbsoluteTarget := filepath.Join(target, "sandboxes", "sandbox-1", "workspace", "shared.txt")
	if linkTarget, err := os.Readlink(absoluteLink); err != nil || linkTarget != wantAbsoluteTarget {
		t.Fatalf("absolute internal link target=%q err=%v, want %q", linkTarget, err, wantAbsoluteTarget)
	}
}

func TestCopyAuthoritativeFilesRejectsControlFileSymlink(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	metadataTarget := filepath.Join(source, "metadata-target.json")
	writeMigrationJSON(t, metadataTarget, map[string]any{"summary": map[string]any{"id": "sandbox-1", "vm_status": "STOPPED"}})
	metadataLink := filepath.Join(source, "sessions", "sandbox-1", "metadata.json")
	if err := os.MkdirAll(filepath.Dir(metadataLink), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(metadataTarget, metadataLink); err != nil {
		t.Fatal(err)
	}
	if _, _, err := copyAuthoritativeFiles(source, target, target, nil, nil); err == nil || !strings.Contains(err.Error(), "migration-controlled") {
		t.Fatalf("control symlink error=%v", err)
	}
}

func TestCopyAuthoritativeFilesRejectsUnmappedLifecyclePath(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	writeMigrationJSON(t, filepath.Join(source, "sessions", ".lifecycle", "sandbox-1.json"), map[string]any{
		"sandbox_id": "sandbox-1", "sandbox_path": "/external/sandbox-1",
	})
	if _, _, err := copyAuthoritativeFiles(source, target, "/data", nil, nil); err == nil {
		t.Fatal("copy accepted an unmapped lifecycle sandbox path")
	}
}

func TestValidateStoppedLegacySandboxesRejectsRunningSandbox(t *testing.T) {
	for _, rootName := range []string{"sessions", "sandboxes"} {
		t.Run(rootName, func(t *testing.T) {
			source := t.TempDir()
			metadataPath := filepath.Join(source, rootName, "sandbox-1", "metadata.json")
			writeMigrationJSON(t, metadataPath, map[string]any{
				"summary": map[string]any{"id": "sandbox-1", "vm_status": "RUNNING"},
			})
			err := validateStoppedLegacySandboxes(source)
			if err == nil || !strings.Contains(err.Error(), "stop all sandboxes") {
				t.Fatalf("running sandbox validation error = %v", err)
			}
			writeMigrationJSON(t, metadataPath, map[string]any{
				"summary": map[string]any{"id": "sandbox-1", "vm_status": "STOPPED"},
			})
			if err := validateStoppedLegacySandboxes(source); err != nil {
				t.Fatalf("stopped sandbox was rejected: %v", err)
			}
		})
	}
}

func TestValidateStoppedLegacySandboxesReportsAllRunningSandboxes(t *testing.T) {
	source := t.TempDir()
	for _, fixture := range []struct {
		rootName string
		path     string
		id       string
		status   string
	}{
		{rootName: "sessions", path: "2026/07/27/zeta", id: "sandbox-zeta", status: "RUNNING"},
		{rootName: "sessions", path: "2026/07/27/stopped", id: "sandbox-stopped", status: "STOPPED"},
		{rootName: "sandboxes", path: "alpha", id: "sandbox-alpha", status: "running"},
		{rootName: "sandboxes", path: "duplicate", id: "sandbox-zeta", status: "RUNNING"},
	} {
		writeMigrationJSON(t, filepath.Join(source, fixture.rootName, fixture.path, "metadata.json"), map[string]any{
			"summary": map[string]any{"id": fixture.id, "vm_status": fixture.status},
		})
	}

	err := validateStoppedLegacySandboxes(source)
	want := "sandboxes sandbox-alpha, sandbox-zeta are still running; stop all sandboxes by full ID with the old daemon before migration"
	if err == nil || err.Error() != want {
		t.Fatalf("running sandbox validation error = %v, want %q", err, want)
	}
}

func TestDryRunIgnoresNestedWorkspaceMetadataFiles(t *testing.T) {
	source := t.TempDir()
	writeMigrationJSON(t, filepath.Join(source, "sessions", "2026", "07", "24", "sandbox-1", "metadata.json"), map[string]any{
		"summary": map[string]any{"id": "sandbox-1", "vm_status": "STOPPED"},
	})
	nested := filepath.Join(
		source, "sessions", "2026", "07", "24", "sandbox-1", "workspace", ".cache", ".task_cache",
		"python", "lib", "python3.10", "site-packages", "coreapi-2.3.3.dist-info", "metadata.json",
	)
	if err := os.MkdirAll(filepath.Dir(nested), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte(`"Python package metadata"`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := validateStoppedLegacySandboxes(source); err != nil {
		t.Fatalf("nested workspace metadata failed sandbox state validation: %v", err)
	}
	if _, _, err := inspectAuthoritativeFiles(source, "/data", nil, nil); err != nil {
		t.Fatalf("nested workspace metadata failed dry-run file inspection: %v", err)
	}
}

func writeMigrationJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readMigrationJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
