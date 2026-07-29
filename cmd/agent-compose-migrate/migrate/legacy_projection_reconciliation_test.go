package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-compose/pkg/identity"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage/sqlite"
)

func TestRunReconcilesExistingLegacyProjectProjections(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := writeStoppedAliasSandbox(source, "legacy-duplicate"); err != nil {
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
	if _, err := db.Exec(`CREATE TABLE event_session_link(
		event_id TEXT NOT NULL, session_id TEXT NOT NULL, relation TEXT NOT NULL,
		loader_id TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '',
		trigger_id TEXT NOT NULL DEFAULT '', loader_event_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		PRIMARY KEY(event_id,session_id,relation,run_id)
	)`); err != nil {
		t.Fatalf("create legacy event session links: %v", err)
	}

	projectID := identity.NewID(identity.ResourceProject, legacyDefaultProjectName, "")
	existingAgentID, err := domain.StableProjectAgentID(projectID, "duplicate")
	if err != nil {
		t.Fatal(err)
	}
	existingSchedulerID, err := domain.StableProjectSchedulerID(projectID, "duplicate", "")
	if err != nil {
		t.Fatal(err)
	}
	removedProjectID := "removed-project"
	removedAgentID := "removed-agent"
	statements := []string{
		`INSERT INTO project(id,name,source_path,current_revision,spec_hash,created_at,updated_at) VALUES(?,?,?,?,?,1,1)`,
		`INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES(?,1,'existing','{"name":"legacy-v1-default","agents":[{"name":"duplicate","provider":"codex"}]}',1)`,
		`INSERT INTO agent_definition(id,name,enabled,provider,managed_project_id,managed_project_revision,managed_agent_name,created_at,updated_at) VALUES(?, 'duplicate',1,'codex',?,1,'duplicate',1,1)`,
		`INSERT INTO project_agent(id,name,project_id,agent_name,managed_agent_id,revision,provider,scheduler_enabled,created_at,updated_at) VALUES('','duplicate',?,'duplicate',?,1,'codex',1,1,1)`,
		`INSERT INTO agent_definition(id,name,enabled,provider,created_at,updated_at) VALUES('legacy-duplicate','Duplicate',1,'codex',1,1)`,
		`INSERT INTO agent_definition(id,name,enabled,provider,created_at,updated_at) VALUES('new-standalone','New Worker',1,'codex',1,1)`,
		`INSERT INTO loader(id,name,script,agent_id,managed_project_id,managed_project_revision,managed_agent_name,managed_scheduler_id,enabled,created_at,updated_at) VALUES('existing-loader','Existing scheduler','run()','legacy-duplicate',?,1,'duplicate',?,1,1,1)`,
		`INSERT INTO project_scheduler(id,project_id,agent_name,managed_loader_id,scheduler_id,revision,enabled,created_at,updated_at) VALUES('',?,'duplicate','existing-loader',?,1,1,1,1)`,
		`INSERT INTO loader_run(loader_id,run_id,status,started_at,artifacts_dir) VALUES('existing-loader','existing-run','succeeded',1,?)`,
		`INSERT INTO project_run(run_id,project_id,project_name,project_revision,agent_name,managed_agent_id,status,created_at,updated_at) VALUES('existing-project-run',?,'legacy-v1-default',1,'duplicate','legacy-duplicate','succeeded',1,1)`,
		`INSERT INTO project(id,name,source_path,current_revision,spec_hash,created_at,updated_at,removed_at) VALUES(?,'removed','/removed',1,'removed',1,1,1)`,
		`INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES(?,1,'removed','{"name":"removed","agents":[{"name":"removed-agent","provider":"codex"}]}',1)`,
		`INSERT INTO agent_definition(id,name,enabled,provider,managed_project_id,managed_project_revision,managed_agent_name,created_at,updated_at) VALUES(?,'removed-agent',1,'codex',?,1,'removed-agent',1,1)`,
		`INSERT INTO project_agent(id,name,project_id,agent_name,managed_agent_id,revision,provider,scheduler_enabled,created_at,updated_at) VALUES(?,'removed-agent',?,'removed-agent',?,1,'codex',1,1,1)`,
		`INSERT INTO project_scheduler(id,project_id,agent_name,managed_loader_id,scheduler_id,revision,enabled,created_at,updated_at) VALUES('',?,'removed-agent','missing-loader','removed-scheduler',1,0,1,1)`,
		`INSERT INTO event_delivery(event_id,loader_id,trigger_id,status,created_at,updated_at) VALUES('orphan-event','missing-loader','trigger','failed',1,1)`,
		`INSERT INTO event_sandbox_link(event_id,sandbox_id,relation,loader_id,created_at) VALUES('orphan-event','legacy-sandbox','triggered','missing-loader',1)`,
		`INSERT INTO event_session_link(event_id,session_id,relation,loader_id,created_at) VALUES('orphan-event','legacy-sandbox','triggered','missing-loader',1)`,
	}
	arguments := [][]any{
		{projectID, legacyDefaultProjectName, "/legacy", 1, "existing"},
		{projectID},
		{existingAgentID, projectID},
		{projectID, existingAgentID},
		{}, {},
		{projectID, existingSchedulerID},
		{projectID, existingSchedulerID},
		{filepath.Join(source, "loaders", "existing-loader", "runs", "existing-run")},
		{projectID},
		{removedProjectID},
		{removedProjectID},
		{removedAgentID, removedProjectID},
		{removedAgentID, removedProjectID, removedAgentID},
		{removedProjectID},
		{}, {}, {},
	}
	for index, statement := range statements {
		if _, err := db.Exec(statement, arguments[index]...); err != nil {
			_ = db.Close()
			t.Fatalf("statement %d: %v", index, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := Run(context.Background(), Options{Source: source, Target: target, RuntimeRoot: "/data"})
	if err != nil {
		t.Fatalf("Run returned error: %v (%+v)", err, report)
	}
	if report.TargetVersion != currentSchemaVersion || report.Stage != "complete" {
		t.Fatalf("report = %+v", report)
	}
	for _, warning := range []string{
		"backfilled 1 project agent and 2 project scheduler legacy identities",
		"removed 1 retired project scheduler projections",
		"detached 1 orphan event delivery, 1 sandbox link, and 1 session link scheduler references",
	} {
		if !strings.Contains(strings.Join(report.Warnings, "\n"), warning) {
			t.Fatalf("warnings %v do not contain %q", report.Warnings, warning)
		}
	}

	targetDB, err := sql.Open("sqlite", filepath.Join(target, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = targetDB.Close() }()
	var agentCount int
	if err := targetDB.QueryRow(`SELECT COUNT(*) FROM project_agent WHERE project_id=?`, projectID).Scan(&agentCount); err != nil || agentCount != 2 {
		t.Fatalf("legacy project agent count=%d, err=%v", agentCount, err)
	}
	var loaderAgentID string
	if err := targetDB.QueryRow(`SELECT agent_name FROM project_scheduler WHERE id=?`, existingSchedulerID).Scan(&loaderAgentID); err != nil || loaderAgentID != "duplicate" {
		t.Fatalf("reused scheduler agent=%q, err=%v", loaderAgentID, err)
	}
	var runSchedulerID, artifactsDir string
	if err := targetDB.QueryRow(`SELECT scheduler_id,artifacts_dir FROM scheduler_run WHERE run_id='existing-run'`).Scan(&runSchedulerID, &artifactsDir); err != nil {
		t.Fatal(err)
	}
	if runSchedulerID != existingSchedulerID || artifactsDir != filepath.Join("/data", "schedulers", existingSchedulerID, "runs", "existing-run") {
		t.Fatalf("scheduler run identity/path=%q/%q", runSchedulerID, artifactsDir)
	}
	var projectRunAgentID string
	if err := targetDB.QueryRow(`SELECT agent_id FROM project_run WHERE run_id='existing-project-run'`).Scan(&projectRunAgentID); err != nil || projectRunAgentID != existingAgentID {
		t.Fatalf("project run agent ID=%q, err=%v", projectRunAgentID, err)
	}
	var specJSON string
	if err := targetDB.QueryRow(`SELECT revision.spec_json FROM project JOIN project_revision AS revision ON revision.project_id=project.id AND revision.revision=project.current_revision WHERE project.id=?`, projectID).Scan(&specJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(specJSON, `"name":"duplicate"`) || !strings.Contains(specJSON, `"name":"newworker"`) {
		t.Fatalf("merged project spec = %s", specJSON)
	}
	for _, table := range []string{"event_delivery", "event_sandbox_link"} {
		var schedulerID string
		if err := targetDB.QueryRow(`SELECT scheduler_id FROM ` + table + ` WHERE event_id='orphan-event'`).Scan(&schedulerID); err != nil || schedulerID != "" {
			t.Fatalf("%s scheduler_id=%q, err=%v", table, schedulerID, err)
		}
	}
	var eventSessionTable int
	if err := targetDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='event_session_link'`).Scan(&eventSessionTable); err != nil || eventSessionTable != 0 {
		t.Fatalf("event_session_link table count=%d, err=%v", eventSessionTable, err)
	}
	metadata := readMigrationJSON(t, filepath.Join(target, "sandboxes", "legacy-sandbox", "metadata.json"))
	tags := migrationTagValues(metadata["summary"].(map[string]any)["tags"])
	if tags["agent_id"] != existingAgentID || tags["agent_name"] != "duplicate" || tags["project_id"] != projectID {
		t.Fatalf("reused standalone sandbox tags=%v", tags)
	}
}

func TestPlanStandaloneAgentsRejectsDifferentExistingAgent(t *testing.T) {
	projectID := identity.NewID(identity.ResourceProject, legacyDefaultProjectName, "")
	existingID, err := domain.StableProjectAgentID(projectID, "worker")
	if err != nil {
		t.Fatal(err)
	}
	_, err = planStandaloneAgents(projectID,
		[]legacyAgentDefinition{{id: "standalone", name: "Worker", provider: "claude", envJSON: "[]", volumesJSON: "[]", configJSON: "{}", capsetIDs: "[]", skills: "[]"}},
		nil,
		map[string]legacyAgentDefinition{"worker": {id: existingID, name: "worker", provider: "codex", envJSON: "[]", volumesJSON: "[]", configJSON: "{}", capsetIDs: "[]", skills: "[]"}},
		1,
	)
	if err == nil || !strings.Contains(err.Error(), "conflicts with existing project agent worker") {
		t.Fatalf("plan error = %v", err)
	}
}

func TestRemoveRetiredOrphanSchedulerProjectionsRejectsActiveProject(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), databaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := sqlite.MigrateThrough(context.Background(), db, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO project(id,name,created_at,updated_at) VALUES('active-project','active',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO project_scheduler(id,project_id,agent_name,managed_loader_id,scheduler_id,created_at,updated_at)
		VALUES('active-scheduler','active-project','worker','missing-loader','active-scheduler',1,1)`); err != nil {
		t.Fatal(err)
	}
	warnings, err := removeRetiredOrphanSchedulerProjections(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "active project scheduler projections") || warnings != nil {
		t.Fatalf("warnings=%v, err=%v", warnings, err)
	}
}

func writeStoppedAliasSandbox(source, legacyAgentID string) error {
	path := filepath.Join(source, "sessions", "legacy-sandbox")
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	data := `{"summary":{"id":"legacy-sandbox","vm_status":"STOPPED","tags":[{"name":"agent_id","value":"` + legacyAgentID + `"}]}}`
	return os.WriteFile(filepath.Join(path, "metadata.json"), []byte(data), 0o600)
}
