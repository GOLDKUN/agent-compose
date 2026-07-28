package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agent-compose/pkg/compose"
	"agent-compose/pkg/storage/sqlite"
)

func TestRunNormalizesLegacyLocalWorkspaceProviders(t *testing.T) {
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
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at) VALUES('project-1','project',1,'legacy-hash',1000,1001)`,
		`INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES('project-1',1,'legacy-hash','{"name":"project","workspaces":[{"key":"source","provider":"local","path":"."}],"agents":[{"name":"worker","provider":"codex","workspace":{"provider":"local","path":"."}}]}',1000)`,
		`INSERT INTO agent_definition(id,name,enabled,provider,managed_project_id,managed_project_revision,managed_agent_name,created_at,updated_at) VALUES('agent-1','Worker',1,'codex','project-1',1,'worker',1000,1001)`,
		`INSERT INTO project_agent(id,name,project_id,agent_name,managed_agent_id,revision,provider,spec_json,created_at,updated_at) VALUES('agent-1','Worker','project-1','worker','agent-1',1,'codex','{"name":"worker","provider":"codex","workspace":{"provider":"local","path":"."}}',1000,1001)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed legacy local workspace fixture with %q: %v", statement, err)
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
	var projectHash, revisionHash, revisionJSON, agentJSON string
	if err := targetDB.QueryRow(`SELECT project.spec_hash,revision.spec_hash,revision.spec_json
		FROM project JOIN project_revision AS revision
		ON revision.project_id=project.id AND revision.revision=project.current_revision
		WHERE project.id='project-1'`).Scan(&projectHash, &revisionHash, &revisionJSON); err != nil {
		t.Fatal(err)
	}
	spec, err := compose.ParseCanonicalJSON([]byte(revisionJSON))
	if err != nil {
		t.Fatalf("parse migrated revision: %v", err)
	}
	if spec.Workspaces["source"].Provider != "file" || spec.Agents[0].Workspace.Provider != "file" {
		t.Fatalf("migrated revision workspaces = %#v / %#v", spec.Workspaces["source"], spec.Agents[0].Workspace)
	}
	wantHash, err := spec.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if projectHash != wantHash || revisionHash != wantHash {
		t.Fatalf("migrated hashes project=%q revision=%q want=%q", projectHash, revisionHash, wantHash)
	}
	if err := targetDB.QueryRow(`SELECT spec_json FROM project_agent WHERE id='agent-1'`).Scan(&agentJSON); err != nil {
		t.Fatal(err)
	}
	var agent struct {
		Workspace struct {
			Provider string `json:"provider"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(agentJSON), &agent); err != nil {
		t.Fatalf("parse migrated agent: %v", err)
	}
	if agent.Workspace.Provider != "file" {
		t.Fatalf("migrated agent workspace provider = %q", agent.Workspace.Provider)
	}
}
