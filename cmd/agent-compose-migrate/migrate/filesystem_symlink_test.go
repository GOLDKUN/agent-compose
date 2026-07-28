package migrate

import (
	"path/filepath"
	"testing"
)

func TestMigratedSymlinkTargetPreservesOwnershipAndLinkStyle(t *testing.T) {
	oldRoot := filepath.Join(string(filepath.Separator), "old-root")
	newRoot := filepath.Join(string(filepath.Separator), "new-root")
	runtimeRoot := filepath.Join(string(filepath.Separator), "data")
	schedulerIDs := map[string]string{"legacy-loader": "scheduler-1"}
	workspaceLink := filepath.Join(oldRoot, "workspaces", "workspace-1", "content", "link")

	tests := []struct {
		name       string
		sourcePath string
		mappedRel  string
		target     string
		targetRoot string
		want       string
		wantInside bool
	}{
		{
			name: "relative target within unchanged subtree", sourcePath: workspaceLink,
			mappedRel: filepath.Join("workspaces", "workspace-1", "content", "link"), target: "sibling", targetRoot: newRoot,
			want: "sibling", wantInside: true,
		},
		{
			name: "relative target crosses renamed sandbox root", sourcePath: workspaceLink,
			mappedRel: filepath.Join("workspaces", "workspace-1", "content", "link"), target: filepath.Join("..", "..", "..", "sessions", "sandbox-1", "workspace"), targetRoot: newRoot,
			want: filepath.Join("..", "..", "..", "sandboxes", "sandbox-1", "workspace"), wantInside: true,
		},
		{
			name: "relative target crosses remapped scheduler identity", sourcePath: workspaceLink,
			mappedRel: filepath.Join("workspaces", "workspace-1", "content", "link"), target: filepath.Join("..", "..", "..", "loaders", "legacy-loader", "runs", "run-1"), targetRoot: newRoot,
			want: filepath.Join("..", "..", "..", "schedulers", "scheduler-1", "runs", "run-1"), wantInside: true,
		},
		{
			name: "absolute source-owned target", sourcePath: workspaceLink,
			mappedRel: filepath.Join("workspaces", "workspace-1", "content", "link"), target: filepath.Join(oldRoot, "sessions", "sandbox-1", "state"), targetRoot: newRoot,
			want: filepath.Join(newRoot, "sandboxes", "sandbox-1", "state"), wantInside: true,
		},
		{
			name: "absolute runtime-owned target", sourcePath: workspaceLink,
			mappedRel: filepath.Join("workspaces", "workspace-1", "content", "link"), target: filepath.Join(runtimeRoot, "loaders", "legacy-loader", "runs", "run-1"), targetRoot: runtimeRoot,
			want: filepath.Join(runtimeRoot, "schedulers", "scheduler-1", "runs", "run-1"), wantInside: true,
		},
		{
			name: "absolute external target", sourcePath: workspaceLink,
			mappedRel: filepath.Join("workspaces", "workspace-1", "content", "link"), target: filepath.Join(string(filepath.Separator), "external", "shared"), targetRoot: newRoot,
			want: filepath.Join(string(filepath.Separator), "external", "shared"), wantInside: false,
		},
		{
			name: "relative external target", sourcePath: workspaceLink,
			mappedRel: filepath.Join("workspaces", "workspace-1", "content", "link"), target: filepath.Join("..", "..", "..", "..", "external"), targetRoot: newRoot,
			want: filepath.Join("..", "..", "..", "..", "external"), wantInside: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, inside, err := migratedSymlinkTarget(test.sourcePath, test.mappedRel, test.target, oldRoot, test.targetRoot, schedulerIDs)
			if err != nil || got != test.want || inside != test.wantInside {
				t.Fatalf("migrated target=%q inside=%v err=%v, want %q/%v", got, inside, err, test.want, test.wantInside)
			}
		})
	}
}
