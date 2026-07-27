package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-compose/pkg/storage/configstore"
	"agent-compose/pkg/storage/sqlite"
)

func TestIntegrationRunMigratesDataRootInPlaceWithoutCopyingSandboxData(t *testing.T) {
	root, _ := createManagedMigrationRoots(t, false)
	sandboxID := "sandbox-1"
	legacySandbox := filepath.Join(root, "sessions", sandboxID)
	if err := os.MkdirAll(filepath.Join(legacySandbox, "workspace"), 0o700); err != nil {
		t.Fatal(err)
	}
	largeState := filepath.Join(legacySandbox, "state.img")
	stateFile, err := os.OpenFile(largeState, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateFile.Truncate(8 << 20); err != nil {
		_ = stateFile.Close()
		t.Fatal(err)
	}
	if err := stateFile.Close(); err != nil {
		t.Fatal(err)
	}
	legacyWorkspaceLink := filepath.Join(legacySandbox, "workspace", "state-link")
	if err := os.Symlink(filepath.Join("..", "state.img"), legacyWorkspaceLink); err != nil {
		t.Fatal(err)
	}
	legacyWorkspaceLinkInfo, err := os.Lstat(legacyWorkspaceLink)
	if err != nil {
		t.Fatal(err)
	}
	legacyCrossLink := filepath.Join(legacySandbox, "workspace", "scheduler-result")
	legacyCrossTarget := filepath.Join("..", "..", "..", "loaders", "loader-1", "runs", "run-1", "result.json")
	if err := os.Symlink(legacyCrossTarget, legacyCrossLink); err != nil {
		t.Fatal(err)
	}
	legacyAbsoluteLink := filepath.Join(legacySandbox, "workspace", "absolute-state")
	if err := os.Symlink(largeState, legacyAbsoluteLink); err != nil {
		t.Fatal(err)
	}
	writeMigrationJSON(t, filepath.Join(legacySandbox, "metadata.json"), map[string]any{
		"summary": map[string]any{
			"id":             sandboxID,
			"vm_status":      "STOPPED",
			"workspace_path": filepath.Join(root, "sessions", sandboxID, "workspace"),
		},
	})
	writeMigrationJSON(t, filepath.Join(root, "sessions", ".lifecycle", sandboxID+".json"), map[string]any{
		"sandbox_id":   sandboxID,
		"sandbox_path": filepath.Join(root, "sessions", sandboxID),
		"owned_resources": []any{
			map[string]any{"kind": "sandbox-directory", "path": filepath.Join(root, "sessions", sandboxID)},
		},
	})
	legacyStateInfo, err := os.Stat(largeState)
	if err != nil {
		t.Fatal(err)
	}
	legacyArtifact := filepath.Join(root, "loaders", "loader-1", "runs", "run-1", "result.json")
	legacyArtifactInfo, err := os.Stat(legacyArtifact)
	if err != nil {
		t.Fatal(err)
	}

	dryRun, err := Run(context.Background(), Options{Source: root, Target: root, DryRun: true})
	if err != nil || dryRun.Stage != "eligible" || !dryRun.InPlace || dryRun.CheckedBytes < 8<<20 || dryRun.CopiedBytes != 0 {
		t.Fatalf("in-place dry run report=%+v err=%v", dryRun, err)
	}
	for _, unchanged := range []string{
		filepath.Join(root, "sessions"),
		filepath.Join(root, "loaders"),
		filepath.Join(root, databaseName),
	} {
		if _, err := os.Stat(unchanged); err != nil {
			t.Fatalf("dry run changed %s: %v", unchanged, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, inPlaceBackupName)); !os.IsNotExist(err) {
		t.Fatalf("dry run created backup: %v", err)
	}

	report, err := Run(context.Background(), Options{Source: root, Target: root})
	if err != nil || report.Stage != "complete" || report.TargetVersion != 7 || !report.InPlace || report.CopiedBytes != 0 {
		t.Fatalf("in-place migration report=%+v err=%v", report, err)
	}
	nativeState := filepath.Join(root, "sandboxes", sandboxID, "state.img")
	nativeStateInfo, err := os.Stat(nativeState)
	if err != nil || !os.SameFile(legacyStateInfo, nativeStateInfo) {
		t.Fatalf("sandbox state was copied instead of renamed: info=%v err=%v", nativeStateInfo, err)
	}
	nativeWorkspaceLink := filepath.Join(root, "sandboxes", sandboxID, "workspace", "state-link")
	nativeWorkspaceLinkInfo, err := os.Lstat(nativeWorkspaceLink)
	if err != nil || !os.SameFile(legacyWorkspaceLinkInfo, nativeWorkspaceLinkInfo) {
		t.Fatalf("sandbox symlink was not renamed in place: info=%v err=%v", nativeWorkspaceLinkInfo, err)
	}
	if linkTarget, err := os.Readlink(nativeWorkspaceLink); err != nil || linkTarget != filepath.Join("..", "state.img") {
		t.Fatalf("sandbox symlink target=%q err=%v", linkTarget, err)
	}
	nativeCrossLink := filepath.Join(root, "sandboxes", sandboxID, "workspace", "scheduler-result")
	nativeCrossTarget := filepath.Join("..", "..", "..", "schedulers", "scheduler-1", "runs", "run-1", "result.json")
	if linkTarget, err := os.Readlink(nativeCrossLink); err != nil || linkTarget != nativeCrossTarget {
		t.Fatalf("cross-root symlink target=%q err=%v, want %q", linkTarget, err, nativeCrossTarget)
	}
	nativeAbsoluteLink := filepath.Join(root, "sandboxes", sandboxID, "workspace", "absolute-state")
	if linkTarget, err := os.Readlink(nativeAbsoluteLink); err != nil || linkTarget != nativeState {
		t.Fatalf("absolute symlink target=%q err=%v, want %q", linkTarget, err, nativeState)
	}
	nativeArtifact := filepath.Join(root, "schedulers", "scheduler-1", "runs", "run-1", "result.json")
	nativeArtifactInfo, err := os.Stat(nativeArtifact)
	if err != nil || !os.SameFile(legacyArtifactInfo, nativeArtifactInfo) {
		t.Fatalf("scheduler artifact was copied instead of renamed: info=%v err=%v", nativeArtifactInfo, err)
	}
	for _, removed := range []string{filepath.Join(root, "sessions"), filepath.Join(root, "loaders")} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("legacy directory remains at %s: %v", removed, err)
		}
	}

	metadata := readMigrationJSON(t, filepath.Join(root, "sandboxes", sandboxID, "metadata.json"))
	summary := metadata["summary"].(map[string]any)
	if summary["workspace_path"] != filepath.Join(root, "sandboxes", sandboxID, "workspace") {
		t.Fatalf("migrated metadata=%#v", metadata)
	}
	backupRoot := filepath.Join(root, inPlaceBackupName)
	for path, wantTarget := range map[string]string{
		filepath.Join(backupRoot, inPlaceSymlinkBackupRoot, "sessions", sandboxID, "workspace", "scheduler-result"): legacyCrossTarget,
		filepath.Join(backupRoot, inPlaceSymlinkBackupRoot, "sessions", sandboxID, "workspace", "absolute-state"):   largeState,
	} {
		if linkTarget, err := os.Readlink(path); err != nil || linkTarget != wantTarget {
			t.Fatalf("symlink backup %s target=%q err=%v, want %q", path, linkTarget, err, wantTarget)
		}
	}
	backupDB, err := openReadOnly(filepath.Join(backupRoot, inPlaceBackupDatabase))
	if err != nil {
		t.Fatal(err)
	}
	backupVersion, versionErr := inspectVersion(context.Background(), backupDB)
	closeErr := backupDB.Close()
	if versionErr != nil || closeErr != nil || backupVersion != 4 {
		t.Fatalf("database backup version=%d versionErr=%v closeErr=%v", backupVersion, versionErr, closeErr)
	}
	jsonBackup, err := os.ReadFile(filepath.Join(backupRoot, inPlaceJSONBackupRoot, "sandboxes", sandboxID, "metadata.json"))
	if err != nil || !strings.Contains(string(jsonBackup), string(filepath.Separator)+"sessions"+string(filepath.Separator)) {
		t.Fatalf("metadata backup=%q err=%v", jsonBackup, err)
	}
	if _, err := os.Stat(filepath.Join(backupRoot, inPlaceOriginalDB)); err != nil {
		t.Fatalf("raw original database was not retained: %v", err)
	}

	summary["vm_status"] = "RUNNING"
	writeMigrationJSON(t, filepath.Join(root, "sandboxes", sandboxID, "metadata.json"), metadata)
	repeated, err := Run(context.Background(), Options{Source: root, Target: root})
	if err != nil || repeated.Stage != "complete" || repeated.TargetVersion != 7 {
		t.Fatalf("repeated in-place migration report=%+v err=%v", repeated, err)
	}
}

