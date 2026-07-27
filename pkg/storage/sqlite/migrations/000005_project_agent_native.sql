CREATE TEMP TABLE migration_000005_guard(reason TEXT);
CREATE TEMP TRIGGER migration_000005_abort
BEFORE INSERT ON migration_000005_guard
BEGIN
    SELECT RAISE(ABORT, 'project agent migration requires agent-compose-migrate');
END;

INSERT INTO migration_000005_guard
SELECT 'invalid project agent identity'
WHERE EXISTS (
    SELECT 1 FROM project_agent
    WHERE trim(id) = '' OR trim(managed_agent_id) = '' OR id <> managed_agent_id
);
INSERT INTO migration_000005_guard
SELECT 'duplicate project agent identity'
WHERE EXISTS (SELECT 1 FROM project_agent GROUP BY id HAVING COUNT(*) > 1);
INSERT INTO migration_000005_guard
SELECT 'standalone or multiply projected agent definition'
WHERE EXISTS (
    SELECT 1
    FROM agent_definition AS definition
    LEFT JOIN project_agent AS agent ON agent.managed_agent_id = definition.id
    GROUP BY definition.id
    HAVING COUNT(agent.id) <> 1
);
INSERT INTO migration_000005_guard
SELECT 'orphan project agent projection'
WHERE EXISTS (
    SELECT 1
    FROM project_agent AS agent
    LEFT JOIN agent_definition AS definition ON definition.id = agent.managed_agent_id
    WHERE definition.id IS NULL
       OR definition.managed_project_id <> agent.project_id
       OR definition.managed_agent_name <> agent.agent_name
);
INSERT INTO migration_000005_guard
SELECT 'project run has no exact project agent owner'
WHERE EXISTS (
    SELECT 1
    FROM project_run AS run
    LEFT JOIN project_agent AS agent
      ON agent.project_id = run.project_id
     AND agent.agent_name = run.agent_name
     AND agent.managed_agent_id = run.managed_agent_id
    WHERE trim(run.managed_agent_id) = '' OR agent.id IS NULL
);

DROP TRIGGER migration_000005_abort;
DROP TABLE migration_000005_guard;

DROP INDEX idx_agent_definition_deleted_enabled;
DROP INDEX idx_agent_definition_workspace;
DROP INDEX idx_agent_definition_managed_project;
DROP INDEX idx_project_agent_managed_agent;
DROP INDEX idx_project_agent_id;
DROP INDEX idx_project_scheduler_agent;
DROP INDEX idx_project_scheduler_managed_loader;
DROP INDEX idx_project_scheduler_id;
DROP INDEX idx_project_run_project_status;
DROP INDEX idx_project_run_agent;
DROP INDEX idx_project_run_scheduler;
DROP INDEX idx_project_run_sandbox;
DROP INDEX idx_project_run_event_sequence;

ALTER TABLE project_run_event RENAME TO project_run_event_v1;
ALTER TABLE project_scheduler RENAME TO project_scheduler_v1;
ALTER TABLE project_run RENAME TO project_run_v1;
ALTER TABLE project_agent RENAME TO project_agent_v1;

CREATE TABLE project_agent (
    id TEXT NOT NULL PRIMARY KEY CHECK(trim(id) <> ''),
    name TEXT NOT NULL DEFAULT '',
    short_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 0,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    image TEXT NOT NULL DEFAULT '',
    driver TEXT NOT NULL DEFAULT '',
    scheduler_enabled INTEGER NOT NULL DEFAULT 0,
    spec_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(project_id, agent_name),
    FOREIGN KEY(project_id) REFERENCES project(id) ON DELETE CASCADE
);
CREATE INDEX idx_project_agent_project ON project_agent(project_id, agent_name);
INSERT INTO project_agent(
    id, name, short_id, project_id, agent_name, revision, provider, model,
    image, driver, scheduler_enabled, spec_json, created_at, updated_at
)
SELECT id, name, short_id, project_id, agent_name, revision, provider, model,
       image, driver, scheduler_enabled, spec_json, created_at, updated_at
FROM project_agent_v1;

