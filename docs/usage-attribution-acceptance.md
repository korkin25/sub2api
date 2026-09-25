# Usage Attribution Acceptance

This acceptance harness validates the server-side usage attribution boundary. It
uses an ephemeral PostgreSQL container with no published port, no production
database, no credentials, and no upstream provider call.

Run after the backend migration is present:

```bash
tests/usage-attribution/run-postgres-acceptance.sh
python3 tests/usage-attribution/probe-codex-app-server.py
```

The native probe requires Python 3 with the `websockets` package and a local
Codex CLI. The PostgreSQL image defaults to `postgres:18-alpine` and can be
overridden with `POSTGRES_IMAGE`.

The harness applies `backend/migrations/234_usage_attribution.sql` to a minimal
pre-migration schema, then verifies these database invariants:

- registry identity is `(user_id, api_key_id, session_id)`; equal session IDs
  on two API keys owned by one user remain separate;
- a second registration cannot retarget an existing binding;
- a usage insert receives a server-side project/task snapshot, including a
  child session with its parent session ID;
- unknown sessions remain unattributed;
- project/task sums retain token dimensions and decimal actual cost;
- a duplicate `(request_id, api_key_id)` cannot add or retarget a usage row.

The test data covers two projects for one user with two API keys, a second user,
a parent/child session, an unknown session, decimal costs, and retry
idempotency. It does not manufacture client totals.

The Codex probe uses one isolated app-server process and two concurrent
loopback WebSocket JSON-RPC clients. It trusts only its generated SessionStart
hook through `hooks/list` and `config/batchWrite` in its isolated `CODEX_HOME`,
then checks that distinct thread IDs reach a mock provider Responses WebSocket
in `x-codex-turn-metadata`. It does not write trust state to the host.

The provider probe requires registration before each `request_kind=turn` WebSocket
request and rejects HTTP fallback or retry. Native Codex can automatically issue
`request_kind=prewarm` immediately after `thread/start`, before SessionStart
registration. The mock counts prewarm separately; any billed unregistered
prewarm remains Unknown. A requirement that registration precede every provider
handshake must move registration ahead of `thread/start` or explicitly exempt
prewarm.

Verified evidence:

- PostgreSQL 18 contract: registry identity, immutable bindings, child snapshot,
  unknown rows, sums, and retry idempotency.
- Real backend repository and HTTP integration at the authentication-context
  seam: owner/key scope, report, options, export, rename, child resume, and all
  seven report groupings.
- Native real-hook transport: two distinct synthetic git projects in one
  app-server, completed provider Responses WebSocket turns, distinct thread
  metadata, and no HTTP fallback or retry.
- Separate native-client proof: Codex V1/V2 and Claude child/resume behavior.

The app-server WebSocket transport and the provider Responses WebSocket are
different transports. The native app-server probe exercises both and completes
without HTTP fallback or retry. These results are integration evidence only;
no production rollout was performed.
