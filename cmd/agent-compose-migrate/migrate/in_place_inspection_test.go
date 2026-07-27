package migrate

import "testing"

func TestSkipInPlaceWorkspaceSubtree(t *testing.T) {
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
		{path: "sessions/sandbox-1/state", want: false},
		{path: "schedulers/scheduler-1/runs", want: false},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			if got := skipInPlaceWorkspaceSubtree(test.path); got != test.want {
				t.Fatalf("skipInPlaceWorkspaceSubtree(%q) = %t, want %t", test.path, got, test.want)
			}
		})
	}
}
