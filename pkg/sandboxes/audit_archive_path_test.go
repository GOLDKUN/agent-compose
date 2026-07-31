package sandboxes

import (
	"path/filepath"
	"testing"
)

func TestSandboxArchiveRelativePathRejectsParentEscapes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sandbox")
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "inside", path: filepath.Join(root, "state", "result.json")},
		{name: "exact parent", path: filepath.Dir(root), wantErr: true},
		{name: "parent child", path: filepath.Join(filepath.Dir(root), "outside"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := sandboxArchiveRelativePath(root, test.path)
			if (err != nil) != test.wantErr {
				t.Fatalf("sandboxArchiveRelativePath(%q, %q) error = %v", root, test.path, err)
			}
		})
	}
}
