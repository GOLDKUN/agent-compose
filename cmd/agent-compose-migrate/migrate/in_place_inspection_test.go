package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSkipInPlacePayloadSubtree(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "sessions/sandbox-1/workspace", want: false},
		{path: "sessions/sandbox-1/workspace/node_modules", want: true},
		{path: "sandboxes/2026/07/27/sandbox-1/workspace", want: false},
		{path: "sandboxes/2026/07/27/sandbox-1/workspace/.cache", want: true},
		{path: "workspaces/workspace-1/content", want: false},
		{path: "workspaces/workspace-1/content/vendor", want: true},
		{path: "sessions/sandbox-1/state", want: true},
		{path: "sessions/sandbox-1/logs", want: true},
		{path: "sessions/sandbox-1/home", want: true},
		{path: "sessions/sandbox-1/runtime", want: true},
		{path: "sessions/sandbox-1/vm", want: false},
		{path: "sessions/sandbox-1/vm/cache", want: true},
		{path: "sessions/2026", want: false},
		{path: "sessions/2026/07", want: false},
		{path: "sessions/2026/07/27", want: false},
		{path: "sessions/2026/07/27/sandbox-1/state", want: true},
		{path: "schedulers/scheduler-1/runs", want: false},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			if got := skipInPlacePayloadSubtree(test.path); got != test.want {
				t.Fatalf("skipInPlacePayloadSubtree(%q) = %t, want %t", test.path, got, test.want)
			}
		})
	}
}

func TestInspectInPlaceAuthoritativeFilesSkipsSandboxPayloads(t *testing.T) {
	root := t.TempDir()
	sandboxRoot := filepath.Join(root, "sessions", "sandbox-1")
	writeMigrationJSON(t, filepath.Join(sandboxRoot, "metadata.json"), map[string]any{
		"summary": map[string]any{"id": "sandbox-1", "vm_status": "STOPPED"},
	})
	for _, payload := range []string{"state", "logs", "home", "runtime"} {
		path := filepath.Join(sandboxRoot, payload, "nested", "payload")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not migration control data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	checked, err := inspectInPlaceAuthoritativeFiles(context.Background(), root, root, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 1 {
		t.Fatalf("checked files = %d, want only metadata.json", checked)
	}
}
