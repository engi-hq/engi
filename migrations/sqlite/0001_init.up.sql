-- Engi schema, SQLite. Conventions kept portable to Postgres:
-- text ULID ids, UTC timestamps as fixed-width ISO-8601 text, JSON as text.

CREATE TABLE workflows (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    active     INTEGER NOT NULL DEFAULT 0,
    version    INTEGER NOT NULL,
    definition TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_workflows_active ON workflows (active);
CREATE INDEX idx_workflows_name ON workflows (name);

CREATE TABLE workflow_tags (
    workflow_id TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    tag         TEXT NOT NULL,
    PRIMARY KEY (workflow_id, tag)
);
CREATE INDEX idx_workflow_tags_tag ON workflow_tags (tag);

CREATE TABLE workflow_versions (
    workflow_id TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    version     INTEGER NOT NULL,
    definition  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (workflow_id, version)
);

CREATE TABLE executions (
    id                  TEXT PRIMARY KEY,
    workflow_id         TEXT NOT NULL,
    workflow_version    INTEGER NOT NULL,
    mode                TEXT NOT NULL,
    status              TEXT NOT NULL,
    started_at          TEXT NOT NULL,
    finished_at         TEXT,
    wait_until          TEXT,
    dedup_key           TEXT,
    parent_execution_id TEXT,
    error               TEXT,
    FOREIGN KEY (workflow_id) REFERENCES workflows (id) ON DELETE CASCADE,
    FOREIGN KEY (workflow_id, workflow_version) REFERENCES workflow_versions (workflow_id, version)
);
CREATE INDEX idx_executions_workflow_status_started ON executions (workflow_id, status, started_at);
CREATE INDEX idx_executions_status_finished ON executions (status, finished_at);
CREATE INDEX idx_executions_waiting ON executions (wait_until) WHERE status = 'waiting';
CREATE UNIQUE INDEX idx_executions_dedup ON executions (workflow_id, dedup_key) WHERE dedup_key IS NOT NULL;

-- Heavy columns live apart so listings never read them.
CREATE TABLE execution_data (
    execution_id TEXT PRIMARY KEY REFERENCES executions (id) ON DELETE CASCADE,
    run_data     TEXT NOT NULL,
    state        TEXT
);

CREATE TABLE credentials (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    type       TEXT NOT NULL,
    key_id     TEXT NOT NULL,
    data       BLOB NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE jobs (
    id           TEXT PRIMARY KEY,
    execution_id TEXT NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    status       TEXT NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0,
    available_at TEXT NOT NULL,
    lease_owner  TEXT,
    lease_until  TEXT,
    last_error   TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX idx_jobs_claim ON jobs (available_at, id) WHERE status = 'queued';
CREATE INDEX idx_jobs_lease ON jobs (lease_until) WHERE status = 'leased';
CREATE INDEX idx_jobs_execution ON jobs (execution_id);
