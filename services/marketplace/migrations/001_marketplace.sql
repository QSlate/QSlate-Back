CREATE TABLE market_items (
 id TEXT PRIMARY KEY CHECK(id ~ '^[A-Za-z0-9_-]{43}$'),
 owner_id BIGINT NOT NULL CHECK(owner_id>0),
 kind TEXT NOT NULL CHECK(kind IN ('widget','indicator','strategy')),
 resource_id TEXT NOT NULL,
 current_version_id TEXT NOT NULL,
 name VARCHAR(150) NOT NULL CHECK(length(btrim(name))>0),
 description TEXT NOT NULL DEFAULT '',
 category TEXT NOT NULL,
 visibility TEXT NOT NULL CHECK(visibility IN ('public','unlisted')),
 revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),
 deleted_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(owner_id,kind,resource_id)
);
CREATE TABLE market_versions (
 item_id TEXT NOT NULL REFERENCES market_items(id),
 version_id TEXT NOT NULL,
 snapshot JSONB NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(item_id,version_id)
);
ALTER TABLE market_items ADD CONSTRAINT market_current_version_fk FOREIGN KEY(id,current_version_id) REFERENCES market_versions(item_id,version_id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE market_installations (
 owner_id BIGINT NOT NULL CHECK(owner_id>0),
 item_id TEXT NOT NULL,
 version_id TEXT NOT NULL,
 installed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 removed_at TIMESTAMPTZ,
 PRIMARY KEY(owner_id,item_id),
 FOREIGN KEY(item_id,version_id) REFERENCES market_versions(item_id,version_id)
);
-- Retain pins for existing dashboard references after uninstall/version changes.
CREATE TABLE market_grants (
 owner_id BIGINT NOT NULL,
 item_id TEXT NOT NULL,
 version_id TEXT NOT NULL,
 PRIMARY KEY(owner_id,item_id,version_id),
 FOREIGN KEY(item_id,version_id) REFERENCES market_versions(item_id,version_id)
);
CREATE TABLE market_reviews (
 id TEXT PRIMARY KEY,
 item_id TEXT NOT NULL REFERENCES market_items(id),
 owner_id BIGINT NOT NULL CHECK(owner_id>0),
 rating INT NOT NULL CHECK(rating BETWEEN 1 AND 5),
 comment TEXT NOT NULL CHECK(length(comment)<=4000),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(owner_id,item_id)
);
CREATE INDEX market_public_idx ON market_items(id DESC) WHERE deleted_at IS NULL AND visibility='public';
CREATE INDEX market_installations_owner_idx ON market_installations(owner_id,item_id DESC) WHERE removed_at IS NULL;
CREATE INDEX market_reviews_item_idx ON market_reviews(item_id,id DESC);
CREATE FUNCTION reject_market_version_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'Published snapshots are immutable'; END; $$;
CREATE TRIGGER immutable_market_versions BEFORE UPDATE OR DELETE ON market_versions FOR EACH ROW EXECUTE FUNCTION reject_market_version_mutation();
