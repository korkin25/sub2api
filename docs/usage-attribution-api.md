# Usage attribution API

All accounting remains in `usage_logs`. Attribution is metadata, never client-supplied tokens/costs. Historical and unregistered requests retain NULL attribution and remain in totals. A BEFORE INSERT trigger snapshots an immutable session registration for the authenticated `(user_id, api_key_id, session_id)`; registrations later than the usage timestamp cannot backfill that request. Requests are counted once using the existing usage insertion/deduplication pipeline. Subagents register the same root `task_id`; no parent rollup copies are generated.

## Client registration

`POST /api/v1/usage-attribution/sessions`, Bearer **existing gateway API key** (not a dashboard JWT). Body:

```json
{"client_kind":"codex","session_id":"current-thread-uuid","project_id":"stable-project-uuid","project_name":"Project","task_id":"root-dialogue-uuid","task_name":"Task","host":"origin-host","parent_session_id":"parent-thread-uuid"}
```

`host` and `parent_session_id` optional. When parent supplied, project/task IDs may be omitted and are inherited from that parent's registration under the **same authenticated API key**. Missing parent: 404. Client kind must match parent. IDs max200 characters, names max300, no controls. Root project/task required; task IDs must be globally unique within the owner (normally root dialogue UUID), not sequential local counters. Client-supplied owner/key identities are ignored. Keys sharing an owner remain separate session namespaces. Same binding is idempotent; different project/task/client/parent for an existing session: 409. Registration names only initialize missing labels; resume cannot undo edits. SessionStart may set `preserve_existing:true`: atomic upsert retains a previously registered binding under this exact owner/key/session/client even when resume proposes new project/task defaults. This flag is for root/default SessionStart registration without a parent; SubagentStart uses strict parent inheritance. Client-kind mismatch remains409. `host` is the **origin host at first registration**, not current request host; moving a resumed session retains this origin. This limitation avoids inventing per-request host provenance from a shared daemon.

Register synchronously before the first gateway request. HTTP and WebSocket usage correlation gives native request-body/frame metadata precedence over connection headers: `client_metadata.thread_id`, embedded `client_metadata.x-codex-turn-metadata.thread_id` (JSON string or object), then `client_metadata.session_id`; next Claude `metadata.user_id.session_id` (JSON string, legacy string, or object). Header fallback is `thread-id`, `x-codex-turn-metadata.thread_id`, its session ID, then existing session headers. HTTP captures the result once before provider rewriting, including an empty result: unknown identity stays unknown even if a provider rewrite later generates an ID. WebSocket recording extracts from each pre-forward frame snapshot independently, so frame metadata overrides a stale handshake or previous turn. Writers carry the captured scalar session ID into asynchronous recording. Project names, task names and host are never placed in provider requests. This endpoint does not forward anything upstream.

## Reports

JWT-authenticated `GET /api/v1/usage/attribution`; administrator `GET /api/v1/admin/usage/attribution`.

Parameters: `start_date`, `end_date` (UTC dates, inclusive end date, capped at current UTC time), `group_by=project|task|client|host|api_key|model|user` (user grouping admin only), optional `api_key_id`, `project_id`, `task_id`, `client_kind`, `host`, `model`, `unknown=true`. Admin may also filter `user_id`; ordinary users are always scoped to their authenticated owner. Missing range defaults last seven UTC calendar days through now; maximum366 days. `unknown=true` selects rows whose complete attribution snapshot is NULL. Model is requested model fallback stored model. All rows, including zero-cost rows, are counted; this intentionally differs from older dashboards that suppress zero-cost rows.

Response in standard success envelope: `{groups:[{user_id,id,name,...measures}],totals:{...measures},series:[{date,...measures}],window_start,window_end,window_minutes}`. Groups separate owners. Nullable ID/name is an unlabelled group. Key grouping names come from existing API keys (e.g. logical users `csssr`, `kk573`), never a client assertion. User group name is username. Group IDs remain stable through renames.

Measures: integer `requests,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,total_tokens`; decimal **strings** `total_cost,actual_cost`; numeric `rpm,tpm`. Total tokens=input+output+cache creation+cache read, matching existing usage totals. Costs summed as PostgreSQL numeric before string serialization. RPM=request count/window minutes; TPM=total tokens/window minutes. Daily series rates use the intersection of that UTC day with the requested window. These are window averages, not instantaneous or peak rates. Totals, groups and series share one repeatable-read snapshot. Unknown rows and all child threads contribute exactly once.

`GET` same path + `/options` supplies `{projects,tasks,clients,hosts,users,truncated}`; each choice is `{user_id,id,name}`. One filtered distinct scan (at most5000 combinations) supplies choices rather than repeated full reports. `truncated=true` explicitly indicates incomplete choices; direct ID filters and report totals are not truncated.

`GET` same path + `/export` uses identical scope and grouped results, exports CSV with precision-preserved costs and explicit window boundaries. Spreadsheet formula prefixes in client labels/IDs are escaped.

`PATCH` same path + `/names`: `{kind:"project"|"task",id,name}`; admin must also supply `user_id`. Changes display names across history without changing IDs or splitting totals. Names are owner-scoped; two API keys deliberately sharing a project/root task ID share its label. No legacy usage/dashboard filters are silently changed; the frontend presents a separate panel with explicit scope.

## Deployment boundary

Migration234 is additive. No live migration, client installation, key changes, or service restart is part of preparing this patch. Test on disposable PostgreSQL before deployment. Registry entries currently persist until their owner/key is deleted; deleting a key does not remove already snapshotted usage attribution.
