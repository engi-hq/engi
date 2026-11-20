-- Chat memory of the AI Agent: one row per message, ordered by id within a
-- session. (0005 exists on Postgres only: bus events need LISTEN/NOTIFY.)

-- session_key is opaque (workflow + session id). tool_calls holds the JSON of an
-- assistant message's tool calls, empty text when there are none.
CREATE TABLE chat_messages (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    session_key  TEXT NOT NULL,
    role         TEXT NOT NULL,
    content      TEXT NOT NULL,
    tool_calls   TEXT NOT NULL DEFAULT '',
    tool_call_id TEXT NOT NULL DEFAULT '',
    is_error     INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL
);

CREATE INDEX chat_messages_session ON chat_messages (session_key, id);
CREATE INDEX chat_messages_created ON chat_messages (created_at);
