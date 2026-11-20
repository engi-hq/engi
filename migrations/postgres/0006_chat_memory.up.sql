-- Chat memory of the AI Agent: one row per message, ordered by id within a
-- session.

-- session_key is opaque (workflow + session id). tool_calls holds the JSON of an
-- assistant message's tool calls, empty text when there are none.
CREATE TABLE chat_messages (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    session_key  TEXT COLLATE "C" NOT NULL,
    role         TEXT NOT NULL,
    content      TEXT NOT NULL,
    tool_calls   TEXT NOT NULL DEFAULT '',
    tool_call_id TEXT NOT NULL DEFAULT '',
    is_error     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX chat_messages_session ON chat_messages (session_key, id);
CREATE INDEX chat_messages_created ON chat_messages (created_at);
