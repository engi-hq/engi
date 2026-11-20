-- Users, sessions and API keys. Same conventions as 0001.

CREATE TABLE users (
    id            TEXT COLLATE "C" PRIMARY KEY,
    email         TEXT NOT NULL,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL,
    disabled      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX idx_users_email ON users (email);
-- At most one owner, so concurrent first-run setups cannot both succeed.
CREATE UNIQUE INDEX idx_users_single_owner ON users (role) WHERE role = 'owner';

CREATE TABLE sessions (
    token_hash   TEXT PRIMARY KEY,
    user_id      TEXT COLLATE "C" NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf_token   TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);

CREATE TABLE api_keys (
    id           TEXT COLLATE "C" PRIMARY KEY,
    user_id      TEXT COLLATE "C" NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    secret_hash  TEXT NOT NULL,
    scopes       TEXT NOT NULL,
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX idx_api_keys_prefix ON api_keys (prefix);
CREATE INDEX idx_api_keys_user ON api_keys (user_id);
