-- Metadata only: billing and usage counters remain exclusively in usage_logs.
CREATE TABLE usage_attribution_sessions (
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
 session_id VARCHAR(200) NOT NULL,
 project_id VARCHAR(200) NOT NULL,
 task_id VARCHAR(200) NOT NULL,
 client_kind VARCHAR(32) NOT NULL CHECK (client_kind IN ('codex','claude')),
 host VARCHAR(200),
 parent_session_id VARCHAR(200),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY (user_id, api_key_id, session_id)
);
CREATE TABLE usage_attribution_names (
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 kind VARCHAR(16) NOT NULL CHECK(kind IN ('project','task')),
 id VARCHAR(200) NOT NULL,
 name VARCHAR(300) NOT NULL,
 PRIMARY KEY(user_id,kind,id)
);
ALTER TABLE usage_logs ADD COLUMN attribution JSONB;
CREATE INDEX usage_logs_attribution_project_idx ON usage_logs(user_id, (attribution->>'project_id'), created_at);
CREATE INDEX usage_logs_attribution_task_idx ON usage_logs(user_id, (attribution->>'task_id'), created_at);
-- The immutable registration must exist before a request is submitted. Snapshot at
-- the common INSERT boundary covers synchronous, batched, retry and WebSocket writes.
CREATE FUNCTION capture_usage_attribution() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 SELECT jsonb_strip_nulls(jsonb_build_object('project_id',s.project_id,'task_id',s.task_id,
   'client_kind',s.client_kind,'host',s.host,'parent_session_id',s.parent_session_id,'thread_id',s.session_id))
 INTO NEW.attribution FROM usage_attribution_sessions s
 WHERE s.user_id=NEW.user_id AND s.api_key_id=NEW.api_key_id AND s.session_id=NEW.session_id
 AND s.created_at <= NEW.created_at;
 RETURN NEW;
END;
$$;
CREATE TRIGGER usage_logs_capture_attribution BEFORE INSERT ON usage_logs
 FOR EACH ROW EXECUTE FUNCTION capture_usage_attribution();
