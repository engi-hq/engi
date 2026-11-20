-- Large event bus payloads. A NOTIFY payload is limited to just under 8000
-- bytes, so the Postgres bus stores bigger events here and notifies the id.
-- Rows are short-lived; the publisher prunes them.
CREATE TABLE bus_events (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    payload    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_bus_events_created ON bus_events (created_at);
