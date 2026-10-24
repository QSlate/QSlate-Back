CREATE TABLE widgets (
 id TEXT PRIMARY KEY CHECK (id ~ '^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$'),
 owner_id BIGINT CHECK (owner_id > 0),
 kind TEXT NOT NULL CHECK (kind IN ('custom','installed')),
 current_version_id TEXT NOT NULL,
 deleted_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK ((kind='custom' AND owner_id IS NOT NULL) OR (kind='installed' AND owner_id IS NULL))
);
CREATE TABLE widget_versions (
 id TEXT PRIMARY KEY,
 widget_id TEXT NOT NULL REFERENCES widgets(id),
 name VARCHAR(150) NOT NULL CHECK (length(btrim(name))>0),
 language TEXT NOT NULL CHECK (language IN ('python','rust')),
 source TEXT NOT NULL CHECK (octet_length(source) BETWEEN 1 AND 100000),
 configuration JSONB NOT NULL CHECK (jsonb_typeof(configuration)='object'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(widget_id,id)
);
ALTER TABLE widgets ADD CONSTRAINT widgets_current_version_fk FOREIGN KEY(id,current_version_id) REFERENCES widget_versions(widget_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX widgets_owner_id_idx ON widgets(owner_id,id DESC) WHERE deleted_at IS NULL;
CREATE INDEX widget_versions_widget_idx ON widget_versions(widget_id,id);

CREATE FUNCTION reject_widget_version_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Widget versions are immutable; insert a new version';
END;
$$;
CREATE TRIGGER immutable_widget_versions BEFORE UPDATE OR DELETE ON widget_versions
 FOR EACH ROW EXECUTE FUNCTION reject_widget_version_mutation();
