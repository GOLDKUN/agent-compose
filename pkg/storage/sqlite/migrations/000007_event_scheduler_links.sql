CREATE TEMP TABLE migration_000007_guard(reason TEXT);
CREATE TEMP TRIGGER migration_000007_abort
BEFORE INSERT ON migration_000007_guard
BEGIN
    SELECT RAISE(ABORT, 'event link migration found conflicting sandbox and session records');
END;

INSERT INTO migration_000007_guard
SELECT 'conflicting event sandbox/session link'
WHERE EXISTS (
    SELECT 1
    FROM event_sandbox_link AS sandbox_link
    JOIN event_session_link AS session_link
      ON session_link.event_id = sandbox_link.event_id
     AND session_link.session_id = sandbox_link.sandbox_id
     AND session_link.relation = sandbox_link.relation
     AND session_link.run_id = sandbox_link.run_id
    WHERE session_link.loader_id <> sandbox_link.loader_id
       OR session_link.trigger_id <> sandbox_link.trigger_id
       OR session_link.loader_event_id <> sandbox_link.loader_event_id
       OR session_link.created_at <> sandbox_link.created_at
);

DROP TRIGGER migration_000007_abort;
DROP TABLE migration_000007_guard;

DROP INDEX idx_event_delivery_run;
DROP INDEX idx_event_delivery_status;
DROP INDEX idx_event_delivery_loader_run;
DROP INDEX idx_event_sandbox_link_sandbox;
DROP INDEX idx_event_sandbox_link_run;
DROP INDEX idx_event_sandbox_link_loader_run;

ALTER TABLE event_delivery RENAME TO event_delivery_v1;
ALTER TABLE event_sandbox_link RENAME TO event_sandbox_link_v1;

CREATE TABLE event_delivery (
    event_id TEXT NOT NULL,
    scheduler_id TEXT NOT NULL,
    trigger_id TEXT NOT NULL,
    scheduler_run_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(event_id, scheduler_id, trigger_id)
);
CREATE INDEX idx_event_delivery_scheduler_run ON event_delivery(scheduler_run_id);
CREATE INDEX idx_event_delivery_status ON event_delivery(status, updated_at);
CREATE INDEX idx_event_delivery_scheduler ON event_delivery(scheduler_id, scheduler_run_id);
INSERT INTO event_delivery(
    event_id, scheduler_id, trigger_id, scheduler_run_id, status, error,
    created_at, updated_at
)
SELECT event_id, loader_id, trigger_id, run_id, status, error,
       created_at, updated_at
FROM event_delivery_v1;

CREATE TABLE event_sandbox_link (
    event_id TEXT NOT NULL,
    sandbox_id TEXT NOT NULL,
    relation TEXT NOT NULL,
    scheduler_id TEXT NOT NULL DEFAULT '',
    scheduler_run_id TEXT NOT NULL DEFAULT '',
    trigger_id TEXT NOT NULL DEFAULT '',
    scheduler_event_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    PRIMARY KEY(event_id, sandbox_id, relation, scheduler_run_id)
);
CREATE INDEX idx_event_sandbox_link_sandbox ON event_sandbox_link(sandbox_id, created_at);
CREATE INDEX idx_event_sandbox_link_scheduler_run ON event_sandbox_link(scheduler_run_id);
CREATE INDEX idx_event_sandbox_link_scheduler ON event_sandbox_link(scheduler_id, scheduler_run_id);
INSERT OR IGNORE INTO event_sandbox_link(
    event_id, sandbox_id, relation, scheduler_id, scheduler_run_id,
    trigger_id, scheduler_event_id, created_at
)
SELECT event_id, sandbox_id, relation, loader_id, run_id,
       trigger_id, loader_event_id, created_at
FROM event_sandbox_link_v1;
INSERT OR IGNORE INTO event_sandbox_link(
    event_id, sandbox_id, relation, scheduler_id, scheduler_run_id,
    trigger_id, scheduler_event_id, created_at
)
SELECT event_id, session_id, relation, loader_id, run_id,
       trigger_id, loader_event_id, created_at
FROM event_session_link;

DROP TABLE event_session_link;
DROP TABLE event_sandbox_link_v1;
DROP TABLE event_delivery_v1;
