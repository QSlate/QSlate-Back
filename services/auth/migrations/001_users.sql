CREATE TABLE users (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email VARCHAR(254) NOT NULL UNIQUE CHECK (email = lower(email)),
    username VARCHAR(32) NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9][a-z0-9_-]{2,31}$'),
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