CREATE TABLE project_scheduler (
    id TEXT PRIMARY KEY,
    short_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL,
    scheduler_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    managed_loader_id TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    trigger_count INTEGER NOT NULL DEFAULT 0,
    spec_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(project_id, scheduler_id),
    FOREIGN KEY(project_id) REFERENCES project(id) ON DELETE CASCADE,
    FOREIGN KEY(project_id, agent_name) REFERENCES project_agent(project_id, agent_name) ON DELETE CASCADE
);
CREATE INDEX idx_project_scheduler_agent ON project_scheduler(project_id, agent_name);
CREATE UNIQUE INDEX idx_project_scheduler_managed_loader ON project_scheduler(managed_loader_id);
INSERT INTO project_scheduler(
    id, short_id, project_id, scheduler_id, agent_name, managed_loader_id,
    revision, enabled, trigger_count, spec_json, created_at, updated_at
)
SELECT id, short_id, project_id, scheduler_id, agent_name, managed_loader_id,
       revision, enabled, trigger_count, spec_json, created_at, updated_at
FROM project_scheduler_v1;

CREATE TABLE project_run (
    run_id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    project_name TEXT NOT NULL DEFAULT '',
    project_revision INTEGER NOT NULL DEFAULT 0,
    agent_name TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    scheduler_id TEXT NOT NULL DEFAULT '',
    scheduler_run_id TEXT,
    trigger_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    sandbox_id TEXT NOT NULL DEFAULT '',
    exit_code INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL DEFAULT '',
    output TEXT NOT NULL DEFAULT '',
    result_json TEXT NOT NULL DEFAULT '',
    logs_path TEXT NOT NULL DEFAULT '',
    artifacts_dir TEXT NOT NULL DEFAULT '',
    cleanup_error TEXT NOT NULL DEFAULT '',
    driver TEXT NOT NULL DEFAULT '',
    image_ref TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL DEFAULT 0,
    completed_at INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY(project_id) REFERENCES project(id) ON DELETE CASCADE,
    FOREIGN KEY(agent_id) REFERENCES project_agent(id) ON DELETE RESTRICT
);
CREATE INDEX idx_project_run_project_status ON project_run(project_id, status, created_at DESC);
CREATE INDEX idx_project_run_agent ON project_run(project_id, agent_name, created_at DESC);
CREATE INDEX idx_project_run_scheduler ON project_run(project_id, scheduler_id, trigger_id);
CREATE INDEX idx_project_run_scheduler_run ON project_run(scheduler_run_id);
CREATE INDEX idx_project_run_sandbox ON project_run(sandbox_id);
INSERT INTO project_run(
    run_id, project_id, project_name, project_revision, agent_name, agent_id,
    source, scheduler_id, scheduler_run_id, trigger_id, status, sandbox_id,
    exit_code, error, prompt, output, result_json, logs_path, artifacts_dir,
    cleanup_error, driver, image_ref, started_at, completed_at, duration_ms,
    created_at, updated_at
)
SELECT run_id, project_id, project_name, project_revision, agent_name,
       managed_agent_id, source, scheduler_id, NULL, trigger_id, status,
       sandbox_id, exit_code, error, prompt, output, result_json, logs_path,
       artifacts_dir, cleanup_error, driver, image_ref, started_at,
       completed_at, duration_ms, created_at, updated_at
FROM project_run_v1;

CREATE TABLE project_run_event (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    kind TEXT NOT NULL,
    text TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    success INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER NOT NULL DEFAULT 0,
    stop_reason TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    UNIQUE(run_id, seq),
    FOREIGN KEY(run_id) REFERENCES project_run(run_id) ON DELETE CASCADE
);
CREATE INDEX idx_project_run_event_sequence ON project_run_event(run_id, seq);
INSERT INTO project_run_event
SELECT id, run_id, seq, kind, text, agent, name, payload_json, success,
       exit_code, stop_reason, created_at
FROM project_run_event_v1;

DROP TABLE project_run_event_v1;
DROP TABLE project_run_v1;
DROP TABLE project_scheduler_v1;
DROP TABLE project_agent_v1;
DROP TABLE agent_definition;