func TestIntegrationRunPreservesPartitionedLifecyclePathsInPlace(t *testing.T) {
	root, _ := createManagedMigrationRoots(t, false)
	directoryName := strings.Repeat("b", 64)
	sandboxID := "sha256:" + directoryName
	sandboxPath := filepath.Join(root, "sandboxes", "2026", "07", "26", directoryName)
	writeMigrationJSON(t, filepath.Join(sandboxPath, "metadata.json"), map[string]any{
		"summary": map[string]any{"id": sandboxID, "vm_status": "STOPPED"},
	})
	writeMigrationJSON(t, filepath.Join(root, "sandboxes", ".lifecycle", sandboxID+".json"), map[string]any{
		"version": 1, "sandbox_id": sandboxID, "sandbox_path": sandboxPath,
		"owned_resources": []any{
			map[string]any{"kind": "sandbox-directory", "path": sandboxPath},
		},
	})

	dryRun, err := Run(context.Background(), Options{Source: root, Target: root, DryRun: true})
	if err != nil || dryRun.Stage != "eligible" {
		t.Fatalf("partitioned in-place dry-run report=%+v err=%v", dryRun, err)
	}
	report, err := Run(context.Background(), Options{Source: root, Target: root})
	if err != nil || report.Stage != "complete" {
		t.Fatalf("partitioned in-place report=%+v err=%v", report, err)
	}
	lifecycle := readMigrationJSON(t, filepath.Join(root, "sandboxes", ".lifecycle", sandboxID+".json"))
	resources := lifecycle["owned_resources"].([]any)
	if lifecycle["sandbox_path"] != sandboxPath || resources[0].(map[string]any)["path"] != sandboxPath {
		t.Fatalf("partitioned in-place lifecycle=%#v", lifecycle)
	}
	repeated, err := Run(context.Background(), Options{Source: root, Target: root})
	if err != nil || repeated.Stage != "complete" {
		t.Fatalf("partitioned repeated in-place report=%+v err=%v", repeated, err)
	}
}

