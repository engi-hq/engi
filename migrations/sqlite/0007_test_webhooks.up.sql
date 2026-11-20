-- Armed test webhooks: one row per test URL waiting for its one call. The rows
-- are shared by every process, so any webhook pod can serve a test that the api
-- pod armed. Expired rows are deleted when another test is armed.

-- "shape" replaces parameter names with ":" so that the same route armed twice
-- replaces the earlier test. "segments" narrows the lookup by call length.
CREATE TABLE test_webhooks (
    method      TEXT NOT NULL,
    shape       TEXT NOT NULL,
    path        TEXT NOT NULL,
    segments    INTEGER NOT NULL,
    workflow_id TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    node        TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    PRIMARY KEY (method, shape)
);
CREATE INDEX idx_test_webhooks_workflow ON test_webhooks (workflow_id);
