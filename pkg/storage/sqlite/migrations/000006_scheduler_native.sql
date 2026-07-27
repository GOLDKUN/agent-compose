CREATE TEMP TABLE migration_000006_guard(reason TEXT);
CREATE TEMP TRIGGER migration_000006_abort
BEFORE INSERT ON migration_000006_guard
BEGIN
    SELECT RAISE(ABORT, 'scheduler migration requires agent-compose-migrate');
END;

INSERT INTO migration_000006_guard
SELECT 'invalid project scheduler identity'
WHERE EXISTS (
    SELECT 1 FROM project_scheduler
    WHERE trim(id) = '' OR trim(managed_loader_id) = ''
);
INSERT INTO migration_000006_guard
SELECT 'duplicate project scheduler identity'
WHERE EXISTS (SELECT 1 FROM project_scheduler GROUP BY id HAVING COUNT(*) > 1)
   OR EXISTS (SELECT 1 FROM project_scheduler GROUP BY managed_loader_id HAVING COUNT(*) > 1);
INSERT INTO migration_000006_guard
SELECT 'standalone or multiply projected loader'
WHERE EXISTS (
    SELECT 1
    FROM loader
    LEFT JOIN project_scheduler ON project_scheduler.managed_loader_id = loader.id
    GROUP BY loader.id
    HAVING COUNT(project_scheduler.id) <> 1
);
INSERT INTO migration_000006_guard
SELECT 'orphan project scheduler bridge'
WHERE EXISTS (
    SELECT 1
    FROM project_scheduler
    LEFT JOIN loader ON loader.id = project_scheduler.managed_loader_id
    WHERE loader.id IS NULL
       OR loader.managed_project_id <> project_scheduler.project_id
       OR loader.managed_project_revision <> project_scheduler.revision
       OR loader.managed_agent_name <> project_scheduler.agent_name
       OR loader.managed_scheduler_id <> project_scheduler.scheduler_id
);
INSERT INTO migration_000006_guard
SELECT 'orphan scheduler execution row'
WHERE EXISTS (SELECT 1 FROM loader_trigger AS item LEFT JOIN loader ON loader.id = item.loader_id WHERE loader.id IS NULL)
   OR EXISTS (SELECT 1 FROM loader_run AS item LEFT JOIN loader ON loader.id = item.loader_id WHERE loader.id IS NULL)
   OR EXISTS (SELECT 1 FROM loader_event AS item LEFT JOIN loader ON loader.id = item.loader_id WHERE loader.id IS NULL)
   OR EXISTS (SELECT 1 FROM loader_state AS item LEFT JOIN loader ON loader.id = item.loader_id WHERE loader.id IS NULL)
   OR EXISTS (SELECT 1 FROM loader_binding AS item LEFT JOIN loader ON loader.id = item.loader_id WHERE loader.id IS NULL);
INSERT INTO migration_000006_guard
SELECT 'duplicate scheduler run identity'
WHERE EXISTS (SELECT 1 FROM loader_run GROUP BY run_id HAVING COUNT(*) > 1);
INSERT INTO migration_000006_guard
SELECT 'orphan scheduler event run'
WHERE EXISTS (
    SELECT 1
    FROM loader_event AS event
    LEFT JOIN loader_run AS run
      ON run.loader_id = event.loader_id AND run.run_id = event.run_id
    WHERE event.run_id <> '' AND run.run_id IS NULL
);
INSERT INTO migration_000006_guard
SELECT 'orphan event scheduler association'
WHERE EXISTS (
    SELECT 1 FROM event_delivery
    LEFT JOIN project_scheduler ON project_scheduler.managed_loader_id = event_delivery.loader_id
    WHERE event_delivery.loader_id <> '' AND project_scheduler.id IS NULL
)
OR EXISTS (
    SELECT 1 FROM event_sandbox_link
    LEFT JOIN project_scheduler ON project_scheduler.managed_loader_id = event_sandbox_link.loader_id
    WHERE event_sandbox_link.loader_id <> '' AND project_scheduler.id IS NULL
);
INSERT INTO migration_000006_guard
SELECT 'unsupported event session link shape'
WHERE EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'event_session_link')
  AND EXISTS (
      SELECT 1
      FROM (
          SELECT 'event_id' AS name UNION ALL SELECT 'session_id'
          UNION ALL SELECT 'relation' UNION ALL SELECT 'loader_id'
          UNION ALL SELECT 'run_id' UNION ALL SELECT 'trigger_id'
          UNION ALL SELECT 'loader_event_id' UNION ALL SELECT 'created_at'
      ) AS required
      WHERE required.name NOT IN (SELECT name FROM pragma_table_info('event_session_link'))
  );
