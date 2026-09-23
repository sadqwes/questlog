-- Сессии входа. Храним только SHA-256 от токена: утечка базы не даёт готовых cookie.
CREATE TABLE sessions (
    token_hash bytea       PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
