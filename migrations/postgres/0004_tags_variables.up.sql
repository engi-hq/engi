-- Managed tags and instance variables.

-- A tag has an identity so it can be renamed and deleted. Workflows keep
-- referencing tags by name (workflow_tags.tag); a trigger registers every name
-- a workflow starts using, so tags saved with a workflow show up in the list.
CREATE TABLE tags (
    id         TEXT COLLATE "C" PRIMARY KEY,
    name       TEXT COLLATE "C" NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL
);

INSERT INTO tags (id, name, created_at)
SELECT 'tg_' || substr(md5(random()::text || tag), 1, 26), tag, now()
FROM (SELECT DISTINCT tag FROM workflow_tags) AS t;

CREATE FUNCTION workflow_tags_register() RETURNS trigger AS $$
BEGIN
    INSERT INTO tags (id, name, created_at)
    VALUES ('tg_' || substr(md5(random()::text || NEW.tag), 1, 26), NEW.tag, now())
    ON CONFLICT (name) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER workflow_tags_register AFTER INSERT ON workflow_tags
    FOR EACH ROW EXECUTE FUNCTION workflow_tags_register();

-- "value" is plaintext, or ciphertext sealed under key_id when secret is true.
CREATE TABLE variables (
    id         TEXT COLLATE "C" PRIMARY KEY,
    key        TEXT COLLATE "C" NOT NULL UNIQUE,
    secret     BOOLEAN NOT NULL DEFAULT FALSE,
    key_id     TEXT NOT NULL DEFAULT '',
    value      BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