INSERT INTO migration_000006_guard
SELECT 'unsupported scheduler binding shape'
WHERE EXISTS (
    SELECT 1
    FROM (
        SELECT 'loader_id' AS name UNION ALL SELECT 'trigger_id'
        UNION ALL SELECT 'sandbox_id' UNION ALL SELECT 'sandbox_config_hash'
        UNION ALL SELECT 'created_at' UNION ALL SELECT 'updated_at'
    ) AS required
    WHERE required.name NOT IN (SELECT name FROM pragma_table_info('loader_binding'))
);

DROP TRIGGER migration_000006_abort;
DROP TABLE migration_000006_guard;

UPDATE event_delivery
SET loader_id = (SELECT id FROM project_scheduler WHERE managed_loader_id = event_delivery.loader_id)
WHERE loader_id <> '';
UPDATE event_sandbox_link
SET loader_id = (SELECT id FROM project_scheduler WHERE managed_loader_id = event_sandbox_link.loader_id)
WHERE loader_id <> '';

CREATE TABLE IF NOT EXISTS event_session_link (
    event_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    relation TEXT NOT NULL,
    loader_id TEXT NOT NULL DEFAULT '',
    run_id TEXT NOT NULL DEFAULT '',
    trigger_id TEXT NOT NULL DEFAULT '',
    loader_event_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    PRIMARY KEY(event_id, session_id, relation, run_id)
);
UPDATE event_session_link
SET loader_id = (SELECT id FROM project_scheduler WHERE managed_loader_id = event_session_link.loader_id)
WHERE loader_id <> '';

DROP INDEX idx_project_scheduler_agent;
DROP INDEX idx_project_scheduler_managed_loader;
DROP INDEX idx_loader_managed_project;
DROP INDEX idx_loader_trigger_schedule;
DROP INDEX idx_loader_run_started;
DROP INDEX idx_loader_run_trigger_started;
DROP INDEX idx_loader_run_status_started;
DROP INDEX idx_loader_run_prune;
DROP INDEX idx_loader_event_created;
DROP INDEX idx_loader_event_run_created;
DROP INDEX idx_loader_event_sandbox_run;

ALTER TABLE project_scheduler RENAME TO project_scheduler_v1;
ALTER TABLE loader_trigger RENAME TO scheduler_trigger_v1;
ALTER TABLE loader_run RENAME TO scheduler_run_v1;
ALTER TABLE loader_event RENAME TO scheduler_event_v1;
ALTER TABLE loader_state RENAME TO scheduler_state_v1;
ALTER TABLE loader_binding RENAME TO scheduler_sandbox_binding_v1;

CREATE TABLE project_scheduler (
    id TEXT NOT NULL PRIMARY KEY CHECK(trim(id) <> ''),
    short_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL,
    scheduler_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    trigger_count INTEGER NOT NULL DEFAULT 0,
    spec_json TEXT NOT NULL DEFAULT '{}',
    last_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(project_id, scheduler_id),
    FOREIGN KEY(project_id) REFERENCES project(id) ON DELETE CASCADE,
    FOREIGN KEY(project_id, agent_name) REFERENCES project_agent(project_id, agent_name) ON DELETE CASCADE
);
CREATE INDEX idx_project_scheduler_agent ON project_scheduler(project_id, agent_name);
INSERT INTO project_scheduler(
    id, short_id, project_id, scheduler_id, agent_name, revision, enabled,
    trigger_count, spec_json, last_error, created_at, updated_at
)
SELECT project_scheduler.id, project_scheduler.short_id,
       project_scheduler.project_id, project_scheduler.scheduler_id,
       project_scheduler.agent_name, project_scheduler.revision,
       project_scheduler.enabled, project_scheduler.trigger_count,
       project_scheduler.spec_json, loader.last_error,
       project_scheduler.created_at, project_scheduler.updated_at
FROM project_scheduler_v1 AS project_scheduler
JOIN loader ON loader.id = project_scheduler.managed_loader_id;

CREATE TABLE scheduler_trigger (
    scheduler_id TEXT NOT NULL,
    trigger_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    topic TEXT NOT NULL DEFAULT '',
    interval_ms INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    auto_id INTEGER NOT NULL DEFAULT 0,
    spec_json TEXT NOT NULL DEFAULT '{}',
    next_fire_at INTEGER NOT NULL DEFAULT 0,
    last_fired_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(scheduler_id, trigger_id),
    FOREIGN KEY(scheduler_id) REFERENCES project_scheduler(id) ON DELETE CASCADE
);
CREATE INDEX idx_scheduler_trigger_schedule ON scheduler_trigger(enabled, kind, next_fire_at);
INSERT INTO scheduler_trigger
SELECT project_scheduler.id, item.trigger_id, item.kind, item.topic,
       item.interval_ms, item.enabled, item.auto_id, item.spec_json,
       item.next_fire_at, item.last_fired_at
