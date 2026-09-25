# Client usage attribution

Status: prepared adapter and isolated native HTTP, resume and Codex/Claude subagent proofs. This does not install hooks,
restart clients, or prove deployed server acceptance. Python 3.11+ is required.

The existing API key remains unchanged. Synchronous SessionStart registers the native
session before the observed HTTP inference turn requests at `POST /api/v1/usage-attribution/sessions`.
Codex SubagentStart registers `agent_id` with `parent_session_id`; the server inherits
project and root task within the authenticated API key. No process-global project
headers, shell environment mutations, prompt reading, or transcript reading are used.

## Contract

Registration authenticates with the existing gateway API key. Root payload:

```json
{"client_kind":"codex","session_id":"native-thread-id","project_id":"repo-stable-id","project_name":"project","task_id":"native-root-id","task_name":"Codex task short-id","host":"origin-host","preserve_existing":true}
```

Child payload: `client_kind`, `session_id` (child ID), `parent_session_id`.
Registration is scoped to the authenticated user and API key. Repeating a binding
must preserve existing editable names; changing a bound project/task returns 409.
SessionStart sends `preserve_existing: true`: an existing same-client binding is
retained atomically, including its parent/root task, even if cwd-derived defaults
change on resume. SubagentStart never sets that flag and retains strict lineage.
A missing parent is an error, never an invitation to create an unrelated root task.

Codex 0.156.1 sends the current thread as JSON field `thread_id` in
`x-codex-turn-metadata`. Its SessionStart `session_id` matches this field.
Claude Code 2.1.280 sends a JSON **string** in body `metadata.user_id`; its
`session_id` matches SessionStart. These facts were observed using actual installed
clients against loopback mocks, not inferred from model output. Native resume retained
the same identity in both clients. A real Claude Agent invocation completed a child:
all root/child/continuation requests retained the root session ID, so Claude only
needs SessionStart registration. Native Codex V1 and V2 tool discovery/spawn were
also exercised: SubagentStart registers the child before its first observed HTTP inference turn request;
no child SessionStart fires during spawn. V1 standalone child resume retains the
child identity and inherited root without another SessionStart. V2 rejects resume
of an unloaded child (`cannot resume an unloaded multi-agent v2 sub-agent`); resume
the parent instead. A separately replayed child SessionStart against the registry
mock verifies `preserve_existing` retains its inherited task. This replay is a
contract check, distinct from the native lifecycle observations.

## Project identity and resume

The adapter hashes the normalized Git origin host/path locally. SSH and HTTPS URLs,
`.git` suffixes, credentials and URL query parameters do not change that identity.
The remote URL is never sent to the server. Display name defaults to the common
Git checkout basename; linked worktrees use the common Git directory. No origin:
identity falls back to the common checkout path, which is **not portable** between
machines. Explicit aliases cover that case and repository moves/remote aliases:

```json
{
  "base_url": "https://gateway.example/v1",
  "api_key_env": "EXISTING_SUB2API_KEY",
  "projects": [{"path": "/local/project", "id": "stable-project-id", "name": "Project"}]
}
```

Use the same explicit ID on each host; paths remain local. New dialogue IDs create
new tasks. Resume and compact reuse the existing ID; generic initial titles can be
renamed in the server UI without a later hook resetting the name. Original host is
recorded by registration; server resume policy determines whether it changes.

Credentials can alternatively reference an existing owner-only file, avoiding a
second stored secret. `credential_source` examples (choose one):

```json
{"kind":"codex-config","path":"~/.codex/config.toml","provider":"sub2api"}
{"kind":"claude-settings","path":"~/.claude/settings.json","field":"ANTHROPIC_AUTH_TOKEN"}
{"kind":"key-file","path":"/owner-only/existing-key"}
```

Codex lookup uses the selected provider's `env_key` when available, otherwise its
`experimental_bearer_token`. Claude prefers `api_key_env` if configured and present;
the explicit settings source is a fallback. Files with non-owner permissions or a
different owner are rejected. Neither errors nor generated config print credentials.
HTTPS is required except loopback mocks, and redirects are rejected.

## Prepare installation without changing running clients

Save a non-secret adapter JSON config, then render reviewed fragments:

```sh
python3 scripts/usage-attribution/configure.py --client codex --config /path/adapter.json --out /path/staging
python3 scripts/usage-attribution/configure.py --client claude --config /path/adapter.json --out /path/staging
```

Rendering is idempotent and produces owner-only files. Keep the adapter path stable.
Merge the generated hook entries with existing client hooks rather than replacing
config files. Codex hooks additionally require trust review through `/hooks`; the
probe's bypass flag applies **only to isolated synthetic sessions**. Do not enable
async hooks: registration must finish before observed inference turns; provider-WS prewarm can precede it. No client provider settings
or API key names are changed. Shared daemon integration requires native concurrency
and trust proof before rollout.

## Verification

```sh
TMPDIR=/var/tmp/attribution-test python3 -m unittest discover -s scripts/usage-attribution -p 'test_*.py' -v
TMPDIR=/var/tmp/attribution-test python3 scripts/usage-attribution/probe_native.py --codex /absolute/codex --claude /absolute/claude
```

Create the temporary directory mode 0700 first. Native probe isolates HOME/config,
uses a synthetic key, sends only to loopback, records only identity/registration,
and removes its generated state. Both clients receive synthetic SSE tool responses that run one native child.
Codex exercises its native tool_search and spawn_agent (both V1 and V2). Both clients then resume the
synthetic dialogue and identity is checked again. Add `--codex-mode v1` to exercise
the legacy native subagent path; the default is V2. No paid model endpoint is used.

Remaining proof boundaries must stay explicit: the first-request probe alone does
not establish WebSocket transport, shared daemon thread isolation, or deployed gateway acceptance.
Those surfaces require the separate shared-daemon/WS acceptance harness. That native
provider-WS probe observed a prewarm request **before** SessionStart registration.
Prewarm cannot be covered by this hook ordering guarantee: if billed without a
registration, it remains Unknown. No process-global pre-thread workaround is used.
The native CLI HTTP proof does not establish an all-transport first-request guarantee. A hook failure is advisory in some clients: the
adapter reports failure but cannot promise to block inference. Such usage must stay
unattributed, never be guessed from a process-global last-used project.

Sources: [Codex hooks](https://developers.openai.com/codex/hooks),
[Claude hooks](https://code.claude.com/docs/en/hooks).
