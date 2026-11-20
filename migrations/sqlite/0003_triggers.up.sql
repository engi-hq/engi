-- Trigger registrations: webhook routes, scheduler state, activation errors.

-- One row per webhook trigger of an active workflow. "path" keeps the pattern
-- as written ("orders/:id"); "shape" replaces parameter names with ":" so that
-- two patterns that match the same requests collide on the unique index.
CREATE TABLE webhook_registrations (
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    shape       TEXT NOT NULL,
    segments    INTEGER NOT NULL,
    dynamic     INTEGER NOT NULL DEFAULT 0,
    workflow_id TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    node        TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_webhook_registrations_route ON webhook_registrations (method, shape);
CREATE INDEX idx_webhook_registrations_workflow ON webhook_registrations (workflow_id);
CREATE INDEX idx_webhook_registrations_dynamic ON webhook_registrations (method, segments) WHERE dynamic = 1;

-- Opaque per-trigger state (the scheduler keeps its next fire time here).
CREATE TABLE trigger_state (
    workflow_id TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    node        TEXT NOT NULL,
    state       TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (workflow_id, node)
);

-- Why the last activation of a workflow failed. Absent when it did not.
CREATE TABLE workflow_activation_errors (
    workflow_id TEXT PRIMARY KEY REFERENCES workflows (id) ON DELETE CASCADE,
    message     TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
