\set ON_ERROR_STOP on

-- The same authenticated user can hold separate keys. The session value is
-- deliberately identical to prove that attribution is also key-scoped.
INSERT INTO usage_attribution_sessions
    (user_id, api_key_id, session_id, client_kind, project_id, task_id, parent_session_id)
VALUES
    (1, 11, 'shared-session', 'codex', 'project-a', 'task-root-a', NULL),
    (1, 12, 'shared-session', 'claude', 'project-b', 'task-root-b', NULL),
    (1, 11, 'child-session', 'codex', 'project-a', 'task-root-a', 'shared-session'),
    (2, 21, 'shared-session', 'codex', 'project-c', 'task-root-c', NULL);

-- Registration is not a backfill mechanism. A usage row whose recorded time
-- predates a binding stays unknown even when the write is replayed later.
INSERT INTO usage_logs
    (user_id, api_key_id, request_id, session_id, input_tokens, total_cost, actual_cost, created_at)
VALUES
    (1, 11, 'request-before-registration', 'late-registered-session', 1, 0.0001000000, 0.0001000000,
     TIMESTAMPTZ '2000-01-01 00:00:00+00');
INSERT INTO usage_attribution_sessions
    (user_id, api_key_id, session_id, client_kind, project_id, task_id)
VALUES (1, 11, 'late-registered-session', 'codex', 'project-late', 'task-late');

-- A registry binding is immutable: a later registration must not retarget a
-- live session. This SQL block must catch the expected unique violation.
DO $$
BEGIN
    INSERT INTO usage_attribution_sessions
        (user_id, api_key_id, session_id, client_kind, project_id, task_id)
    VALUES (1, 11, 'shared-session', 'codex', 'attacker-project', 'attacker-task');
    RAISE EXCEPTION 'mutable attribution registration was accepted';
EXCEPTION WHEN unique_violation THEN
    NULL;
END;
$$;

-- Two keys of user 1, a child session, a second user, and an unknown session.
-- The trigger must snapshot server-side bindings; it must never trust a total
-- supplied by a client, and unknown correlation remains unknown.
INSERT INTO usage_logs
    (user_id, api_key_id, request_id, session_id, input_tokens, output_tokens,
     cache_creation_tokens, cache_read_tokens, total_cost, actual_cost)
VALUES
    (1, 11, 'request-a', 'shared-session', 10, 4, 2, 1, 0.0011000000, 0.0010000000),
    (1, 12, 'request-b', 'shared-session', 20, 8, 3, 2, 0.0022000000, 0.0020000000),
    (1, 11, 'request-child', 'child-session', 7, 3, 1, 1, 0.0007000000, 0.0006000000),
    (2, 21, 'request-c', 'shared-session', 30, 9, 4, 3, 0.0033000000, 0.0030000000),
    (1, 11, 'request-unknown', 'unknown-session', 5, 2, 0, 0, 0.0005000000, 0.0004000000);

-- Existing request idempotency remains in force. The duplicate must neither
-- add another total nor permit the later row to change the snapshot.
DO $$
BEGIN
    INSERT INTO usage_logs
        (user_id, api_key_id, request_id, session_id, input_tokens, total_cost, actual_cost)
    VALUES (1, 11, 'request-a', 'child-session', 999, 99, 99);
    RAISE EXCEPTION 'duplicate usage record was accepted';
EXCEPTION WHEN unique_violation THEN
    NULL;
END;
$$;

DO $$
DECLARE
    got JSONB;
    rows INTEGER;
    tokens INTEGER;
    cost NUMERIC(20, 10);
BEGIN
    SELECT attribution INTO got FROM usage_logs WHERE request_id = 'request-a';
    IF got->>'project_id' <> 'project-a' OR got->>'task_id' <> 'task-root-a' THEN
        RAISE EXCEPTION 'key 11 attribution mismatch: %', got;
    END IF;

    SELECT attribution INTO got FROM usage_logs WHERE request_id = 'request-b';
    IF got->>'project_id' <> 'project-b' OR got->>'task_id' <> 'task-root-b' THEN
        RAISE EXCEPTION 'same user / different key leaked attribution: %', got;
    END IF;

    SELECT attribution INTO got FROM usage_logs WHERE request_id = 'request-child';
    IF got->>'parent_session_id' <> 'shared-session' OR got->>'task_id' <> 'task-root-a' THEN
        RAISE EXCEPTION 'child attribution mismatch: %', got;
    END IF;

    SELECT attribution INTO got FROM usage_logs WHERE request_id = 'request-unknown';
    IF got IS NOT NULL THEN
        RAISE EXCEPTION 'unknown session was attributed: %', got;
    END IF;

    SELECT attribution INTO got FROM usage_logs WHERE request_id = 'request-before-registration';
    IF got IS NOT NULL THEN
        RAISE EXCEPTION 'late registration backfilled historical usage: %', got;
    END IF;

    SELECT count(*), sum(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), sum(actual_cost)
      INTO rows, tokens, cost
      FROM usage_logs
     WHERE attribution->>'project_id' = 'project-a'
       AND attribution->>'task_id' = 'task-root-a';
    IF rows <> 2 OR tokens <> 29 OR cost <> 0.0016000000 THEN
        RAISE EXCEPTION 'project/task aggregate mismatch rows=% tokens=% cost=%', rows, tokens, cost;
    END IF;

    SELECT count(*) INTO rows FROM usage_logs WHERE request_id = 'request-a';
    IF rows <> 1 THEN
        RAISE EXCEPTION 'idempotency mismatch: % rows for request-a', rows;
    END IF;
END;
$$;
