-- Workspace owns this database; owner_id is an Auth subject, not a cross-service FK.
CREATE TABLE dashboards (
    id TEXT PRIMARY KEY CHECK (id ~ '^[a-zA-Z0-9_-]{43}$'),
    owner_id BIGINT NOT NULL CHECK (owner_id > 0),
    title VARCHAR(150) NOT NULL CHECK (length(btrim(title)) > 0),
    description VARCHAR(2000) NOT NULL DEFAULT '',
    tags JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(tags) = 'array'),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX dashboards_owner_id_idx ON dashboards(owner_id, id DESC);