func TestIntegrationRunMigratesUnversionedAndMixedStandaloneDataInPlace(t *testing.T) {
	for _, unversioned := range []bool{false, true} {
		name := "versioned"
		if unversioned {
			name = "unversioned"
		}
		t.Run(name, func(t *testing.T) {
			root, _ := createManagedMigrationRoots(t, unversioned)
			db, err := sql.Open("sqlite", filepath.Join(root, databaseName))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO agent_definition(id,name,enabled,provider,model,created_at,updated_at) VALUES('standalone-agent','Standalone Agent',1,'codex','legacy-model',1,1)`); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO loader(id,name,runtime,script,agent_id,enabled,created_at,updated_at) VALUES('standalone-loader','Standalone Scheduler','scheduler','function main() {}','standalone-agent',1,1,1)`); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			sandboxID := "standalone-sandbox"
			writeMigrationJSON(t, filepath.Join(root, "sessions", sandboxID, "metadata.json"), map[string]any{
				"summary": map[string]any{
					"id": sandboxID, "vm_status": "STOPPED",
					"tags": []any{
						map[string]any{"name": "source", "value": "agent"},
						map[string]any{"name": "agent_id", "value": "standalone-agent"},
						map[string]any{"name": "agent_name", "value": "Standalone Agent"},
					},
				},
			})
			legacyArtifact := filepath.Join(root, "loaders", "standalone-loader", "runs", "standalone-run", "result.json")
			if err := os.MkdirAll(filepath.Dir(legacyArtifact), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacyArtifact, []byte("standalone"), 0o600); err != nil {
				t.Fatal(err)
			}
			legacyInfo, err := os.Stat(legacyArtifact)
			if err != nil {
				t.Fatal(err)
			}

			report, err := Run(context.Background(), Options{Source: root, Target: root})
			if err != nil || report.Stage != "complete" || report.TargetVersion != 7 {
				t.Fatalf("mixed in-place migration report=%+v err=%v", report, err)
			}
			targetDB, err := openReadOnly(filepath.Join(root, databaseName))
			if err != nil {
				t.Fatal(err)
			}
			var standaloneAgents int
			if err := targetDB.QueryRow(`SELECT COUNT(*) FROM project_agent`).Scan(&standaloneAgents); err != nil {
				_ = targetDB.Close()
				t.Fatal(err)
			}
			if err := targetDB.Close(); err != nil {
				t.Fatal(err)
			}
			if standaloneAgents < 2 {
				t.Fatal("standalone agent was not converted into the default project")
			}
			state, err := readJournal(root)
			if err != nil {
				t.Fatal(err)
			}
			schedulerID := state.SchedulerIDs["standalone-loader"]
			if schedulerID == "" {
				t.Fatalf("standalone scheduler mapping=%v", state.SchedulerIDs)
			}
			agentIdentity, ok := state.AgentIDs["standalone-agent"]
			if !ok || agentIdentity.NativeID == "" || agentIdentity.ProjectID == "" || agentIdentity.AgentName == "" {
				t.Fatalf("standalone agent mapping=%v", state.AgentIDs)
			}
			metadata := readMigrationJSON(t, filepath.Join(root, "sandboxes", sandboxID, "metadata.json"))
			tags := migrationTagValues(metadata["summary"].(map[string]any)["tags"])
			if tags["agent_id"] != agentIdentity.NativeID || tags["agent_name"] != agentIdentity.AgentName || tags["agent"] != agentIdentity.AgentName ||
				tags["project"] != agentIdentity.ProjectID || tags["project_id"] != agentIdentity.ProjectID {
				t.Fatalf("standalone sandbox tags=%v mapping=%+v", tags, agentIdentity)
			}
			nativeDatabase, err := sqlite.Open(filepath.Join(root, databaseName), 0)
			if err != nil {
				t.Fatal(err)
			}
			definition, lookupErr := configstore.FromDB(nativeDatabase.DB()).GetAgentDefinition(context.Background(), tags["agent_id"])
			closeErr := nativeDatabase.Close()
			if lookupErr != nil || closeErr != nil || definition.ID != agentIdentity.NativeID || definition.ProjectID != agentIdentity.ProjectID {
				t.Fatalf("resumed standalone sandbox agent=%+v lookupErr=%v closeErr=%v", definition, lookupErr, closeErr)
			}
			nativeInfo, err := os.Stat(filepath.Join(root, "schedulers", schedulerID, "runs", "standalone-run", "result.json"))
			if err != nil || !os.SameFile(legacyInfo, nativeInfo) {
				t.Fatalf("standalone artifact was copied instead of renamed: info=%v err=%v", nativeInfo, err)
			}
		})
	}
}

