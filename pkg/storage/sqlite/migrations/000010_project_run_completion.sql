ALTER TABLE project_run ADD COLUMN cleanup_policy TEXT NOT NULL DEFAULT 'stop_on_completion';
ALTER TABLE project_run ADD COLUMN sandbox_created INTEGER NOT NULL DEFAULT 0;

CREATE TABLE project_run_completion (
    run_id TEXT PRIMARY KEY,
    target_status TEXT NOT NULL,
    transition_json TEXT NOT NULL,
    cleanup_action TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY(run_id) REFERENCES project_run(run_id) ON DELETE CASCADE
);

CREATE INDEX idx_project_run_completion_retry
    ON project_run_completion(next_attempt_at, created_at);
