package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agent-compose/pkg/storage/sqlite"
)

func TestRunCopiesLatestDataRootAndResumes(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(filepath.Join(source, "sandboxes", "sandbox-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.Open(filepath.Join(source, databaseName), 0)
	if err != nil {
		t.Fatalf("create source database: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close source database: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "sandboxes", "sandbox-1", "artifact.txt"), []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err != nil {
		t.Fatalf("Run returned error: %v (%+v)", err, report)
	}
	if report.TargetVersion != 7 || report.CopiedFiles != 1 || report.Stage != "complete" {
		t.Fatalf("report = %+v", report)
	}
	data, err := os.ReadFile(filepath.Join(target, "sandboxes", "sandbox-1", "artifact.txt"))
	if err != nil || string(data) != "preserved" {
		t.Fatalf("copied artifact = %q, %v", data, err)
	}
	resumed, err := Run(context.Background(), Options{Source: source, Target: target})
	if err != nil || resumed.Stage != "complete" {
		t.Fatalf("resume report = %+v, err=%v", resumed, err)
	}
}

func TestRunDryRunDoesNotCreateTarget(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.Open(filepath.Join(source, databaseName), 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	report, err := Run(context.Background(), Options{Source: source, Target: target, DryRun: true})
	if err != nil || report.Stage != "eligible" {
		t.Fatalf("dry-run report = %+v, err=%v", report, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dry run created target: %v", err)
	}
}

func TestRunRejectsSourceSymlink(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.Open(filepath.Join(source, databaseName), 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	if err := os.Symlink(databaseName, filepath.Join(source, "linked.db")); err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err == nil || report.Error == "" {
		t.Fatalf("symlink report = %+v, err=%v", report, err)
	}
}