FROM scheduler_trigger_v1 AS item
JOIN project_scheduler_v1 AS project_scheduler ON project_scheduler.managed_loader_id = item.loader_id;

CREATE TABLE scheduler_run (
    scheduler_id TEXT NOT NULL,
    run_id TEXT PRIMARY KEY,
    trigger_id TEXT NOT NULL DEFAULT '',
    trigger_kind TEXT NOT NULL DEFAULT '',
    trigger_source TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    completed_at INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    result_json TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    source_script_sha256 TEXT NOT NULL DEFAULT '',
    artifacts_dir TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(scheduler_id) REFERENCES project_scheduler(id) ON DELETE CASCADE
);
CREATE INDEX idx_scheduler_run_started ON scheduler_run(scheduler_id, started_at DESC);
CREATE INDEX idx_scheduler_run_trigger_started ON scheduler_run(scheduler_id, trigger_id, started_at DESC, run_id DESC);
CREATE INDEX idx_scheduler_run_status_started ON scheduler_run(scheduler_id, status, started_at DESC, run_id DESC);
CREATE INDEX idx_scheduler_run_prune ON scheduler_run(scheduler_id, status, completed_at, started_at, run_id);
INSERT INTO scheduler_run
SELECT project_scheduler.id, item.run_id, item.trigger_id, item.trigger_kind,
       item.trigger_source, item.status, item.started_at, item.completed_at,
       item.duration_ms, item.error, item.result_json, item.payload_json,
       item.source_script_sha256, item.artifacts_dir
FROM scheduler_run_v1 AS item
JOIN project_scheduler_v1 AS project_scheduler ON project_scheduler.managed_loader_id = item.loader_id;

CREATE TABLE scheduler_event (
    scheduler_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    scheduler_run_id TEXT NOT NULL DEFAULT '',
    trigger_id TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    level TEXT NOT NULL DEFAULT 'info',
    message TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    linked_sandbox_id TEXT NOT NULL DEFAULT '',
    linked_cell_id TEXT NOT NULL DEFAULT '',
    linked_agent_thread_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    PRIMARY KEY(scheduler_id, event_id),
    FOREIGN KEY(scheduler_id) REFERENCES project_scheduler(id) ON DELETE CASCADE
);
CREATE INDEX idx_scheduler_event_created ON scheduler_event(scheduler_id, created_at DESC);
CREATE INDEX idx_scheduler_event_run_created ON scheduler_event(scheduler_id, scheduler_run_id, created_at DESC, event_id DESC);
CREATE INDEX idx_scheduler_event_sandbox_run ON scheduler_event(linked_sandbox_id, scheduler_id, scheduler_run_id) WHERE linked_sandbox_id <> '';
INSERT INTO scheduler_event
SELECT project_scheduler.id, item.event_id, item.run_id, item.trigger_id,
       item.type, item.level, item.message, item.payload_json,
       item.linked_sandbox_id, item.linked_cell_id,
       item.linked_agent_thread_id, item.created_at
FROM scheduler_event_v1 AS item
JOIN project_scheduler_v1 AS project_scheduler ON project_scheduler.managed_loader_id = item.loader_id;

CREATE TABLE scheduler_state (
    scheduler_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value_json TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(scheduler_id, key),
    FOREIGN KEY(scheduler_id) REFERENCES project_scheduler(id) ON DELETE CASCADE
);
INSERT INTO scheduler_state
SELECT project_scheduler.id, item.key, item.value_json, item.updated_at
FROM scheduler_state_v1 AS item
JOIN project_scheduler_v1 AS project_scheduler ON project_scheduler.managed_loader_id = item.loader_id;

CREATE TABLE scheduler_sandbox_binding (
    scheduler_id TEXT NOT NULL,
    trigger_id TEXT NOT NULL DEFAULT '',
    sandbox_id TEXT NOT NULL,
    sandbox_config_hash TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(scheduler_id, trigger_id),
    FOREIGN KEY(scheduler_id) REFERENCES project_scheduler(id) ON DELETE CASCADE
);
INSERT INTO scheduler_sandbox_binding
SELECT project_scheduler.id, item.trigger_id, item.sandbox_id,
       item.sandbox_config_hash, item.created_at, item.updated_at
FROM scheduler_sandbox_binding_v1 AS item
JOIN project_scheduler_v1 AS project_scheduler ON project_scheduler.managed_loader_id = item.loader_id;

DROP TABLE scheduler_sandbox_binding_v1;
DROP TABLE scheduler_state_v1;
DROP TABLE scheduler_event_v1;
DROP TABLE scheduler_run_v1;
DROP TABLE scheduler_trigger_v1;
DROP TABLE loader;
DROP TABLE project_scheduler_v1;
