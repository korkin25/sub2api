#!/usr/bin/env python3
"""Exercise two app-server WebSocket clients against a loopback Responses mock."""
import asyncio
import json
import logging
import os
import pathlib
import socket
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import websockets
from websockets.sync.server import serve


ROOT = pathlib.Path(__file__).resolve().parents[2]
CODEX = os.environ.get("CODEX_BIN", "codex")
PROVIDER_WS_REQUESTS = []
REGISTRATIONS = []
PROVIDER_PROTOCOL_ERRORS = []


class RegistrationReceiver(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        payload = json.loads(self.rfile.read(length))
        REGISTRATIONS.append({
            "at": time.monotonic(),
            "session_id": payload.get("session_id"),
            "project_id": payload.get("project_id"),
            "task_id": payload.get("task_id"),
        })
        self.send_response(200)
        self.end_headers()


def metadata_value(value):
    if isinstance(value, dict):
        return value
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
            return parsed if isinstance(parsed, dict) else {}
        except json.JSONDecodeError:
            return {}
    return {}


def frame_metadata(payload, connection_metadata):
    client_metadata = payload.get("client_metadata")
    client_metadata = client_metadata if isinstance(client_metadata, dict) else {}
    metadata = metadata_value(client_metadata.get("x-codex-turn-metadata"))
    if not metadata:
        metadata = dict(connection_metadata)
    metadata.setdefault("thread_id", client_metadata.get("thread_id"))
    metadata.setdefault("session_id", client_metadata.get("session_id"))
    return metadata


def provider_response(connection, ordinal):
    response_id = f"synthetic-{ordinal}"
    item = {"type": "message", "id": f"message-{ordinal}", "role": "assistant", "status": "completed", "content": [{"type": "output_text", "text": "OK"}]}
    events = [
        {"type": "response.created", "response": {"id": response_id, "status": "in_progress", "output": []}},
        {"type": "response.output_item.added", "output_index": 0, "item": item},
        {"type": "response.output_item.done", "output_index": 0, "item": item},
        {"type": "response.completed", "response": {"id": response_id, "status": "completed", "output": [item], "usage": {"input_tokens": 1, "input_tokens_details": {"cached_tokens": 0}, "output_tokens": 1, "total_tokens": 2}}},
    ]
    for event in events:
        connection.send(json.dumps(event))


def provider_ws(connection):
    connection_metadata = metadata_value(connection.request.headers.get("x-codex-turn-metadata", ""))
    ordinal = 0
    while True:
        try:
            payload = json.loads(connection.recv(timeout=20))
        except Exception:
            return
        if not isinstance(payload, dict):
            continue
        metadata = frame_metadata(payload, connection_metadata)
        PROVIDER_WS_REQUESTS.append({
            "at": time.monotonic(),
            "session_id": metadata.get("session_id"),
            "thread_id": metadata.get("thread_id"),
            "request_kind": metadata.get("request_kind", "unknown"),
            "frame_received": True,
        })
        ordinal += 1
        provider_response(connection, ordinal)


class ProviderLogHandler(logging.Handler):
    def emit(self, record):
        if record.levelno >= logging.ERROR:
            PROVIDER_PROTOCOL_ERRORS.append(record.getMessage())


def unused_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


async def rpc(ws, request_id, method, params):
    await ws.send(json.dumps({"id": request_id, "method": method, "params": params}))
    while True:
        message = json.loads(await asyncio.wait_for(ws.recv(), timeout=20))
        if message.get("id") == request_id:
            if "error" in message:
                raise RuntimeError(f"{method}: app-server returned an error")
            return message["result"]


async def one_client(url, label, cwd, project_id):
    async with websockets.connect(url) as ws:
        await rpc(ws, 1, "initialize", {"clientInfo": {"name": "usage-attribution-probe", "version": "1"}})
        await ws.send(json.dumps({"method": "initialized", "params": {}}))
        thread = await rpc(ws, 2, "thread/start", {"cwd": str(cwd)})
        thread_id = thread["thread"]["id"]
        await rpc(ws, 3, "turn/start", {
            "threadId": thread_id,
            "input": [{"type": "text", "text": f"synthetic {label}"}],
        })
        await wait_for_turn_completed(ws, thread_id)
        return thread_id, project_id


async def run(url, projects):
    return await asyncio.gather(
        one_client(url, "a", projects[0][0], projects[0][1]),
        one_client(url, "b", projects[1][0], projects[1][1]),
    )


async def wait_for_turn_completed(ws, thread_id):
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        message = json.loads(await asyncio.wait_for(ws.recv(), timeout=deadline - time.monotonic()))
        if message.get("method") != "turn/completed":
            continue
        params = message.get("params") or {}
        turn = params.get("turn") or {}
        if params.get("threadId") == thread_id or turn.get("thread_id") == thread_id or turn.get("threadId") == thread_id:
            return
    raise RuntimeError("app-server did not report turn completion")


async def trust_session_hook(url, cwds, hook_script):
    async with websockets.connect(url) as ws:
        await rpc(ws, 1, "initialize", {"clientInfo": {"name": "usage-attribution-probe", "version": "1"}})
        await ws.send(json.dumps({"method": "initialized", "params": {}}))
        listed = await rpc(ws, 2, "hooks/list", {"cwds": [str(cwd) for cwd in cwds]})
        hooks = [hook for item in listed.get("data", []) for hook in item.get("hooks", [])]
        hook = next(hook for hook in hooks if str(hook_script) in hook.get("command", ""))
        if hook.get("trustStatus") not in {"untrusted", "modified", "trusted"}:
            raise RuntimeError(f"unexpected hook trust state: {hook}")
        await rpc(ws, 3, "config/batchWrite", {
            "edits": [{"keyPath": "hooks.state", "value": {hook["key"]: {"trusted_hash": hook["currentHash"]}}, "mergeStrategy": "upsert"}],
            "filePath": None,
            "expectedVersion": None,
            "reloadUserConfig": True,
        })


def main():
    temp_root = pathlib.Path(os.environ.get("SUB2API_ATTRIBUTION_TMPDIR", os.environ.get("TMPDIR", "/var/tmp")))
    temp_root.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="sub2api-attribution-", dir=temp_root) as temporary:
        run_probe(pathlib.Path(temporary))


