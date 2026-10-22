CREATE TABLE external_identities (
    provider TEXT NOT NULL,
    subject TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(provider,subject)
);
CREATE TABLE oauth_states (
    hash TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    encrypted_verifier BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
