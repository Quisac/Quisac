
CREATE TABLE IF NOT EXISTS users (
    id         BIGINT      PRIMARY KEY,            -- GitHub user id
    login      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT        PRIMARY KEY,        -- opaque random token (the cookie value)
    user_id         BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    installation_id TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx    ON sessions (user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions (expires_at);
