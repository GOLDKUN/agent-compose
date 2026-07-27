package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"agent-compose/pkg/storage/configstore"
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

func TestRunRejectsInvalidSourceAndOptionShapes(t *testing.T) {
	if report, err := Run(context.Background(), Options{}); err == nil || report.Error == "" {
		t.Fatalf("missing options report = %+v, err=%v", report, err)
	}
	same := t.TempDir()
	if report, err := Run(context.Background(), Options{Source: same, Target: same}); err == nil || report.Error == "" {
		t.Fatalf("same roots report = %+v, err=%v", report, err)
	}
	sourceFile := filepath.Join(t.TempDir(), "source-file")
	if err := os.WriteFile(sourceFile, []byte("not a root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if report, err := Run(context.Background(), Options{Source: sourceFile, Target: filepath.Join(t.TempDir(), "target")}); err == nil || report.Error == "" {
		t.Fatalf("source file report = %+v, err=%v", report, err)
	}
	emptyRoot := t.TempDir()
	if report, err := Run(context.Background(), Options{Source: emptyRoot, Target: filepath.Join(t.TempDir(), "target")}); err == nil || report.Error == "" {
		t.Fatalf("missing database report = %+v, err=%v", report, err)
	}
	unknownRoot := t.TempDir()
	unknownDB, err := sql.Open("sqlite", filepath.Join(unknownRoot, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unknownDB.Exec(`CREATE TABLE unrelated(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := unknownDB.Close(); err != nil {
		t.Fatal(err)
	}
	if report, err := Run(context.Background(), Options{Source: unknownRoot, Target: filepath.Join(t.TempDir(), "target")}); err == nil || report.Error == "" {
		t.Fatalf("unknown schema report = %+v, err=%v", report, err)
	}
}

func TestRunRejectsTargetConflictAndChangedSource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.Open(filepath.Join(source, databaseName), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	conflictingTarget := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(conflictingTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conflictingTarget, "owned.txt"), []byte("operator data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if report, err := Run(context.Background(), Options{Source: source, Target: conflictingTarget}); err == nil || report.Stage != "validate" {
		t.Fatalf("target conflict report = %+v, err=%v", report, err)
	}

	target := filepath.Join(t.TempDir(), "target")
	if report, err := Run(context.Background(), Options{Source: source, Target: target}); err != nil {
		t.Fatalf("initial migration = %+v, err=%v", report, err)
	}
	if err := os.WriteFile(filepath.Join(source, "authoritative.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if report, err := Run(context.Background(), Options{Source: source, Target: target}); err == nil || report.Stage != "validate" || report.Error == "" {
		t.Fatalf("changed source report = %+v, err=%v", report, err)
	}
}

func TestRunRejectsSymlinkedTargetComponentOnResume(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "sandboxes", "sandbox-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "sandboxes", "sandbox-1", "state.json"), []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.Open(filepath.Join(source, databaseName), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := fingerprintRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeJournal(target, journal{SourceFingerprint: fingerprint, Stage: "files"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "sandboxes")); err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err == nil || report.Stage != "files" || report.Error == "" {
		t.Fatalf("target symlink report = %+v, err=%v", report, err)
	}
}

func TestRunConvertsStandaloneVersionedAndUnversionedSources(t *testing.T) {
	for _, versioned := range []bool{true, false} {
		name := "unversioned"
		if versioned {
			name = "versioned"
		}
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source")
			target := filepath.Join(t.TempDir(), "target")
			if err := os.MkdirAll(source, 0o700); err != nil {
				t.Fatal(err)
			}
			artifactDir := filepath.Join(source, "loaders", "standalone-loader", "runs", "legacy-run")
			if err := os.MkdirAll(artifactDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(artifactDir, "result.json"), []byte(`{"ok":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(source, "sessions", "legacy-sandbox"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "sessions", "legacy-sandbox", "state.json"), []byte(`{"ready":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(source, databaseName))
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			if err := sqlite.MigrateThrough(context.Background(), db, 4); err != nil {
				t.Fatalf("create v4 fixture: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO workspace_config(id,name,type,config_json,created_at,updated_at) VALUES('legacy-workspace','Legacy Workspace','file','{}',1000,1001)`); err != nil {
				t.Fatalf("insert legacy workspace: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO agent_definition(id,name,enabled,provider,workspace_id,created_at,updated_at) VALUES('standalone-agent','Standalone Agent',1,'codex','legacy-workspace',1000,1001)`); err != nil {
				t.Fatalf("insert standalone agent: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO loader(id,name,runtime,script,workspace_id,enabled,created_at,updated_at) VALUES('standalone-loader','Standalone Scheduler','scheduler','function main() {}','legacy-workspace',1,1000,1001)`); err != nil {
				t.Fatalf("insert standalone scheduler: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO loader_run(loader_id,run_id,status,started_at,artifacts_dir) VALUES('standalone-loader','legacy-run','succeeded',1700000000,?)`, artifactDir); err != nil {
				t.Fatalf("insert standalone run: %v", err)
			}
			if !versioned {
				if _, err := db.Exec(`DROP TABLE schema_migrations`); err != nil {
					t.Fatalf("make fixture unversioned: %v", err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			report, err := Run(context.Background(), Options{Source: source, Target: target})
			if err != nil {
				t.Fatalf("Run returned error: %v (%+v)", err, report)
			}
			if report.TargetVersion != 7 || len(report.Warnings) != 1 {
				t.Fatalf("report = %+v", report)
			}
			targetDB, err := sql.Open("sqlite", filepath.Join(target, databaseName))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = targetDB.Close() }()
			var projects, agents, schedulers int
			if err := targetDB.QueryRow(`SELECT COUNT(*) FROM project WHERE name=?`, legacyDefaultProjectName).Scan(&projects); err != nil {
				t.Fatal(err)
			}
			if err := targetDB.QueryRow(`SELECT COUNT(*) FROM project_agent`).Scan(&agents); err != nil {
				t.Fatal(err)
			}
			if err := targetDB.QueryRow(`SELECT COUNT(*) FROM project_scheduler`).Scan(&schedulers); err != nil {
				t.Fatal(err)
			}
			if projects != 1 || agents != 2 || schedulers != 1 {
				t.Fatalf("converted counts project=%d agents=%d schedulers=%d", projects, agents, schedulers)
			}
			definitionStore := configstore.FromDB(targetDB)
			if agent, err := definitionStore.GetAgentDefinition(context.Background(), "standalone-agent"); err != nil || agent.WorkspaceID != "legacy-workspace" {
				t.Fatalf("converted standalone agent = %#v, err=%v", agent, err)
			}
			var schedulerID string
			if err := targetDB.QueryRow(`SELECT id FROM project_scheduler`).Scan(&schedulerID); err != nil {
				t.Fatal(err)
			}
			if scheduler, err := definitionStore.GetScheduler(context.Background(), schedulerID); err != nil || scheduler.Summary.WorkspaceID != "legacy-workspace" {
				t.Fatalf("converted standalone scheduler = %#v, err=%v", scheduler, err)
			}
			var startedAt int64
			if err := targetDB.QueryRow(`SELECT started_at FROM scheduler_run WHERE run_id='legacy-run'`).Scan(&startedAt); err != nil {
				t.Fatal(err)
			}
			if !versioned && startedAt != 1700000000000 {
				t.Fatalf("unversioned run started_at = %d", startedAt)
			}
			var migratedArtifacts string
			var migratedSchedulerID string
			if err := targetDB.QueryRow(`SELECT scheduler_id, artifacts_dir FROM scheduler_run WHERE run_id='legacy-run'`).Scan(&migratedSchedulerID, &migratedArtifacts); err != nil {
				t.Fatal(err)
			}
			wantArtifacts := filepath.Join(target, "schedulers", migratedSchedulerID, "runs", "legacy-run")
			if migratedArtifacts != wantArtifacts {
				t.Fatalf("migrated artifacts path = %q, want %q", migratedArtifacts, wantArtifacts)
			}
			for _, copied := range []string{
				filepath.Join(wantArtifacts, "result.json"),
				filepath.Join(target, "sandboxes", "legacy-sandbox", "state.json"),
			} {
				if _, err := os.Stat(copied); err != nil {
					t.Fatalf("mapped legacy file %s: %v", copied, err)
				}
			}
		})
	}
}

func TestRunNormalizesUnversionedTextTimestamps(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(source, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := sqlite.MigrateThrough(context.Background(), db, 4); err != nil {
		t.Fatalf("create v4 fixture: %v", err)
	}
	statements := []string{
		`DROP TABLE global_env`,
		`CREATE TABLE global_env(name TEXT PRIMARY KEY, value TEXT NOT NULL, secret INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL)`,
		`INSERT INTO global_env(name,value,updated_at) VALUES('TOKEN','preserved','2024-01-02 03:04:05')`,
		`DROP TABLE workspace_config`,
		`CREATE TABLE workspace_config(id TEXT PRIMARY KEY, name TEXT NOT NULL, type TEXT NOT NULL, config_json TEXT NOT NULL DEFAULT '{}', comment TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO workspace_config(id,name,type,created_at,updated_at) VALUES('workspace-1','Workspace','file','1700000000','2024-01-02 03:04:05')`,
		`INSERT INTO loader(id,name,runtime,script,enabled,created_at,updated_at) VALUES('standalone-loader','Scheduler','scheduler','function main() {}',1,1700000000,1700000001)`,
		`DROP TABLE loader_binding`,
		`CREATE TABLE loader_binding_legacy(loader_id TEXT NOT NULL, sandbox_id TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY(loader_id))`,
		`INSERT INTO loader_binding_legacy(loader_id,sandbox_id,created_at,updated_at) VALUES('standalone-loader','sandbox-1',1700000000,1700000001)`,
		`DROP TABLE schema_migrations`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare text timestamp fixture with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err != nil {
		t.Fatalf("Run returned error: %v (%+v)", err, report)
	}
	targetDB, err := sql.Open("sqlite", filepath.Join(target, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = targetDB.Close() }()
	var envValue, envType string
	var envUpdated int64
	if err := targetDB.QueryRow(`SELECT value, typeof(updated_at), updated_at FROM global_env WHERE name='TOKEN'`).Scan(&envValue, &envType, &envUpdated); err != nil {
		t.Fatal(err)
	}
	if envValue != "preserved" || envType != "integer" || envUpdated != 1704164645 {
		t.Fatalf("normalized global env value=%q type=%q updated=%d", envValue, envType, envUpdated)
	}
	var createdType, updatedType string
	var createdAt, updatedAt int64
	if err := targetDB.QueryRow(`SELECT typeof(created_at), created_at, typeof(updated_at), updated_at FROM workspace_config WHERE id='workspace-1'`).Scan(&createdType, &createdAt, &updatedType, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if createdType != "integer" || createdAt != 1700000000 || updatedType != "integer" || updatedAt != 1704164645 {
		t.Fatalf("normalized workspace created=%s/%d updated=%s/%d", createdType, createdAt, updatedType, updatedAt)
	}
	var bindingSchedulerID, bindingSandboxID string
	if err := targetDB.QueryRow(`SELECT scheduler_id, sandbox_id FROM scheduler_sandbox_binding`).Scan(&bindingSchedulerID, &bindingSandboxID); err != nil {
		t.Fatal(err)
	}
	if bindingSchedulerID == "standalone-loader" || bindingSandboxID != "sandbox-1" {
		t.Fatalf("recovered binding scheduler=%q sandbox=%q", bindingSchedulerID, bindingSandboxID)
	}
}

func TestRunRejectsConflictingLegacyAndNativeFileLayouts(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	for _, root := range []string{"sessions", "sandboxes"} {
		dir := filepath.Join(source, root, "same-sandbox")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(root), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	database, err := sqlite.Open(filepath.Join(source, databaseName), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err == nil || report.Stage != "files" || report.Error == "" {
		t.Fatalf("conflicting layout report = %+v, err=%v", report, err)
	}
}

func TestRunAppendsProjectionRevisionAndBackfillsProvableSchedulerRun(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(source, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := sqlite.MigrateThrough(context.Background(), db, 4); err != nil {
		t.Fatalf("create v4 fixture: %v", err)
	}
	statements := []string{
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at) VALUES('project-1','project',1,'old',1000,1001)`,
		`INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES('project-1',1,'old','{"name":"project","agents":[]}',1000)`,
		`INSERT INTO agent_definition(id,name,enabled,provider,managed_project_id,managed_project_revision,managed_agent_name,created_at,updated_at) VALUES('agent-1','Worker',1,'codex','wrong-project',0,'wrong-agent',1000,1001)`,
		`INSERT INTO project_agent(id,name,project_id,agent_name,managed_agent_id,revision,provider,scheduler_enabled,created_at,updated_at) VALUES('agent-1','Worker','project-1','worker','agent-1',1,'codex',1,1000,1001)`,
		`INSERT INTO loader(id,name,script,agent_id,managed_project_id,managed_project_revision,managed_agent_name,managed_scheduler_id,created_at,updated_at) VALUES('loader-1','Worker scheduler','run()','agent-1','wrong-project',0,'wrong-agent','wrong-scheduler',1000,1001)`,
		`INSERT INTO project_scheduler(id,project_id,scheduler_id,agent_name,managed_loader_id,revision,enabled,created_at,updated_at) VALUES('scheduler-1','project-1','scheduler-1','worker','loader-1',1,1,1000,1001)`,
		`INSERT INTO project_run(run_id,project_id,project_name,project_revision,agent_name,managed_agent_id,source,scheduler_id,status,sandbox_id,created_at,updated_at) VALUES('project-run-1','project-1','project',1,'worker','agent-1','scheduler','scheduler-1','succeeded','sandbox-1',1100,1200)`,
		`INSERT INTO loader_run(loader_id,run_id,status,started_at) VALUES('loader-1','scheduler-run-1','succeeded',1100)`,
		`INSERT INTO loader_event(loader_id,event_id,run_id,type,linked_sandbox_id,created_at) VALUES('loader-1','scheduler-event-1','scheduler-run-1','loader.completed','sandbox-1',1200)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed projection fixture with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err != nil {
		t.Fatalf("Run returned error: %v (%+v)", err, report)
	}
	if len(report.Warnings) != 1 || report.TargetVersion != 7 {
		t.Fatalf("report = %+v", report)
	}
	targetDB, err := sql.Open("sqlite", filepath.Join(target, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = targetDB.Close() }()
	var currentRevision, revisionCount int64
	if err := targetDB.QueryRow(`SELECT current_revision FROM project WHERE id='project-1'`).Scan(&currentRevision); err != nil {
		t.Fatal(err)
	}
	if err := targetDB.QueryRow(`SELECT COUNT(*) FROM project_revision WHERE project_id='project-1'`).Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if currentRevision != 2 || revisionCount != 2 {
		t.Fatalf("project revision current=%d count=%d, want 2/2", currentRevision, revisionCount)
	}
	var schedulerRunID sql.NullString
	if err := targetDB.QueryRow(`SELECT scheduler_run_id FROM project_run WHERE run_id='project-run-1'`).Scan(&schedulerRunID); err != nil {
		t.Fatal(err)
	}
	if !schedulerRunID.Valid || schedulerRunID.String != "scheduler-run-1" {
		t.Fatalf("scheduler run link = %#v", schedulerRunID)
	}
	store := configstore.FromDB(targetDB)
	if agent, err := store.GetAgentDefinition(context.Background(), "agent-1"); err != nil || agent.ProjectRevision != 2 || agent.AgentName != "worker" {
		t.Fatalf("revision-backed agent = %#v, err=%v", agent, err)
	}
	if scheduler, err := store.GetScheduler(context.Background(), "scheduler-1"); err != nil || scheduler.Summary.ProjectRevision != 2 || scheduler.Summary.AgentName != "worker" {
		t.Fatalf("revision-backed scheduler = %#v, err=%v", scheduler, err)
	}
}

func TestIntegrationLegacyCopyMigrationWorkflows(t *testing.T) {
	TestRunCopiesLatestDataRootAndResumes(t)
	TestRunConvertsStandaloneVersionedAndUnversionedSources(t)
	TestRunNormalizesUnversionedTextTimestamps(t)
	TestRunAppendsProjectionRevisionAndBackfillsProvableSchedulerRun(t)
	TestRunRejectsTargetConflictAndChangedSource(t)
	TestRunRejectsSymlinkedTargetComponentOnResume(t)
}

func TestE2ELegacyCopyMigrationWorkflows(t *testing.T) {
	TestRunCopiesLatestDataRootAndResumes(t)
	TestRunDryRunDoesNotCreateTarget(t)
	TestRunRejectsSourceSymlink(t)
	TestRunRejectsInvalidSourceAndOptionShapes(t)
	TestRunConvertsStandaloneVersionedAndUnversionedSources(t)
	TestRunNormalizesUnversionedTextTimestamps(t)
	TestRunAppendsProjectionRevisionAndBackfillsProvableSchedulerRun(t)
	TestRunRejectsTargetConflictAndChangedSource(t)
	TestRunRejectsSymlinkedTargetComponentOnResume(t)
	TestRunRejectsConflictingLegacyAndNativeFileLayouts(t)
}

func TestE2ELegacyMigrationReportAndPathMappingContracts(t *testing.T) {
	if got := (Report{Stage: "validate", Error: "bad source"}).Text(); got != "legacy migration validate: bad source" {
		t.Fatalf("error report text = %q", got)
	}
	if got := (Report{DryRun: true, SourceVersion: 4}).Text(); got != "legacy migration dry run: source schema version 4 is eligible" {
		t.Fatalf("dry-run report text = %q", got)
	}
	if got := (Report{TargetVersion: 7, CopiedFiles: 2, CopiedBytes: 9, Target: "/target"}).Text(); got != "legacy migration complete: schema v7, 2 files (9 bytes) copied to /target" {
		t.Fatalf("complete report text = %q", got)
	}
	source := filepath.Join(string(filepath.Separator), "old-root")
	target := filepath.Join(string(filepath.Separator), "new-root")
	stored := filepath.Join(source, "loaders", "legacy-loader", "runs", "run-1")
	rewritten, inside, err := migratedStoredPath(stored, source, target, map[string]string{"legacy-loader": "scheduler-1"})
	if err != nil || !inside || rewritten != filepath.Join(target, "schedulers", "scheduler-1", "runs", "run-1") {
		t.Fatalf("mapped stored path = %q inside=%v err=%v", rewritten, inside, err)
	}
	if rewritten, inside, err := migratedStoredPath("relative/path", source, target, nil); err != nil || inside || rewritten != "relative/path" {
		t.Fatalf("relative stored path = %q inside=%v err=%v", rewritten, inside, err)
	}
	used := map[string]struct{}{}
	first := uniqueLegacyName(" Worker! ", "agent-1", "agent", used)
	second := uniqueLegacyName(" Worker! ", "agent-2", "agent", used)
	if first != "worker" || second == first || second == "worker" {
		t.Fatalf("unique legacy names = %q/%q", first, second)
	}
	fallbackName := uniqueLegacyName("!!!", "agent-3", "agent", used)
	if fallbackName != "agent" || shortLegacyID("1234567890123456") != "123456789012" || firstLegacyTime(0, 9) != 9 || firstLegacyTime(7, 9) != 7 {
		t.Fatalf("legacy fallback helpers name=%q short=%q times=%d/%d", fallbackName, shortLegacyID("1234567890123456"), firstLegacyTime(0, 9), firstLegacyTime(7, 9))
	}
	agentJSON := legacyAgentJSON(legacyAgentDefinition{
		name: "Worker", driver: "docker", workspaceID: "workspace-1",
		envJSON: `[{"name":"A","value":"one"}]`, capsetIDs: `["dev"]`, skills: `[{"name":"review"}]`, volumesJSON: `[]`,
		configJSON: `{"jupyter":{"enabled":true},"mcp_servers":{"tools":{"type":"stdio","command":"tool"}}}`,
	}, "worker", map[string]any{"enabled": true})
	if agentJSON["driver"] == nil || agentJSON["workspace"] == nil || agentJSON["jupyter"] == nil || agentJSON["mcp_servers"] == nil || agentJSON["scheduler"] == nil {
		t.Fatalf("legacy agent JSON = %#v", agentJSON)
	}
	if legacyEnvList("not-json") != nil {
		t.Fatal("invalid legacy env JSON was accepted")
	}
	fallback := []any{"fallback"}
	if got := legacyJSONValue("not-json", fallback); len(got.([]any)) != 1 {
		t.Fatalf("legacy JSON fallback = %#v", got)
	}
	if !isIntegerColumnType("BIGINT") || isIntegerColumnType("TEXT") || normalizeSQLiteTimestampExpr("updated_at") == "" {
		t.Fatal("legacy timestamp helpers returned unexpected values")
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if err := execLegacyStatements(ctx, db, "create helper fixture", []string{`CREATE TABLE helper_fixture(id TEXT)`}); err != nil {
		t.Fatal(err)
	}
	if exists, err := sqliteTableExists(ctx, db, "helper_fixture"); err != nil || !exists {
		t.Fatalf("helper fixture exists=%v err=%v", exists, err)
	}
	if exists, err := sqliteTableExists(ctx, db, "missing_fixture"); err != nil || exists {
		t.Fatalf("missing fixture exists=%v err=%v", exists, err)
	}
	if err := addLegacyColumn(ctx, db, "helper_fixture", "count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		t.Fatal(err)
	}
	if err := addLegacyColumn(ctx, db, "helper_fixture", "count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		t.Fatalf("repeat legacy column: %v", err)
	}
	if err := addLegacyColumn(ctx, db, "missing_fixture", "count", "INTEGER"); err != nil {
		t.Fatalf("missing-table legacy column: %v", err)
	}
	columns, err := sqliteTableColumnTypes(ctx, db, "helper_fixture")
	if err != nil || !isIntegerColumnType(columns["count"]) {
		t.Fatalf("helper fixture columns=%#v err=%v", columns, err)
	}
	if err := execLegacyStatements(ctx, db, "invalid helper fixture", []string{"NOT SQL"}); err == nil {
		t.Fatal("invalid legacy statement returned nil error")
	}
}
