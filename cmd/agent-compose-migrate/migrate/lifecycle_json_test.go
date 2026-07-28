package migrate

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteLifecyclePathsPreservesSandboxLayout(t *testing.T) {
	sourceRoot := filepath.Join(t.TempDir(), "source")
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	digest := strings.Repeat("a", 64)
	tests := []struct {
		name       string
		sandboxID  string
		storedPath string
		wantParts  []string
	}{
		{name: "legacy flat absolute", sandboxID: "sandbox-1", storedPath: filepath.Join(sourceRoot, "sessions", "sandbox-1"), wantParts: []string{"sandboxes", "sandbox-1"}},
		{name: "native flat absolute", sandboxID: "sandbox-1", storedPath: filepath.Join(sourceRoot, "sandboxes", "sandbox-1"), wantParts: []string{"sandboxes", "sandbox-1"}},
		{name: "native partitioned absolute", sandboxID: "sandbox-1", storedPath: filepath.Join(sourceRoot, "sandboxes", "2026", "07", "26", "sandbox-1"), wantParts: []string{"sandboxes", "2026", "07", "26", "sandbox-1"}},
		{name: "legacy partitioned relative", sandboxID: "sandbox-1", storedPath: filepath.Join("sessions", "2026", "07", "26", "sandbox-1"), wantParts: []string{"sandboxes", "2026", "07", "26", "sandbox-1"}},
		{name: "prefixed identity flat", sandboxID: "sha256:" + digest, storedPath: filepath.Join(sourceRoot, "sandboxes", digest), wantParts: []string{"sandboxes", digest}},
		{name: "prefixed identity partitioned", sandboxID: "sha256:" + digest, storedPath: filepath.Join(sourceRoot, "sandboxes", "2026", "07", "26", digest), wantParts: []string{"sandboxes", "2026", "07", "26", digest}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := map[string]any{
				"sandbox_id":   test.sandboxID,
				"sandbox_path": test.storedPath,
				"owned_resources": []any{
					map[string]any{"kind": "sandbox-directory", "path": test.storedPath},
					map[string]any{"kind": "runtime", "path": "/external/runtime"},
				},
			}
			if err := rewriteLifecyclePaths(document, test.sandboxID, sourceRoot, runtimeRoot, nil); err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(append([]string{runtimeRoot}, test.wantParts...)...)
			if document["sandbox_path"] != want {
				t.Fatalf("sandbox_path = %q, want %q", document["sandbox_path"], want)
			}
			resources := document["owned_resources"].([]any)
			if resources[0].(map[string]any)["path"] != want || resources[1].(map[string]any)["path"] != "/external/runtime" {
				t.Fatalf("owned_resources = %#v", resources)
			}
		})
	}
}

func TestRewriteLifecyclePathsRejectsInconsistentOwnership(t *testing.T) {
	sourceRoot := filepath.Join(t.TempDir(), "source")
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	sandboxID := "sandbox-1"
	validPath := filepath.Join(sourceRoot, "sandboxes", "2026", "07", "26", sandboxID)
	tests := []struct {
		name      string
		recordID  string
		document  map[string]any
		wantError string
	}{
		{
			name: "record identity mismatch", recordID: "different-record",
			document:  map[string]any{"sandbox_id": sandboxID, "sandbox_path": validPath},
			wantError: "does not match record",
		},
		{
			name: "external sandbox path", recordID: sandboxID,
			document:  map[string]any{"sandbox_id": sandboxID, "sandbox_path": filepath.Join(t.TempDir(), sandboxID)},
			wantError: "outside the legacy data root",
		},
		{
			name: "wrong application root", recordID: sandboxID,
			document:  map[string]any{"sandbox_id": sandboxID, "sandbox_path": filepath.Join(sourceRoot, "workspaces", sandboxID)},
			wantError: "outside sandbox root",
		},
		{
			name: "wrong sandbox basename", recordID: sandboxID,
			document:  map[string]any{"sandbox_id": sandboxID, "sandbox_path": filepath.Join(sourceRoot, "sandboxes", "sandbox-2")},
			wantError: "does not identify sandbox",
		},
		{
			name: "owned directory mismatch", recordID: sandboxID,
			document: map[string]any{
				"sandbox_id": sandboxID, "sandbox_path": validPath,
				"owned_resources": []any{map[string]any{
					"kind": "sandbox-directory", "path": filepath.Join(sourceRoot, "sandboxes", sandboxID),
				}},
			},
			wantError: "does not identify",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := rewriteLifecyclePaths(test.document, test.recordID, sourceRoot, runtimeRoot, nil)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}
