package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-compose/pkg/capabilities"
	"agent-compose/pkg/llms"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/runs"
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
	if err := os.Symlink("artifact.txt", filepath.Join(source, "sandboxes", "sandbox-1", "artifact-link")); err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err != nil {
		t.Fatalf("Run returned error: %v (%+v)", err, report)
	}
	if report.TargetVersion != currentSchemaVersion || report.CopiedFiles != 2 || report.Stage != "complete" || report.SourceFingerprint == "" {
		t.Fatalf("report = %+v", report)
	}
	data, err := os.ReadFile(filepath.Join(target, "sandboxes", "sandbox-1", "artifact.txt"))
	if err != nil || string(data) != "preserved" {
		t.Fatalf("copied artifact = %q, %v", data, err)
	}
	if linkTarget, err := os.Readlink(filepath.Join(target, "sandboxes", "sandbox-1", "artifact-link")); err != nil || linkTarget != "artifact.txt" {
		t.Fatalf("copied artifact symlink = %q, %v", linkTarget, err)
	}
	resumed, err := Run(context.Background(), Options{Source: source, Target: target})
	if err != nil || resumed.Stage != "complete" {
		t.Fatalf("resume report = %+v, err=%v", resumed, err)
	}
}

func TestRunPreservesPartitionedLifecyclePathsAcrossDryRunCopyAndResume(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	directoryName := strings.Repeat("a", 64)
	sandboxID := "sha256:" + directoryName
	partition := filepath.Join("2026", "07", "26")
	sourceSandbox := filepath.Join(source, "sandboxes", partition, directoryName)
	writeMigrationJSON(t, filepath.Join(sourceSandbox, "metadata.json"), map[string]any{
		"summary": map[string]any{"id": sandboxID, "vm_status": "STOPPED"},
	})
	writeMigrationJSON(t, filepath.Join(source, "sandboxes", ".lifecycle", sandboxID+".json"), map[string]any{
		"version": 1, "sandbox_id": sandboxID, "sandbox_path": sourceSandbox,
		"owned_resources": []any{
			map[string]any{"kind": "sandbox-directory", "path": sourceSandbox},
		},
	})
	database, err := sqlite.Open(filepath.Join(source, databaseName), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	dryRun, err := Run(context.Background(), Options{Source: source, Target: target, DryRun: true})
	if err != nil || dryRun.Stage != "eligible" {
		t.Fatalf("partitioned dry-run report=%+v err=%v", dryRun, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dry run created target: %v", err)
	}

	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err != nil || report.Stage != "complete" {
		t.Fatalf("partitioned copy report=%+v err=%v", report, err)
	}
	wantPath := filepath.Join(target, "sandboxes", partition, directoryName)
	lifecycle := readMigrationJSON(t, filepath.Join(target, "sandboxes", ".lifecycle", sandboxID+".json"))
	resources := lifecycle["owned_resources"].([]any)
	if lifecycle["sandbox_path"] != wantPath || resources[0].(map[string]any)["path"] != wantPath {
		t.Fatalf("partitioned lifecycle=%#v", lifecycle)
	}

	resumed, err := Run(context.Background(), Options{Source: source, Target: target})
	if err != nil || resumed.Stage != "complete" {
		t.Fatalf("partitioned resume report=%+v err=%v", resumed, err)
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
	var progress strings.Builder
	report, err := Run(context.Background(), Options{Source: source, Target: target, DryRun: true, Progress: &progress})
	if err != nil || report.Stage != "eligible" {
		t.Fatalf("dry-run report = %+v, err=%v", report, err)
	}
	if report.SourceFingerprint != "" {
		t.Fatalf("dry-run calculated full source fingerprint %q", report.SourceFingerprint)
	}
	for _, stage := range []string{"[preflight]", "[database]", "[files]", "[complete]"} {
		if !strings.Contains(progress.String(), stage) {
			t.Fatalf("dry-run progress %q does not contain %s", progress.String(), stage)
		}
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

func TestFingerprintRootIncludesUserSymlinkTarget(t *testing.T) {
	source := t.TempDir()
	database, err := sqlite.Open(filepath.Join(source, databaseName), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(source, "workspaces", "workspace-1", "content", "current")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("revision-a", link); err != nil {
		t.Fatal(err)
	}
	first, err := fingerprintRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("revision-b", link); err != nil {
		t.Fatal(err)
	}
	second, err := fingerprintRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("source fingerprint ignored a changed symlink target")
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

func TestRunRejectsNestedDataRootsBeforeCreatingTarget(t *testing.T) {
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
	target := filepath.Join(source, "migrated", "data")
	report, err := Run(context.Background(), Options{Source: source, Target: target})
	if err == nil || report.Stage != "validate" || !strings.Contains(report.Error, "target must not be nested inside source") {
		t.Fatalf("nested target report=%+v, err=%v", report, err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("nested target was created: %v", statErr)
	}

	parent := t.TempDir()
	nestedSource := filepath.Join(parent, "source")
	if err := os.MkdirAll(nestedSource, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateMigrationDataRoots(nestedSource, parent); err == nil || !strings.Contains(err.Error(), "source must not be nested inside target") {
		t.Fatalf("nested source validation error=%v", err)
	}

	symlinkParent := t.TempDir()
	symlink := filepath.Join(symlinkParent, "source-link")
	if err := os.Symlink(source, symlink); err != nil {
		t.Fatal(err)
	}
	if err := validateMigrationDataRoots(source, filepath.Join(symlink, "nested")); err == nil || !strings.Contains(err.Error(), "target must not be nested inside source") {
		t.Fatalf("symlink nested target validation error=%v", err)
	}
}

func TestLegacyProjectWorkspaceRejectsEscapingFileID(t *testing.T) {
	if _, err := legacyProjectWorkspace("../outside", "Unsafe", "file", "{}"); err == nil {
		t.Fatal("legacyProjectWorkspace accepted an escaping file workspace id")
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
	sourceDB, err := openReadOnly(filepath.Join(source, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshotDatabase(context.Background(), sourceDB, filepath.Join(target, databaseName)); err != nil {
		_ = sourceDB.Close()
		t.Fatal(err)
	}
	if err := sourceDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeJournal(target, journal{SourceFingerprint: fingerprint, Stage: "files", SchedulerIDs: map[string]string{}}); err != nil {
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
			writeMigrationJSON(t, filepath.Join(source, "sessions", "legacy-sandbox", "metadata.json"), map[string]any{
				"summary": map[string]any{
					"id": "legacy-sandbox", "vm_status": "STOPPED",
					"tags": []any{
						map[string]any{"name": "source", "value": "agent"},
						map[string]any{"name": "agent_id", "value": "standalone-agent"},
						map[string]any{"name": "agent_name", "value": "123 Worker"},
					},
				},
			})
			workspaceContent := filepath.Join(source, "workspaces", "legacy-workspace", "content")
			if err := os.MkdirAll(workspaceContent, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspaceContent, "README.md"), []byte("legacy workspace"), 0o600); err != nil {
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
			if _, err := db.Exec(`INSERT INTO agent_definition(id,name,enabled,provider,model,system_prompt,workspace_id,config_json,capset_ids,skills,created_at,updated_at) VALUES('standalone-agent','123 Worker',1,'codex','legacy-model','preserve this prompt','legacy-workspace','{"mcp_servers":{"tools":{"type":"local","command":"tool","env":{"TOKEN":{"value":"mcp-secret","secret":true}}}},"octobus_servers":{"internal":{"url":"https://octobus.example","token":"legacy-secret"}}}','["internal/dev"]','[{"name":"review"}]',1000,1001)`); err != nil {
				t.Fatalf("insert standalone agent: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO agent_definition(id,name,enabled,deleted_at,provider,created_at,updated_at) VALUES('deleted-agent','Deleted Agent',1,1002,'codex',1000,1001)`); err != nil {
				t.Fatalf("insert deleted standalone agent: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO loader(id,name,runtime,script,workspace_id,agent_id,enabled,created_at,updated_at) VALUES('standalone-loader','Standalone Scheduler','scheduler','function main() {}','legacy-workspace','standalone-agent',1,1000,1001)`); err != nil {
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
			if report.TargetVersion != currentSchemaVersion || len(report.Warnings) != 1 {
				t.Fatalf("report = %+v", report)
			}
			targetDatabase, err := sqlite.Open(filepath.Join(target, databaseName), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = targetDatabase.Close() }()
			targetDB := targetDatabase.DB()
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
			if projects != 1 || agents != 1 || schedulers != 1 {
				t.Fatalf("converted counts project=%d agents=%d schedulers=%d", projects, agents, schedulers)
			}
			definitionStore := configstore.FromDB(targetDB)
			var projectID, agentID, agentName string
			if err := targetDB.QueryRow(`SELECT project_id,id,agent_name FROM project_agent`).Scan(&projectID, &agentID, &agentName); err != nil {
				t.Fatal(err)
			}
			wantAgentID, err := domain.StableProjectAgentID(projectID, agentName)
			if err != nil || agentID != wantAgentID || agentName != "agent-123worker" {
				t.Fatalf("native agent identity id=%q name=%q want=%q, err=%v", agentID, agentName, wantAgentID, err)
			}
			convertedDefinition, definitionErr := definitionStore.GetAgentDefinition(context.Background(), agentID)
			if definitionErr != nil || convertedDefinition.WorkspaceID != "legacy-workspace" || convertedDefinition.Model != "legacy-model" || convertedDefinition.SystemPrompt != "preserve this prompt" || len(convertedDefinition.Skills) != 1 {
				t.Fatalf("converted standalone agent = %#v, err=%v", convertedDefinition, definitionErr)
			}
			octoBusServers, octoBusErr := capabilities.AgentOctoBusServers(convertedDefinition)
			if octoBusErr != nil || octoBusServers["internal"].URL != "https://octobus.example" || octoBusServers["internal"].Token != "legacy-secret" {
				t.Fatalf("converted standalone OctoBus servers=%#v err=%v", octoBusServers, octoBusErr)
			}
			mcpServers := llms.AgentMCPConfig(convertedDefinition)
			if mcpServers["tools"].Command != "tool" || mcpServers["tools"].Env["TOKEN"].Value != "mcp-secret" || !mcpServers["tools"].Env["TOKEN"].Secret {
				t.Fatalf("converted standalone MCP servers=%#v", mcpServers)
			}
			metadata := readMigrationJSON(t, filepath.Join(target, "sandboxes", "legacy-sandbox", "metadata.json"))
			tags := migrationTagValues(metadata["summary"].(map[string]any)["tags"])
			if tags["agent_id"] != agentID || tags["agent_name"] != agentName || tags["agent"] != agentName || tags["project"] != projectID || tags["project_id"] != projectID {
				t.Fatalf("converted standalone sandbox tags=%v", tags)
			}
			var deletedCount int
			if err := targetDB.QueryRow(`SELECT COUNT(*) FROM project_agent WHERE name='Deleted Agent'`).Scan(&deletedCount); err != nil || deletedCount != 0 {
				t.Fatalf("deleted standalone agent count=%d, err=%v", deletedCount, err)
			}
			var schedulerID string
			if err := targetDB.QueryRow(`SELECT id FROM project_scheduler`).Scan(&schedulerID); err != nil {
				t.Fatal(err)
			}
			if scheduler, err := definitionStore.GetScheduler(context.Background(), schedulerID); err != nil || scheduler.Summary.WorkspaceID != "legacy-workspace" {
				t.Fatalf("converted standalone scheduler = %#v, err=%v", scheduler, err)
			}
			project, err := definitionStore.GetProject(context.Background(), projectID)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := definitionStore.GetProjectRevision(context.Background(), projectID, project.CurrentRevision)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := runs.DecodeRevisionSpec(revision.SpecJSON)
			if err != nil {
				t.Fatalf("decode migrated revision: %v", err)
			}
			if len(spec.GetOctobusServers()) != 1 || spec.GetOctobusServers()[0].GetName() != "internal" || spec.GetOctobusServers()[0].GetUrl() != "https://octobus.example" || spec.GetOctobusServers()[0].GetToken() != "legacy-secret" {
				t.Fatalf("migrated revision OctoBus servers=%#v", spec.GetOctobusServers())
			}
			agentSpec, ok := runs.AgentSpecByName(spec, agentName)
			if !ok {
				t.Fatalf("migrated revision missing agent %s", agentName)
			}
			_, resolvedWorkspace, err := runs.ProjectRunWorkspaceSpecsFromV2(spec.GetWorkspaces(), agentSpec.GetWorkspace())
			if err != nil || resolvedWorkspace == nil || resolvedWorkspace.Provider != "file" || resolvedWorkspace.Path != filepath.ToSlash(filepath.Join("workspaces", "legacy-workspace", "content")) {
				t.Fatalf("resolved migrated workspace=%#v, err=%v", resolvedWorkspace, err)
			}
			resolvedPath, err := runs.ResolveLocalProjectWorkspacePath(project, resolvedWorkspace.Path)
			if err != nil {
				t.Fatalf("resolve migrated workspace path: %v", err)
			}
			if data, readErr := os.ReadFile(filepath.Join(resolvedPath, "README.md")); readErr != nil || string(data) != "legacy workspace" {
				t.Fatalf("materialized migrated workspace data=%q, err=%v", data, readErr)
			}
			coordinator := runs.NewCoordinator(definitionStore, domain.StableProjectRunID)
			createdRun, err := coordinator.BeginRun(context.Background(), runs.StartRequest{ProjectID: projectID, AgentName: agentName, Source: domain.ProjectRunSourceAPI, ClientRequestID: "post-migration-run"})
			if err != nil || createdRun.AgentID != agentID {
				t.Fatalf("begin post-migration run=%#v, err=%v", createdRun, err)
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
				filepath.Join(target, "workspaces", "legacy-workspace", "content", "README.md"),
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
		`INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES('project-1',1,'old','{"name":"project","variables":[{"name":"TOKEN","value":"preserved"}],"workspaces":[{"key":"repo","provider":"file","path":"."}],"volumes":[{"key":"cache","name":"preserved-volume"}],"mcp_servers":[{"name":"tools","type":"stdio","command":"preserved-mcp"}],"octobus_servers":[{"name":"internal","url":"https://preserved.invalid"}],"agents":[]}',1000)`,
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
	if len(report.Warnings) != 1 || report.TargetVersion != currentSchemaVersion {
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
	var alignedSpecJSON string
	if err := targetDB.QueryRow(`SELECT spec_json FROM project_revision WHERE project_id='project-1' AND revision=2`).Scan(&alignedSpecJSON); err != nil {
		t.Fatal(err)
	}
	var alignedSpec map[string]any
	if err := json.Unmarshal([]byte(alignedSpecJSON), &alignedSpec); err != nil {
		t.Fatalf("decode aligned revision: %v", err)
	}
	for _, field := range []string{"variables", "workspaces", "volumes", "mcp_servers", "octobus_servers"} {
		items, ok := alignedSpec[field].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("aligned revision field %s = %#v", field, alignedSpec[field])
		}
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
	relativeLegacyPath := filepath.Join("sessions", "sandbox-1", "workspace")
	if rewritten, inside, err := migratedStoredPath(relativeLegacyPath, source, target, nil); err != nil || !inside || rewritten != filepath.Join(target, "sandboxes", "sandbox-1", "workspace") {
		t.Fatalf("relative legacy stored path = %q inside=%v err=%v", rewritten, inside, err)
	}
	for _, external := range []string{
		filepath.FromSlash("/external/volumes/sessions/backup"),
		filepath.Join("..", "sessions", "sandbox-1"),
		`C:\data\loaders\legacy-loader\runs\run-1`,
	} {
		if rewritten, inside, err := migratedStoredPath(external, source, target, map[string]string{"legacy-loader": "scheduler-1"}); err != nil || inside || rewritten != external {
			t.Fatalf("external stored path %q = %q inside=%v err=%v", external, rewritten, inside, err)
		}
	}
	alreadyMigrated := filepath.Join(target, "schedulers", "scheduler-1", "runs", "run-1")
	if rewritten, inside, err := migratedStoredPath(alreadyMigrated, source, target, nil); err != nil || !inside || rewritten != alreadyMigrated {
		t.Fatalf("already migrated stored path = %q inside=%v err=%v", rewritten, inside, err)
	}
	containerPath := filepath.FromSlash("/data/sessions/sandbox-1/state/runs/run-1/transcript.txt")
	sameRuntimeRoot := filepath.FromSlash("/data")
	wantSameRootPath := filepath.FromSlash("/data/sandboxes/sandbox-1/state/runs/run-1/transcript.txt")
	if rewritten, inside, err := migratedStoredPath(containerPath, source, sameRuntimeRoot, nil); err != nil || !inside || rewritten != wantSameRootPath {
		t.Fatalf("same-root container stored path = %q inside=%v err=%v, want %q", rewritten, inside, err, wantSameRootPath)
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
