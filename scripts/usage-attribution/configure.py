#!/usr/bin/env python3
"""Render isolated, reviewable installation files. Never edit live client config."""
import argparse
import json
from pathlib import Path
import shlex


def render(output, client, config):
    output.mkdir(parents=True, exist_ok=True, mode=0o700)
    output.chmod(0o700)
    config_path = output / (client + '-attribution.json')
    hook = Path(__file__).with_name('hook.py').resolve()
    command = 'python3 ' + shlex.quote(str(hook)) + ' --client ' + client + ' --config ' + shlex.quote(str(config_path.resolve()))
    files = {config_path: json.dumps(config,indent=2)+'\n'}
    if client == 'codex':
        fragment=''
        for event in ('SessionStart','SubagentStart'):
            fragment+='[[hooks.'+event+']]\n[[hooks.'+event+'.hooks]]\ntype="command"\ncommand='+json.dumps(command)+'\ntimeout=15\n\n'
        files[output/'codex-hooks.toml']=fragment
    else:
        files[output/'claude-hooks.json']=json.dumps({'hooks':{'SessionStart':[{'hooks':[{'type':'command','command':command,'timeout':15}]}]}},indent=2)+'\n'
    for path,content in files.items():
        if path.is_symlink(): raise ValueError('refusing symbolic link output')
        path.write_text(content)
        path.chmod(0o600)
    return sorted(str(p) for p in files)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--client',choices=('codex','claude'),required=True)
    parser.add_argument('--config',type=Path,required=True,help='non-secret adapter config template')
    parser.add_argument('--out',type=Path,required=True,help='explicit staging directory, not client config directory')
    args=parser.parse_args()
    config=json.loads(args.config.read_text())
    # Config contains only references to existing credentials, never key literals.
    allowed={'base_url','api_key_env','credential_source','host','projects'}
    if set(config)-allowed: parser.error('unknown config fields')
    if args.out.resolve() in (Path.home()/'.codex',Path.home()/'.claude'):
        parser.error('use a staging directory; merge reviewed hook fragments separately')
    for path in render(args.out,args.client,config): print(path)

if __name__=='__main__':main()
