-- Trigger registrations: webhook routes, scheduler state, activation errors.

-- One row per webhook trigger of an active workflow. "path" keeps the pattern
-- as written ("orders/:id"); "shape" replaces parameter names with ":" so that
-- two patterns that match the same requests collide on the unique index.
CREATE TABLE webhook_registrations (
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    shape       TEXT NOT NULL,
    segments    INTEGER NOT NULL,
    dynamic     BOOLEAN NOT NULL DEFAULT FALSE,
    workflow_id TEXT COLLATE "C" NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    node        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX idx_webhook_registrations_route ON webhook_registrations (method, shape);
CREATE INDEX idx_webhook_registrations_workflow ON webhook_registrations (workflow_id);
CREATE INDEX idx_webhook_registrations_dynamic ON webhook_registrations (method, segments) WHERE dynamic;

-- Opaque per-trigger state (the scheduler keeps its next fire time here).
CREATE TABLE trigger_state (
    workflow_id TEXT COLLATE "C" NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    node        TEXT NOT NULL,
    state       TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workflow_id, node)
);

-- Why the last activation of a workflow failed. Absent when it did not.
CREATE TABLE workflow_activation_errors (
    workflow_id TEXT COLLATE "C" PRIMARY KEY REFERENCES workflows (id) ON DELETE CASCADE,
    message     TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);
