package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samber/do/v2"
)

func TestNewConfigRejectsSandboxArchiveRootInsideSandboxRoot(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		symlink bool
	}{
		{name: "same directory", path: "."},
		{name: "nested directory", path: "archives"},
		{name: "symlink into sandbox", path: "archives", symlink: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			sandboxRoot := filepath.Join(root, "sandboxes")
			t.Setenv("DATA_ROOT", filepath.Join(root, "data"))
			t.Setenv("SANDBOX_ROOT", sandboxRoot)
			archiveRoot := filepath.Join(sandboxRoot, test.path)
			if test.symlink {
				if err := os.MkdirAll(archiveRoot, 0o700); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(root, "archive-link")
				if err := os.Symlink(archiveRoot, link); err != nil {
					t.Skipf("create archive root symlink: %v", err)
				}
				archiveRoot = link
			}
			t.Setenv("SANDBOX_ARCHIVE_ROOT", archiveRoot)
			di := do.New()
			do.ProvideValue(di, slog.Default())
			_, err := NewConfig(di)
			if err == nil || !strings.Contains(err.Error(), "SANDBOX_ARCHIVE_ROOT") {
				t.Fatalf("NewConfig error = %v, want unsafe archive root rejection", err)
			}
		})
	}
}

func TestNewConfigAcceptsSandboxArchiveRootOutsideSandboxRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATA_ROOT", filepath.Join(root, "data"))
	t.Setenv("SANDBOX_ROOT", filepath.Join(root, "sandboxes"))
	t.Setenv("SANDBOX_ARCHIVE_ROOT", filepath.Join(root, "archives"))
	di := do.New()
	do.ProvideValue(di, slog.Default())
	if _, err := NewConfig(di); err != nil {
		t.Fatalf("NewConfig returned error for external archive root: %v", err)
	}
}