func TestRunRevalidatesStoppedSandboxesBeforeResumingInPlace(t *testing.T) {
	ctx := context.Background()
	root, _ := createManagedMigrationRoots(t, false)
	metadataPath := filepath.Join(root, "sessions", "sandbox-1", "metadata.json")
	writeMigrationJSON(t, metadataPath, map[string]any{
		"summary": map[string]any{"id": "sandbox-1", "vm_status": "STOPPED"},
	})
	snapshot, err := openSourceDatabaseSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := fingerprintRootFromDatabase(root, snapshot.path)
	if err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, inPlaceBackupName), 0o700); err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	state := journal{
		Mode: inPlaceJournalMode, SourceFingerprint: fingerprint, SourceVersion: 4,
		RuntimeRoot: root, Stage: "database",
	}
	if err := writeJournal(root, state); err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	if err := prepareInPlaceDatabases(ctx, root, root, snapshot.db, &state); err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	writeMigrationJSON(t, metadataPath, map[string]any{
		"summary": map[string]any{"id": "sandbox-1", "vm_status": "RUNNING"},
	})

	report, err := Run(ctx, Options{Source: root, Target: root})
	if err == nil || report.Stage != "validate" || !strings.Contains(report.Error, "stop all sandboxes") {
		t.Fatalf("running sandbox resume report=%+v err=%v", report, err)
	}
	resumedState, readErr := readJournal(root)
	if readErr != nil || resumedState.Stage != inPlaceStagePrepared {
		t.Fatalf("journal advanced after rejected resume: state=%+v err=%v", resumedState, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "sessions", "sandbox-1")); statErr != nil {
		t.Fatalf("legacy sandbox layout changed after rejected resume: %v", statErr)
	}
}

