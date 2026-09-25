-- Minimal pre-migration schema for the isolated attribution acceptance database.
-- It intentionally has no production data and only the columns used by migration 234.
CREATE TABLE users (
    id BIGINT PRIMARY KEY
);

CREATE TABLE api_keys (
    id BIGINT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id)
);

CREATE TABLE usage_logs (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    request_id TEXT,
    session_id VARCHAR(255),
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    total_cost NUMERIC(20, 10) NOT NULL DEFAULT 0,
    actual_cost NUMERIC(20, 10) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX usage_logs_request_key_unique
    ON usage_logs (request_id, api_key_id);

INSERT INTO users (id) VALUES (1), (2);
INSERT INTO api_keys (id, user_id) VALUES (11, 1), (12, 1), (21, 2);
