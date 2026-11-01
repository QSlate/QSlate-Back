CREATE TABLE strategies (
 id TEXT PRIMARY KEY CHECK (id ~ '^[A-Za-z0-9_-]{43}$'),
 owner_id BIGINT NOT NULL CHECK(owner_id>0),
 current_version_id TEXT NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),
 deleted_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE strategy_versions (
 id TEXT PRIMARY KEY CHECK(id ~ '^[A-Za-z0-9_-]{43}$'),
 strategy_id TEXT NOT NULL REFERENCES strategies(id),
 revision BIGINT NOT NULL CHECK(revision>0),
 name VARCHAR(150) NOT NULL CHECK(length(btrim(name))>0),
 source TEXT NOT NULL CHECK(octet_length(source) BETWEEN 1 AND 100000),
 parameters JSONB NOT NULL CHECK(jsonb_typeof(parameters)='object'),
 restored_from TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(strategy_id,revision),
 UNIQUE(strategy_id,id),
 UNIQUE(strategy_id,id,revision),
 FOREIGN KEY(strategy_id,restored_from) REFERENCES strategy_versions(strategy_id,id)
);
ALTER TABLE strategies ADD CONSTRAINT strategies_current_version_fk
 FOREIGN KEY(id,current_version_id,revision) REFERENCES strategy_versions(strategy_id,id,revision)
 DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX strategies_owner_idx ON strategies(owner_id,id DESC) WHERE deleted_at IS NULL;
CREATE FUNCTION reject_strategy_version_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Strategy versions are immutable; insert a new revision';
END;
$$;
CREATE TRIGGER immutable_strategy_versions BEFORE UPDATE OR DELETE ON strategy_versions
 FOR EACH ROW EXECUTE FUNCTION reject_strategy_version_mutation();
