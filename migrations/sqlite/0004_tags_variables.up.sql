-- Managed tags and instance variables.

-- A tag has an identity so it can be renamed and deleted. Workflows keep
-- referencing tags by name (workflow_tags.tag); a trigger registers every name
-- a workflow starts using, so tags saved with a workflow show up in the list.
CREATE TABLE tags (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

INSERT INTO tags (id, name, created_at)
SELECT 'tg_' || hex(randomblob(13)), tag, strftime('%Y-%m-%dT%H:%M:%f000Z', 'now')
FROM (SELECT DISTINCT tag FROM workflow_tags);

CREATE TRIGGER workflow_tags_register AFTER INSERT ON workflow_tags
BEGIN
    INSERT OR IGNORE INTO tags (id, name, created_at)
    VALUES ('tg_' || hex(randomblob(13)), NEW.tag, strftime('%Y-%m-%dT%H:%M:%f000Z', 'now'));
END;

-- "value" is plaintext, or ciphertext sealed under key_id when secret = 1.
CREATE TABLE variables (
    id         TEXT PRIMARY KEY,
    key        TEXT NOT NULL UNIQUE,
    secret     INTEGER NOT NULL DEFAULT 0,
    key_id     TEXT NOT NULL DEFAULT '',
    value      BLOB NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
