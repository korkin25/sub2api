#!/usr/bin/env python3
"""Register native client sessions; never read transcripts or mutate client headers."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import stat
import subprocess
import tomllib
import sys
import urllib.error
import urllib.parse
import urllib.request


def git(cwd, *args):
    result = subprocess.run(['git', '-C', str(cwd), *args], capture_output=True, text=True, timeout=5)
    return result.stdout.strip() if result.returncode == 0 else ''


def project(cwd, config):
    root = Path(git(cwd, 'rev-parse', '--show-toplevel') or cwd).resolve()
    common = git(cwd, 'rev-parse', '--path-format=absolute', '--git-common-dir')
    canonical = Path(common).parent.resolve() if common else root
    for item in config.get('projects', []):
        if canonical == Path(item['path']).expanduser().resolve() or root == Path(item['path']).expanduser().resolve():
            return item['id'], item.get('name', canonical.name)
    remote = git(cwd, 'remote', 'get-url', 'origin')
    # Keep only host/path, stripping credentials, query and protocol differences.
    if remote:
        if '://' not in remote and re.match(r'^[^/]+:', remote):
            remote = 'ssh://' + remote.replace(':', '/', 1)
        parsed = urllib.parse.urlsplit(remote)
        identity = (parsed.hostname or '').lower() + '/' + parsed.path.strip('/').removesuffix('.git')
        if not parsed.hostname:
            identity = str(canonical)
    else:
        identity = str(canonical)
    return 'repo-' + hashlib.sha256(identity.encode()).hexdigest()[:32], canonical.name


def registration(event, client, config):
    kind = event.get('hook_event_name')
    if kind not in ('SessionStart', 'SubagentStart'):
        return None
    session = event.get('session_id')
    if not isinstance(session, str) or not session or len(session) > 200:
        raise ValueError('invalid session ID')
    if kind == 'SubagentStart':
        if client == 'claude':
            # Claude subagent requests retain the root session in metadata.user_id.
            return None
        child = event.get('agent_id')
        if not isinstance(child, str) or not child or len(child) > 200:
            raise ValueError('invalid child ID')
        return dict(client_kind=client, session_id=child, parent_session_id=session)
    pid, name = project(event['cwd'], config)
    return dict(client_kind=client, session_id=session, preserve_existing=True, project_id=pid, project_name=name,
                task_id=session, task_name=client.title() + ' task ' + session[-8:],
                host=config.get('host', socket.gethostname()))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def credential(config):
    if config.get('api_key_env'):
        value = os.environ.get(config['api_key_env'])
        if value:
            return value
    source = config.get('credential_source', {})
    filename = source.get('path')
    if not filename:
        raise ValueError('configured API key unavailable')
    path = Path(filename).expanduser()
    info = path.stat()
    if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077:
        raise ValueError('credential source must be owner-only')
    if source.get('kind') == 'codex-config':
        provider = tomllib.loads(path.read_text())['model_providers'][source['provider']]
        if provider.get('env_key') and os.environ.get(provider['env_key']):
            return os.environ[provider['env_key']]
        value = provider.get('experimental_bearer_token')
    elif source.get('kind') == 'claude-settings':
        value = json.loads(path.read_text()).get('env', {}).get(source.get('field', 'ANTHROPIC_AUTH_TOKEN'))
    elif source.get('kind') == 'key-file':
        value = path.read_text().strip()
    else:
        raise ValueError('unsupported credential source')
    if not isinstance(value, str) or not value:
        raise ValueError('configured API key unavailable')
    return value


def send(payload, config):
    base = config['base_url'].rstrip('/').removesuffix('/v1')
    url = urllib.parse.urlsplit(base)
    if url.username or url.password or url.query or url.fragment:
        raise ValueError('base URL must not contain credentials/query/fragment')
    if url.scheme != 'https' and not (url.scheme == 'http' and url.hostname in ('127.0.0.1', 'localhost', '::1')):
        raise ValueError('HTTPS required except loopback')
    key = credential(config)
    req = urllib.request.Request(base + '/api/v1/usage-attribution/sessions',
        data=json.dumps(payload).encode(), headers={'Content-Type':'application/json','Authorization':'Bearer '+key}, method='POST')
    # Redirects must never disclose a key to another endpoint.
    with urllib.request.build_opener(NoRedirect).open(req, timeout=8) as response:
        if response.status not in (200,201,204):
            raise ValueError('registration rejected')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--client', choices=('codex','claude'), required=True)
    parser.add_argument('--config', type=Path, required=True)
    args = parser.parse_args()
    try:
        config = json.loads(args.config.read_text())
        event = json.load(sys.stdin)
        payload = registration(event, args.client, config)
        if payload:
            send(payload, config)
    except Exception:
        # Hook errors are advisory in some clients. Never claim fail-closed coverage.
        print('usage attribution registration failed; request may be unattributed', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
