package schedulers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chaitin/agent-compose/pkg/identity"
)

func TestFSArtifactsInspectAndRemoveRunArtifacts(t *testing.T) {
	root := t.TempDir()
	schedulerID := identity.NewID(identity.ResourceScheduler, "scheduler")
	runID := identity.NewID(identity.ResourceRun, "run")
	artifacts := FSArtifacts{DataRoot: root}
	dir := artifacts.RunDir(schedulerID, runID)
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatalf("create artifact directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.json"), []byte("12345"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "result.json"), []byte("1234567"), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}

	info, err := artifacts.InspectRunArtifacts(schedulerID, runID, dir)
	if err != nil || !info.Exists || info.Path != dir || info.Bytes != 12 {
		t.Fatalf("inspect info=%#v err=%v", info, err)
	}
	removed, err := artifacts.RemoveRunArtifacts(schedulerID, runID, dir)
	if err != nil || removed != info {
		t.Fatalf("remove info=%#v err=%v, want %#v", removed, err, info)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("artifact directory still exists: %v", err)
	}
	missing, err := artifacts.InspectRunArtifacts(schedulerID, runID, dir)
	if err != nil || missing.Exists || missing.Path != dir {
		t.Fatalf("missing info=%#v err=%v", missing, err)
	}
}

func TestFSArtifactsRejectsUnsafeRunArtifactPaths(t *testing.T) {
	root := t.TempDir()
	schedulerID := identity.NewID(identity.ResourceScheduler, "scheduler")
	runID := identity.NewID(identity.ResourceRun, "run")
	artifacts := FSArtifacts{DataRoot: root}
	dir := artifacts.RunDir(schedulerID, runID)

	tests := []struct {
		name        string
		schedulerID string
		runID       string
		recorded    string
		wantError   string
	}{
		{name: "invalid scheduler id", schedulerID: "scheduler", runID: runID, wantError: "complete resource ids"},
		{name: "invalid run id", schedulerID: schedulerID, runID: "run", wantError: "complete resource ids"},
		{name: "recorded mismatch", schedulerID: schedulerID, runID: runID, recorded: filepath.Join(root, "elsewhere"), wantError: "does not match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := artifacts.InspectRunArtifacts(test.schedulerID, test.runID, test.recorded)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error=%v, want %q", err, test.wantError)
			}
		})
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatalf("create runs root: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatalf("create run symlink: %v", err)
	}
	if _, err := artifacts.InspectRunArtifacts(schedulerID, runID, dir); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("run symlink error=%v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory changed: %v", err)
	}
}

func TestFSArtifactsRejectsSymlinkedParentBelowDataRoot(t *testing.T) {
	root := t.TempDir()
	schedulerID := identity.NewID(identity.ResourceScheduler, "scheduler")
	runID := identity.NewID(identity.ResourceRun, "run")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "schedulers"), 0o755); err != nil {
		t.Fatalf("create schedulers root: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "schedulers", schedulerID)); err != nil {
		t.Fatalf("create scheduler symlink: %v", err)
	}
	artifacts := FSArtifacts{DataRoot: root}
	if _, err := artifacts.InspectRunArtifacts(schedulerID, runID, ""); err == nil || !strings.Contains(err.Error(), "parent must not contain symlinks") {
		t.Fatalf("symlink parent error=%v", err)
	}
}