def run_probe(scratch):
    os.chmod(scratch, 0o700)
    home = scratch / "home"
    codex_home = scratch / "codex-home"
    home.mkdir()
    codex_home.mkdir()
    projects = []
    for suffix in ("a", "b"):
        project = scratch / f"project-{suffix}"
        project.mkdir()
        subprocess.run(["git", "init", "-q", str(project)], check=True)
        subprocess.run(["git", "-C", str(project), "remote", "add", "origin", f"https://example.invalid/usage-{suffix}.git"], check=True)
        projects.append((project, f"project-{suffix}"))
    hook_script = ROOT / "scripts" / "usage-attribution" / "hook.py"
    registration_server = ThreadingHTTPServer(("127.0.0.1", 0), RegistrationReceiver)
    threading.Thread(target=registration_server.serve_forever, daemon=True).start()
    hook_config = scratch / "hook.json"
    hook_config.write_text(json.dumps({"base_url": f"http://127.0.0.1:{registration_server.server_port}", "api_key_env": "SYNTHETIC_ATTRIBUTION_KEY", "projects": [{"path": str(cwd), "id": project_id} for cwd, project_id in projects]}))
    provider_logger = logging.getLogger(f"usage-attribution-provider-{os.getpid()}")
    provider_logger.propagate = False
    provider_logger.addHandler(ProviderLogHandler())
    mock = serve(provider_ws, "127.0.0.1", 0, logger=provider_logger)
    threading.Thread(target=mock.serve_forever, daemon=True).start()
    (codex_home / "config.toml").write_text(
        'model = "gpt-5.4"\nmodel_provider = "mock"\n'
        '[model_providers.mock]\nname = "mock"\n'
        f'base_url = "http://127.0.0.1:{mock.socket.getsockname()[1]}/v1"\n'
        'wire_api = "responses"\nrequires_openai_auth = false\nrequest_max_retries = 0\nsupports_websockets = true\n'
        '[features]\nresponses_websockets = true\n'
        '[[hooks.SessionStart]]\n[[hooks.SessionStart.hooks]]\n'
        f'type = "command"\ncommand = "python3 {hook_script} --client codex --config {hook_config}"\n'
    )
    port = unused_port()
    env = {"PATH": os.environ["PATH"], "HOME": str(home), "CODEX_HOME": str(codex_home), "TMPDIR": str(scratch), "SYNTHETIC_ATTRIBUTION_KEY": "synthetic-only"}
    server = subprocess.Popen([CODEX, "app-server", "--listen", f"ws://127.0.0.1:{port}"], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        url = f"ws://127.0.0.1:{port}"
        for _ in range(40):
            try:
                asyncio.run(trust_session_hook(url, [cwd for cwd, _ in projects], hook_script))
                threads = asyncio.run(run(url, projects))
                break
            except OSError:
                time.sleep(0.25)
        else:
            raise RuntimeError("app-server did not accept a loopback WebSocket")
    finally:
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait(timeout=10)
        server_stderr = server.stderr.read()
        server_stderr_error_lines = sum(
            1 for line in server_stderr.splitlines()
            if any(level in line.lower() for level in ("error", "fatal", "panic"))
        )
        mock.shutdown()
        registration_server.shutdown()
    thread_projects = dict(threads)
    if len(thread_projects) != 2:
        raise RuntimeError(f"threads collided: {threads}")
    framed = [row for row in PROVIDER_WS_REQUESTS if row.get("frame_received")]
    turns = [row for row in framed if row["request_kind"] == "turn"]
    prewarms = [row for row in framed if row["request_kind"] == "prewarm"]
    seen_threads = {row["thread_id"] for row in turns}
    if not set(thread_projects).issubset(seen_threads):
        raise RuntimeError(f"provider WebSocket metadata missed a thread: expected={len(thread_projects)} observed={len(seen_threads)}")
    if len(turns) < 2:
        raise RuntimeError(f"provider WebSocket turn frames missing: got={len(turns)}")
    registered_at = {row["session_id"]: row["at"] for row in REGISTRATIONS}
    for row in turns:
        session_id = row["session_id"]
        ws_time = row["at"]
        if session_id not in registered_at or registered_at[session_id] > ws_time:
            raise RuntimeError(f"registration did not precede provider WebSocket turn: session={session_id is not None}")
        if REGISTRATIONS[[registration["session_id"] for registration in REGISTRATIONS].index(session_id)]["project_id"] != thread_projects[row["thread_id"]]:
            raise RuntimeError("provider WebSocket turn was registered to another project")
    registrations_by_session = {row["session_id"]: row for row in REGISTRATIONS}
    registered_projects = {registrations_by_session[thread_id]["project_id"] for thread_id in thread_projects if thread_id in registrations_by_session}
    registered_tasks = {registrations_by_session[thread_id]["task_id"] for thread_id in thread_projects if thread_id in registrations_by_session}
    if registered_projects != set(thread_projects.values()) or len(registered_tasks) != 2:
        raise RuntimeError(f"real SessionStart hook did not retain distinct project/task roots: projects={len(registered_projects)} tasks={len(registered_tasks)}")
    if PROVIDER_PROTOCOL_ERRORS:
        raise RuntimeError(f"provider WebSocket fell back or retried over HTTP: protocol_errors={len(PROVIDER_PROTOCOL_ERRORS)}")
    prewarm_before_registration = sum(
        1 for row in prewarms
        if row["session_id"] not in registered_at or registered_at[row["session_id"]] > row["at"]
    )
    if server.returncode != 0 or server_stderr_error_lines:
        raise RuntimeError(f"app-server exited unexpectedly: returncode={server.returncode} stderr_error_lines={server_stderr_error_lines}")
    print(json.dumps({"status": "PASS", "threads": len(thread_projects), "provider_ws_threads": len(seen_threads), "projects": len(registered_projects), "prewarm_before_registration": prewarm_before_registration, "server_stderr_bytes": len(server_stderr), "server_stderr_error_lines": server_stderr_error_lines}, sort_keys=True))


if __name__ == "__main__":
    main()
