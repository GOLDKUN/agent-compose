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
		"summary":         map[string]any{"id": sandboxID, "workspace_path": "/data/sessions/" + sandboxID + "/workspace"},
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

	files, _, err := copyAuthoritativeFiles(source, target, runtimeRoot, nil)
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

func TestCopyAuthoritativeFilesRejectsUnmappedLifecyclePath(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	writeMigrationJSON(t, filepath.Join(source, "sessions", ".lifecycle", "sandbox-1.json"), map[string]any{
		"sandbox_id": "sandbox-1", "sandbox_path": "/external/sandbox-1",
	})
	if _, _, err := copyAuthoritativeFiles(source, target, "/data", nil); err == nil {
		t.Fatal("copy accepted an unmapped lifecycle sandbox path")
	}
}

func TestValidateStoppedLegacySandboxesRejectsRunningSandbox(t *testing.T) {
	source := t.TempDir()
	writeMigrationJSON(t, filepath.Join(source, "sessions", "sandbox-1", "metadata.json"), map[string]any{
		"summary": map[string]any{"id": "sandbox-1", "vm_status": "RUNNING"},
	})
	err := validateStoppedLegacySandboxes(source)
	if err == nil || !strings.Contains(err.Error(), "stop all sandboxes") {
		t.Fatalf("running sandbox validation error = %v", err)
	}
	writeMigrationJSON(t, filepath.Join(source, "sessions", "sandbox-1", "metadata.json"), map[string]any{
		"summary": map[string]any{"id": "sandbox-1", "vm_status": "STOPPED"},
	})
	if err := validateStoppedLegacySandboxes(source); err != nil {
		t.Fatalf("stopped sandbox was rejected: %v", err)
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
