package sqlite

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"agent-compose/pkg/compose"
)

func TestUniqueProjectNameMigrationRenamesDuplicatesInStableOrder(t *testing.T) {
	ctx := context.Background()
	db := newMemoryDB(t)
	if err := MigrateThrough(ctx, db, 8); err != nil {
		t.Fatalf("MigrateThrough v8: %v", err)
	}

	for _, statement := range []string{
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at,removed_at) VALUES('canonical','demo',1,'canonical-hash',1,20,0)`,
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at,removed_at) VALUES('older-active','demo',1,'active-hash',2,10,0)`,
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at,removed_at) VALUES('newer-removed','demo',1,'removed-hash',3,100,99)`,
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at,removed_at) VALUES('reserved','demo-2',1,'reserved-hash',4,5,0)`,
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at,removed_at) VALUES('huge-suffix','demo-999999999999999999999999999999999999',1,'huge-suffix-hash',5,5,0)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("insert migration fixture project: %v", err)
		}
	}
	for _, item := range []struct {
		id   string
		hash string
	}{
		{id: "canonical", hash: "canonical-hash"},
		{id: "older-active", hash: "active-hash"},
		{id: "newer-removed", hash: "removed-hash"},
		{id: "reserved", hash: "reserved-hash"},
		{id: "huge-suffix", hash: "huge-suffix-hash"},
	} {
		specJSON := `{"name":"demo"}`
		if item.id == "older-active" {
			specJSON = `{"name":"demo","agents":[{"name":"worker","scheduler":{}}],"volumes":[{"key":"cache"}]}`
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES(?,1,?,?,1)`, item.id, item.hash, specJSON); err != nil {
			t.Fatalf("insert migration fixture revision %s: %v", item.id, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO project_agent(id,name,short_id,project_id,agent_name,revision,spec_json,created_at,updated_at) VALUES('older-agent','worker','older','older-active','worker',1,'{}',1,1)`); err != nil {
		t.Fatalf("insert migration fixture agent: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO project_scheduler(id,short_id,project_id,scheduler_id,agent_name,revision,spec_json,created_at,updated_at) VALUES('older-scheduler','sched','older-active','older-scheduler','worker',1,'{}',1,1)`); err != nil {
		t.Fatalf("insert migration fixture scheduler: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO volumes(id,name,project_id) VALUES('older-cache','demo_cache','older-active')`); err != nil {
		t.Fatalf("insert migration fixture volume: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO project_volumes(project_id,volume_key,volume_id) VALUES('older-active','cache','older-cache')`); err != nil {
		t.Fatalf("insert migration fixture volume link: %v", err)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate v9: %v", err)
	}
	for id, wantName := range map[string]string{
		"canonical":     "demo",
		"older-active":  "demo-3",
		"newer-removed": "demo-4",
		"reserved":      "demo-2",
		"huge-suffix":   "demo-999999999999999999999999999999999999",
	} {
		var name string
		if err := db.QueryRowContext(ctx, `SELECT name FROM project WHERE id = ?`, id).Scan(&name); err != nil {
			t.Fatalf("query migrated project %s: %v", id, err)
		}
		if name != wantName {
			t.Errorf("project %s name = %q, want %q", id, name, wantName)
		}
	}
	var revision int64
	var projectHash string
	if err := db.QueryRowContext(ctx, `SELECT current_revision,spec_hash FROM project WHERE id='older-active'`).Scan(&revision, &projectHash); err != nil {
		t.Fatalf("query renamed project revision: %v", err)
	}
	if revision != 2 {
		t.Fatalf("renamed project revision = %d, want migration revision 2", revision)
	}
	var revisionHash, revisionJSON string
	if err := db.QueryRowContext(ctx, `SELECT spec_hash,spec_json FROM project_revision WHERE project_id='older-active' AND revision=2`).Scan(&revisionHash, &revisionJSON); err != nil {
		t.Fatalf("query migrated project revision: %v", err)
	}
	migratedSpec, err := compose.ParseCanonicalJSON([]byte(revisionJSON))
	if err != nil {
		t.Fatalf("parse migrated project revision: %v", err)
	}
	canonicalHash, err := migratedSpec.Hash()
	if err != nil {
		t.Fatalf("hash migrated project revision: %v", err)
	}
	if migratedSpec.Name != "demo-3" || migratedSpec.Volumes["cache"].Name != "demo_cache" || len(migratedSpec.Agents) != 1 ||
		migratedSpec.Agents[0].Scheduler == nil || migratedSpec.Agents[0].Scheduler.SandboxPolicy != "new" ||
		migratedSpec.Agents[0].Scheduler.ConcurrencyPolicy != "skip" {
		t.Fatalf("migrated spec = %#v, want renamed project with materialized demo_cache", migratedSpec)
	}
	if projectHash != revisionHash || revisionHash != canonicalHash {
		t.Fatalf("project/revision/canonical hashes = %q/%q/%q", projectHash, revisionHash, canonicalHash)
	}
	var originalJSON string
	if err := db.QueryRowContext(ctx, `SELECT spec_json FROM project_revision WHERE project_id='older-active' AND revision=1`).Scan(&originalJSON); err != nil {
		t.Fatalf("query original project revision: %v", err)
	}
	if originalJSON != `{"name":"demo","agents":[{"name":"worker","scheduler":{}}],"volumes":[{"key":"cache"}]}` {
		t.Fatalf("original project revision changed to %s", originalJSON)
	}
	for table := range map[string]struct{}{"project_agent": {}, "project_scheduler": {}} {
		if err := db.QueryRowContext(ctx, `SELECT revision FROM `+table+` WHERE project_id='older-active'`).Scan(&revision); err != nil {
			t.Fatalf("query %s revision: %v", table, err)
		}
		if revision != 2 {
			t.Errorf("%s revision = %d, want migration revision 2", table, revision)
		}
	}
	var volumeID string
	if err := db.QueryRowContext(ctx, `SELECT volume_id FROM project_volumes WHERE project_id='older-active' AND volume_key='cache'`).Scan(&volumeID); err != nil {
		t.Fatalf("query preserved project volume link: %v", err)
	}
	if volumeID != "older-cache" {
		t.Fatalf("preserved project volume id = %q, want older-cache", volumeID)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO project(id,name,created_at,updated_at) VALUES('duplicate','demo',1,1)`); err == nil {
		t.Fatal("unique project name index accepted duplicate name")
	}
	if _, err := db.ExecContext(ctx, `UPDATE project SET name='renamed' WHERE id='canonical'`); err == nil {
		t.Fatal("project name immutability trigger accepted a rename")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("run foreign key check: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatal("foreign key check reported a violation")
	}
}

func TestUniqueProjectNameMigrationPreservesProjectListDuplicateData(t *testing.T) {
	ctx := context.Background()
	db := newMemoryDB(t)
	if err := MigrateThrough(ctx, db, 8); err != nil {
		t.Fatalf("MigrateThrough v8: %v", err)
	}

	type projectFixture struct {
		id, name, sourcePath string
		updatedAt            int64
	}
	const relatedProjectID = "project-facade-file"
	fixtures := []projectFixture{
		{id: "b96a99621d61-legacy", name: "legacy-v1-default", updatedAt: 10},
		{id: "884486024b96-cli", name: "cli-smoke", sourcePath: "/data/projects/cli-smoke/agent-compose.yml", updatedAt: 60},
		{id: "project-facade-pilot", name: "facade-smoke-pilot-patched-20260701", sourcePath: "/tmp/facade-smoke-pilot-patched/agent-compose.yml", updatedAt: 50},
		{id: "project-facade-workspace", name: "facade-smoke-20260701", sourcePath: "/tmp/facade-smoke-workspace/agent-compose.yml", updatedAt: 40},
		{id: "project-facade-file", name: "facade-smoke-20260701", sourcePath: "/tmp/agent-compose-facade-smoke.yml", updatedAt: 30},
		{id: "project-cli-source", name: "cli-smoke", sourcePath: "/data/projects/cli-smoke/agent-compose.yml", updatedAt: 20},
	}
	for _, item := range fixtures {
		hash := "hash-" + item.id
		specJSON := fmt.Sprintf(`{"name":%q}`, item.name)
		if item.id == relatedProjectID {
			specJSON = fmt.Sprintf(`{"name":%q,"volumes":[{"key":"cache"}]}`, item.name)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO project(
			id,name,source_path,current_revision,spec_hash,created_at,updated_at,removed_at
		) VALUES(?,?,?,1,?,1,?,0)`, item.id, item.name, item.sourcePath, hash, item.updatedAt); err != nil {
			t.Fatalf("insert project %s: %v", item.id, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO project_revision(
			project_id,revision,spec_hash,spec_json,created_at
		) VALUES(?,1,?,?,1)`, item.id, hash, specJSON); err != nil {
			t.Fatalf("insert revision for %s: %v", item.id, err)
		}
	}

	for _, statement := range []string{
		`INSERT INTO project_agent(id,name,project_id,agent_name,revision,spec_json,created_at,updated_at)
		 VALUES('facade-agent','worker','project-facade-file','worker',1,'{}',1,1)`,
		`INSERT INTO project_scheduler(id,project_id,scheduler_id,agent_name,revision,spec_json,created_at,updated_at)
		 VALUES('facade-scheduler','project-facade-file','facade-scheduler','worker',1,'{}',1,1)`,
		`INSERT INTO scheduler_run(scheduler_id,run_id,status,started_at)
		 VALUES('facade-scheduler','facade-scheduler-run','succeeded',1)`,
		`INSERT INTO project_run(run_id,project_id,project_name,project_revision,agent_name,agent_id,status,created_at,updated_at)
		 VALUES('facade-project-run','project-facade-file','facade-smoke-20260701',1,'worker','facade-agent','succeeded',1,1)`,
		`INSERT INTO sandboxes(id,project_id,project_id_search)
		 VALUES('facade-sandbox','project-facade-file','project-facade-file')`,
		`INSERT INTO volumes(id,name,project_id) VALUES('facade-volume','facade-volume','project-facade-file')`,
		`INSERT INTO project_volumes(project_id,volume_key,volume_id)
		 VALUES('project-facade-file','cache','facade-volume')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("insert related project data: %v", err)
		}
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate v9: %v", err)
	}
	wantNames := map[string]string{
		"b96a99621d61-legacy":      "legacy-v1-default",
		"884486024b96-cli":         "cli-smoke",
		"project-facade-pilot":     "facade-smoke-pilot-patched-20260701",
		"project-facade-workspace": "facade-smoke-20260701",
		"project-facade-file":      "facade-smoke-20260701-2",
		"project-cli-source":       "cli-smoke-2",
	}
	for _, item := range fixtures {
		var name, sourcePath string
		if err := db.QueryRowContext(ctx, `SELECT name,source_path FROM project WHERE id=?`, item.id).Scan(&name, &sourcePath); err != nil {
			t.Fatalf("query project %s: %v", item.id, err)
		}
		if name != wantNames[item.id] || sourcePath != item.sourcePath {
			t.Errorf("project %s = name %q source %q, want %q and %q", item.id, name, sourcePath, wantNames[item.id], item.sourcePath)
		}
	}

	relationQueries := map[string]string{
		"revision":       `SELECT project_id FROM project_revision WHERE spec_hash='hash-project-facade-file'`,
		"agent":          `SELECT project_id FROM project_agent WHERE id='facade-agent'`,
		"scheduler":      `SELECT project_id FROM project_scheduler WHERE id='facade-scheduler'`,
		"scheduler run":  `SELECT project_scheduler.project_id FROM scheduler_run JOIN project_scheduler ON project_scheduler.id=scheduler_run.scheduler_id WHERE scheduler_run.run_id='facade-scheduler-run'`,
		"project run":    `SELECT project_id FROM project_run WHERE run_id='facade-project-run'`,
		"sandbox":        `SELECT project_id FROM sandboxes WHERE id='facade-sandbox'`,
		"volume":         `SELECT project_id FROM volumes WHERE id='facade-volume'`,
		"project volume": `SELECT project_id FROM project_volumes WHERE volume_id='facade-volume'`,
	}
	for relation, query := range relationQueries {
		var projectID string
		if err := db.QueryRowContext(ctx, query).Scan(&projectID); err != nil {
			t.Fatalf("query %s owner: %v", relation, err)
		}
		if projectID != relatedProjectID {
			t.Errorf("%s project_id = %q, want %q", relation, projectID, relatedProjectID)
		}
	}
	var currentRevision int64
	var currentHash string
	if err := db.QueryRowContext(ctx, `SELECT current_revision,spec_hash FROM project WHERE id=?`, relatedProjectID).Scan(&currentRevision, &currentHash); err != nil {
		t.Fatalf("query related project current revision: %v", err)
	}
	if currentRevision != 2 {
		t.Fatalf("related project current revision = %d, want 2", currentRevision)
	}
	var migratedHash, migratedJSON string
	if err := db.QueryRowContext(ctx, `SELECT spec_hash,spec_json FROM project_revision WHERE project_id=? AND revision=2`, relatedProjectID).Scan(&migratedHash, &migratedJSON); err != nil {
		t.Fatalf("query related project migrated revision: %v", err)
	}
	migratedSpec, err := compose.ParseCanonicalJSON([]byte(migratedJSON))
	if err != nil {
		t.Fatalf("parse related project migrated revision: %v", err)
	}
	canonicalHash, err := migratedSpec.Hash()
	if err != nil {
		t.Fatalf("hash related project migrated revision: %v", err)
	}
	if migratedSpec.Name != "facade-smoke-20260701-2" || migratedSpec.Volumes["cache"].Name != "facade-volume" {
		t.Fatalf("related project migrated spec = %#v", migratedSpec)
	}
	if currentHash != migratedHash || migratedHash != canonicalHash {
		t.Fatalf("related project hashes = %q/%q/%q", currentHash, migratedHash, canonicalHash)
	}
	for _, table := range []string{"project_agent", "project_scheduler"} {
		var artifactRevision int64
		if err := db.QueryRowContext(ctx, `SELECT revision FROM `+table+` WHERE project_id=?`, relatedProjectID).Scan(&artifactRevision); err != nil {
			t.Fatalf("query related %s revision: %v", table, err)
		}
		if artifactRevision != currentRevision {
			t.Errorf("related %s revision = %d, want %d", table, artifactRevision, currentRevision)
		}
	}
	var projectCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project`).Scan(&projectCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if projectCount != len(fixtures) {
		t.Fatalf("project count = %d, want all %d projects preserved", projectCount, len(fixtures))
	}
}

func TestUniqueProjectNameMigrationRollsBackMalformedCurrentRevision(t *testing.T) {
	ctx := context.Background()
	db := newMemoryDB(t)
	if err := MigrateThrough(ctx, db, 8); err != nil {
		t.Fatalf("MigrateThrough v8: %v", err)
	}
	for _, statement := range []string{
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at) VALUES('canonical','demo',1,'canonical-hash',1,30)`,
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at) VALUES('valid','demo',1,'valid-hash',1,20)`,
		`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at) VALUES('malformed','demo',1,'malformed-hash',1,10)`,
		`INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES('canonical',1,'canonical-hash','{"name":"demo"}',1)`,
		`INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES('valid',1,'valid-hash','{"name":"demo"}',1)`,
		`INSERT INTO project_revision(project_id,revision,spec_hash,spec_json,created_at) VALUES('malformed',1,'malformed-hash','{"name":',1)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("insert malformed migration fixture: %v", err)
		}
	}

	err := Migrate(ctx, db)
	if err == nil || !strings.Contains(err.Error(), "decode current revision malformed/1 before rename") {
		t.Fatalf("Migrate error = %v, want malformed current revision failure", err)
	}
	var name string
	var currentRevision, revisionCount int64
	if err := db.QueryRowContext(ctx, `SELECT name,current_revision FROM project WHERE id='valid'`).Scan(&name, &currentRevision); err != nil {
		t.Fatalf("query valid project after rollback: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_revision WHERE project_id='valid'`).Scan(&revisionCount); err != nil {
		t.Fatalf("count valid project revisions after rollback: %v", err)
	}
	if name != "demo" || currentRevision != 1 || revisionCount != 1 {
		t.Fatalf("valid project after rollback = name %q revision %d count %d", name, currentRevision, revisionCount)
	}
	var migrationCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=9`).Scan(&migrationCount); err != nil {
		t.Fatalf("query v8 history after rollback: %v", err)
	}
	if migrationCount != 0 {
		t.Fatalf("v8 migration history count after rollback = %d, want 0", migrationCount)
	}
}
