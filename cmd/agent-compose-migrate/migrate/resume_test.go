package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-compose/pkg/storage/sqlite"
)

func TestRunResumesFileCopyFromPersistedSchedulerMappings(t *testing.T) {
	for _, unversioned := range []bool{false, true} {
		name := "versioned-v4"
		if unversioned {
			name = "unversioned"
		}
		t.Run(name, func(t *testing.T) {
			source, target := createManagedMigrationRoots(t, unversioned)
			if report, err := Run(context.Background(), Options{Source: source, Target: target}); err != nil {
				t.Fatalf("initial migration report=%+v err=%v", report, err)
			}
			copiedArtifact := filepath.Join(target, "schedulers", "scheduler-1", "runs", "run-1", "result.json")
			if err := os.Remove(copiedArtifact); err != nil {
				t.Fatalf("remove copied artifact before resume: %v", err)
			}
			fingerprint, err := fingerprintRoot(source)
			if err != nil {
				t.Fatal(err)
			}
			state := journal{
				SourceFingerprint: fingerprint,
				Stage:             "files",
				SchedulerIDs:      map[string]string{"loader-1": "scheduler-1"},
				Warnings:          []string{"preserved warning"},
			}
			if err := writeJournal(target, state); err != nil {
				t.Fatal(err)
			}

			report, err := Run(context.Background(), Options{Source: source, Target: target})
			if err != nil || report.Stage != "complete" || report.TargetVersion != 7 {
				t.Fatalf("resumed migration report=%+v err=%v", report, err)
			}
			if len(report.Warnings) != 1 || report.Warnings[0] != "preserved warning" {
				t.Fatalf("resumed warnings=%v", report.Warnings)
			}
			if data, err := os.ReadFile(copiedArtifact); err != nil || string(data) != "preserved" {
				t.Fatalf("resumed artifact=%q err=%v", data, err)
			}
			if _, err := os.Stat(filepath.Join(target, "schedulers", "loader-1")); !os.IsNotExist(err) {
				t.Fatalf("resume copied files under legacy loader identity: %v", err)
			}
		})
	}
}

func TestRunResumesDatabaseStageAfterTargetUpgrade(t *testing.T) {
	source, target := createManagedMigrationRoots(t, false)
	if report, err := Run(context.Background(), Options{Source: source, Target: target}); err != nil {
		t.Fatalf("initial migration report=%+v err=%v", report, err)
	}
	copiedArtifact := filepath.Join(target, "schedulers", "scheduler-1", "runs", "run-1", "result.json")
	if err := os.Remove(copiedArtifact); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := fingerprintRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJournal(target, journal{
		SourceFingerprint: fingerprint,
		Stage:             "database",
		SchedulerIDs:      map[string]string{"loader-1": "scheduler-1"},
		Warnings:          []string{"checkpoint warning"},
	}); err != nil {
		t.Fatal(err)
	}

	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err != nil || report.Stage != "complete" || report.TargetVersion != 7 {
		t.Fatalf("database-stage resume report=%+v err=%v", report, err)
	}
	if data, err := os.ReadFile(copiedArtifact); err != nil || string(data) != "preserved" {
		t.Fatalf("database-stage resumed artifact=%q err=%v", data, err)
	}
}

func TestRunRejectsFileStageJournalWithoutSchedulerMappings(t *testing.T) {
	source, target := createManagedMigrationRoots(t, false)
	if report, err := Run(context.Background(), Options{Source: source, Target: target}); err != nil {
		t.Fatalf("initial migration report=%+v err=%v", report, err)
	}
	fingerprint, err := fingerprintRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJournal(target, journal{SourceFingerprint: fingerprint, Stage: "files"}); err != nil {
		t.Fatal(err)
	}

	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err == nil || report.Stage != "validate" || !strings.Contains(report.Error, "missing scheduler identity mappings") {
		t.Fatalf("missing-map resume report=%+v err=%v", report, err)
	}
}

func TestRunRejectsChangedRuntimeRootOnResume(t *testing.T) {
	source, target := createManagedMigrationRoots(t, false)
	firstRuntimeRoot := filepath.Join(t.TempDir(), "runtime-a")
	if report, err := Run(context.Background(), Options{Source: source, Target: target, RuntimeRoot: firstRuntimeRoot}); err != nil {
		t.Fatalf("initial migration report=%+v err=%v", report, err)
	}

	report, err := Run(context.Background(), Options{
		Source: source, Target: target, RuntimeRoot: filepath.Join(t.TempDir(), "runtime-b"),
	})
	if err == nil || report.Stage != "validate" || !strings.Contains(report.Error, "runtime root changed") {
		t.Fatalf("changed runtime-root report=%+v err=%v", report, err)
	}
}

func createManagedMigrationRoots(t *testing.T, unversioned bool) (string, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	artifactDir := filepath.Join(source, "loaders", "loader-1", "runs", "run-1")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "result.json"), []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(source, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := sqlite.MigrateThrough(context.Background(), db, 4); err != nil {
		_ = db.Close()
		t.Fatalf("create v4 migration fixture: %v", err)
	}
	statements := []string{
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at) VALUES('project-1','project',1,'hash',1,1)`,
		`INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES('project-1',1,'hash','{"name":"project","agents":[{"name":"worker"}]}',1)`,
		`INSERT INTO agent_definition(id,name,managed_project_id,managed_project_revision,managed_agent_name,created_at,updated_at) VALUES('agent-1','worker','project-1',1,'worker',1,1)`,
		`INSERT INTO project_agent(id,name,project_id,agent_name,managed_agent_id,revision,created_at,updated_at) VALUES('agent-1','worker','project-1','worker','agent-1',1,1,1)`,
		`INSERT INTO loader(id,name,script,agent_id,managed_project_id,managed_project_revision,managed_agent_name,managed_scheduler_id,created_at,updated_at) VALUES('loader-1','scheduler','run()','agent-1','project-1',1,'worker','scheduler-1',1,1)`,
		`INSERT INTO project_scheduler(id,project_id,scheduler_id,agent_name,managed_loader_id,revision,created_at,updated_at) VALUES('scheduler-1','project-1','scheduler-1','worker','loader-1',1,1,1)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("seed migration fixture with %q: %v", statement, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO loader_run(loader_id,run_id,status,started_at,artifacts_dir) VALUES('loader-1','run-1','succeeded',1,?)`, artifactDir); err != nil {
		_ = db.Close()
		t.Fatalf("seed scheduler run migration fixture: %v", err)
	}
	if unversioned {
		if _, err := db.Exec(`DROP TABLE schema_migrations`); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return source, target
}
