CREATE TABLE lab_runs (
 id TEXT PRIMARY KEY,
 owner_id BIGINT NOT NULL CHECK(owner_id>0),
 status TEXT NOT NULL CHECK(status IN ('queued','running','completed','failed','cancelled')),
 progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100),
 input JSONB NOT NULL,
 frozen JSONB NOT NULL,
 execution_hash TEXT NOT NULL,
 runtime_image TEXT NOT NULL DEFAULT '',
 optimization_id TEXT NOT NULL DEFAULT '',
 result JSONB,
 error TEXT NOT NULL DEFAULT '',
 claim_token TEXT NOT NULL DEFAULT '',
 lease_until TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX lab_runs_owner ON lab_runs(owner_id,created_at DESC,id DESC);
CREATE INDEX lab_runs_queue ON lab_runs(created_at,id) WHERE status='queued';
CREATE TABLE lab_requests (
 owner_id BIGINT NOT NULL,
 operation TEXT NOT NULL,
 key TEXT NOT NULL,
 hash TEXT NOT NULL,
 response JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(owner_id,operation,key)
);
CREATE TABLE lab_events (
 id BIGSERIAL PRIMARY KEY,
 run_id TEXT NOT NULL REFERENCES lab_runs(id) ON DELETE CASCADE,
 type TEXT NOT NULL,
 data JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX lab_events_run ON lab_events(run_id,id);
CREATE TABLE lab_workers (
 id TEXT PRIMARY KEY,
 execution_hash TEXT NOT NULL,
 ready BOOLEAN NOT NULL,
 seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