func TestIntegrationRunInPlaceDryRunRejectsLayoutConflictWithoutMutation(t *testing.T) {
	root, _ := createManagedMigrationRoots(t, false)
	if err := os.MkdirAll(filepath.Join(root, "schedulers", "scheduler-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), Options{Source: root, Target: root, DryRun: true})
	if err == nil || report.Stage != "validate" || !strings.Contains(report.Error, "both map") {
		t.Fatalf("conflicting in-place dry run report=%+v err=%v", report, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "loaders", "loader-1")); statErr != nil {
		t.Fatalf("dry run mutated legacy layout: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, inPlaceBackupName)); !os.IsNotExist(statErr) {
		t.Fatalf("failed dry run created backup: %v", statErr)
	}
}

func TestIntegrationRunResumesInPlaceAfterOriginalDatabaseMove(t *testing.T) {
	ctx := context.Background()
	root, _ := createManagedMigrationRoots(t, false)
	snapshot, err := openSourceDatabaseSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := fingerprintRootFromDatabase(root, snapshot.path)
	if err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, inPlaceBackupName), 0o700); err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	state := journal{
		Mode: inPlaceJournalMode, SourceFingerprint: fingerprint, SourceVersion: 4,
		RuntimeRoot: root, Stage: "database",
	}
	if err := writeJournal(root, state); err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	if err := prepareInPlaceDatabases(ctx, root, root, snapshot.db, &state); err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if err := applyInPlaceLayout(root, root, state.SchedulerIDs, state.AgentIDs); err != nil {
		t.Fatal(err)
	}
	state.Stage = inPlaceStageSwitch
	if err := writeJournal(root, state); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, databaseName), filepath.Join(root, inPlaceBackupName, inPlaceOriginalDB)); err != nil {
		t.Fatal(err)
	}

	report, err := Run(ctx, Options{Source: root, Target: root})
	if err != nil || report.Stage != "complete" || report.TargetVersion != 7 {
		t.Fatalf("resumed switch report=%+v err=%v", report, err)
	}
	if _, err := os.Stat(filepath.Join(root, databaseName)); err != nil {
		t.Fatalf("converted database was not activated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "schedulers", "scheduler-1", "runs", "run-1", "result.json")); err != nil {
		t.Fatalf("renamed scheduler artifact is unavailable: %v", err)
	}
}

func TestInPlaceReportJSONIncludesNonCopyingMode(t *testing.T) {
	data, err := json.Marshal(Report{InPlace: true, CheckedFiles: 2, CheckedBytes: 9})
	if err != nil || !strings.Contains(string(data), `"in_place":true`) || !strings.Contains(string(data), `"checked_bytes":9`) || strings.Contains(string(data), "copied_bytes") {
		t.Fatalf("in-place report JSON=%s err=%v", data, err)
	}
}

func TestApplyInPlaceLayoutRejectsUnsafeSchedulerDirectoryIdentity(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "escape")
	err := applyInPlaceLayout(root, root, map[string]string{"../escape": "scheduler-1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "safe directory name") {
		t.Fatalf("unsafe identity error=%v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("unsafe identity touched path outside root: %v", err)
	}
}
