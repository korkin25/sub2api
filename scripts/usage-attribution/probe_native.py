#!/usr/bin/env python3
"""Optional native smoke proof. Loopback only; isolated homes and synthetic key.

Usage: TMPDIR=/var/tmp/... python3 probe_native.py --codex /absolute/codex --claude /absolute/claude
Never uses production client configuration or stores request content.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def run(binary, client, base, codex_mode="v2"):
    work = base / client
    work.mkdir(mode=0o700)
    for part in ('home','config','repo'):
        (work/part).mkdir(mode=0o700)
    events=[]
    bindings={}
    child_seen=threading.Event()
    class Handler(BaseHTTPRequestHandler):
        def log_message(self,*args): pass
        def do_POST(self):
            body=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            if self.path == '/api/v1/usage-attribution/sessions':
                events.append(('registration',body))
                session=body['session_id']
                existing=bindings.get(session)
                if existing and body.get('preserve_existing') and existing['client_kind']==body['client_kind']:
                    self.send_response(200);self.end_headers();self.wfile.write(b'{}');return
                parent=bindings.get(body.get('parent_session_id'))
                proposed=dict(parent,session_id=session,parent_session_id=body['parent_session_id']) if parent else dict(body)
                if existing and (existing['project_id'],existing['task_id'])!=(proposed['project_id'],proposed['task_id']):
                    self.send_response(409);self.end_headers();return
                bindings[session]=proposed
                self.send_response(200);self.end_headers();self.wfile.write(b'{}');return
            if client == 'codex':
                identity=json.loads(self.headers.get('x-codex-turn-metadata','{}')).get('thread_id')
            else:
                identity=json.loads(body.get('metadata',{}).get('user_id','{}')).get('session_id')
            events.append(('model',{'session_id':identity}))
            if client == 'claude':
                self.claude_response(body)
                return
            self.codex_response(body, identity)
        def codex_response(self, body, identity):
            count=sum(k=='model' for k,v in events)
            root=events[0][1]['session_id']
            if identity != root:
                child_seen.set()
            elif count >= 3:
                assert child_seen.wait(10), 'Codex child did not issue a request'
            if count == 1:
                item={'type':'tool_search_call','id':'tsc_1','call_id':'search-1','execution':'client','arguments':{'query':'spawn_agent','limit':2}}
            elif count == 2:
                arguments=({'message':'Synthetic child reply OK','task_name':'probe','fork_turns':'none'} if codex_mode=='v2'
                           else {'message':'Synthetic child reply OK','fork_context':False})
                item={'type':'function_call','id':'fc_1','call_id':'spawn-1','namespace':'collaboration' if codex_mode=='v2' else 'multi_agent_v1',
                      'name':'spawn_agent','arguments':json.dumps(arguments)}
            else:
                item={'type':'message','id':'msg_1','role':'assistant','status':'completed','content':[{'type':'output_text','text':'OK'}]}
            self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
            for kind,data in [('response.created',{'response':{'id':'resp_1','status':'in_progress','output':[]}}),
                              ('response.output_item.added',{'output_index':0,'item':item}),
                              ('response.output_item.done',{'output_index':0,'item':item}),
                              ('response.completed',{'response':{'id':'resp_1','status':'completed','output':[item],
                                'usage':{'input_tokens':1,'output_tokens':1,'total_tokens':2}}})]:
                data['type']=kind
                self.wfile.write(('event: '+kind+'\ndata: '+json.dumps(data)+'\n\n').encode())
        def claude_response(self, body):
            first=sum(k=='model' for k,v in events)==1
            block=({'type':'tool_use','id':'toolu_synthetic','name':'Agent','input':{'description':'Synthetic probe','prompt':'Reply OK','subagent_type':'general-purpose'}}
                   if first else {'type':'text','text':'OK'})
            message={'id':'msg_synthetic','type':'message','role':'assistant','model':'claude-sonnet-4-5','content':[block],
                     'stop_reason':'tool_use' if first else 'end_turn','stop_sequence':None,'usage':{'input_tokens':1,'output_tokens':1}}
            self.send_response(200)
            self.send_header('Content-Type','text/event-stream' if body.get('stream') else 'application/json')
            self.end_headers()
            if not body.get('stream'):
                self.wfile.write(json.dumps(message).encode());return
            def emit(kind,data):
                data['type']=kind
                self.wfile.write(('event: '+kind+'\ndata: '+json.dumps(data)+'\n\n').encode())
            emit('message_start',{'message':dict(message,content=[],stop_reason=None)})
            emit('content_block_start',{'index':0,'content_block':dict(block,input={}) if first else dict(block,text='')})
            delta={'type':'input_json_delta','partial_json':json.dumps(block['input'])} if first else {'type':'text_delta','text':'OK'}
            emit('content_block_delta',{'index':0,'delta':delta})
            emit('content_block_stop',{'index':0})
            emit('message_delta',{'delta':{'stop_reason':message['stop_reason'],'stop_sequence':None},'usage':{'output_tokens':1}})
            emit('message_stop',{})
    server=ThreadingHTTPServer(('127.0.0.1',0),Handler)
    threading.Thread(target=server.serve_forever,daemon=True).start()
    url=f'http://127.0.0.1:{server.server_port}'
    config=work/'adapter.json'
    config.write_text(json.dumps({'base_url':url,'api_key_env':'SYNTHETIC_ATTRIBUTION_KEY'}))
    import shlex
    command='python3 '+shlex.quote(str(Path(__file__).with_name('hook.py').resolve()))+' --client '+client+' --config '+shlex.quote(str(config))
    env={'PATH':'/usr/bin:/bin','HOME':str(work/'home'),'TMPDIR':str(base),'SYNTHETIC_ATTRIBUTION_KEY':'synthetic-only'}
    if client == 'codex':
        env['CODEX_HOME']=str(work/'config')
        (work/'config/config.toml').write_text('features.multi_agent_v2='+str(codex_mode=='v2').lower()+'\nmodel="gpt-5.4"\nmodel_provider="mock"\n[model_providers.mock]\nname="mock"\nbase_url='+json.dumps(url+'/v1')+'\nwire_api="responses"\nrequires_openai_auth=false\nrequest_max_retries=0\n[[hooks.SessionStart]]\n[[hooks.SessionStart.hooks]]\ntype="command"\ncommand='+json.dumps(command)+'\n[[hooks.SubagentStart]]\n[[hooks.SubagentStart.hooks]]\ntype="command"\ncommand='+json.dumps(command)+'\n')
        args=[binary,'exec','--dangerously-bypass-hook-trust','--skip-git-repo-check','-C',str(work/'repo'),'Reply OK']
    else:
        env.update(CLAUDE_CONFIG_DIR=str(work/'config'),ANTHROPIC_API_KEY='synthetic-only',ANTHROPIC_BASE_URL=url,CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1')
        settings=work/'settings.json'
        settings.write_text(json.dumps({'hooks':{'SessionStart':[{'hooks':[{'type':'command','command':command}]}]}}))
        args=[binary,'-p','Reply OK','--settings',str(settings),'--setting-sources','','--model','claude-sonnet-4-5','--max-turns','4','--allowedTools','Agent','--output-format','json']
    try:
        result=subprocess.run(args,env=env,cwd=work/'repo',capture_output=True,text=True,timeout=45)
        models=[v for k,v in events if k=='model']
        registrations=[v for k,v in events if k=='registration']
        assert models and registrations, f'{client}: missing registration/model event'
        assert events[0][0]=='registration', f'{client}: registration did not precede model'
        if client == 'claude':
            assert all(m['session_id']==registrations[0]['session_id'] for m in models)
        else:
            children=[r for r in registrations if r.get('parent_session_id')]
            assert len(children)==1 and children[0]['parent_session_id']==registrations[0]['session_id']
            assert len({m['session_id'] for m in models})==2, 'Codex child request missing'
            registered=set()
            for kind,value in events:
                if kind=='registration': registered.add(value['session_id'])
                else: assert value['session_id'] in registered, 'request preceded its registration'
            assert all('task_id' not in r for r in registrations if r['session_id']==children[0]['session_id']), 'child unexpectedly registered as root'
        root=registrations[0]['session_id']
        if client == 'claude':
            summaries=[json.loads(line) for line in result.stdout.splitlines() if line.startswith('{')]
            assert any(x.get('subagent_stats',{}).get('spawned',0)>=1 for x in summaries), 'native child not spawned'
            resume_args=args+['--resume',root]
        else:
            resume_args=[binary,'exec','resume','--dangerously-bypass-hook-trust','--skip-git-repo-check',root,'Reply OK']
        checkpoint=len(events)
        subprocess.run(resume_args,env=env,cwd=work/'repo',capture_output=True,text=True,timeout=45)
        resumed=events[checkpoint:]
        assert resumed and resumed[0][0]=='registration', f'{client}: resume registration missing'
        assert any(k=='model' for k,v in resumed), f'{client}: resume request missing'
        assert all(v['session_id']==root for k,v in resumed), f'{client}: resume identity changed'
        if client == 'codex':
            child=children[0]['session_id']
            checkpoint=len(events)
            child_args=[binary,'exec','resume','--dangerously-bypass-hook-trust','--skip-git-repo-check',child,'Reply OK']
            child_result=subprocess.run(child_args,env=env,cwd=work/'repo',capture_output=True,text=True,timeout=45)
            child_resume=events[checkpoint:]
            if codex_mode=='v2':
                assert not child_resume and child_result.returncode != 0
                assert 'cannot resume an unloaded multi-agent v2 sub-agent' in child_result.stderr
                child_resume_surface='native rejects unloaded child; resume root instead'
            else:
                assert any(k=='model' and v['session_id']==child for k,v in child_resume), 'child resume request absent'
                child_resume_surface='native child resume retains inherited binding'
            assert bindings[child]['task_id']==root, 'child resume split root task'
            # Future/alternate hosts may deliver SessionStart on a child. Exercise
            # the actual hook against the registry mock, separate from native proof.
            replay=subprocess.run(shlex.split(command),input=json.dumps({'hook_event_name':'SessionStart','source':'resume',
                                  'session_id':child,'cwd':str(work/'repo')}),env=env,capture_output=True,text=True,timeout=15)
            assert replay.returncode==0 and bindings[child]['task_id']==root, 'child SessionStart split root task'
        else:
            child_resume_surface='root session shared by native child'
        print(json.dumps({'client':client,'status':'PASS','surface':'native HTTP + resume + native child' + (' '+codex_mode if client=='codex' else ''),
                          'model_requests':sum(k=='model' for k,v in events),'registration_before_model':True,'child_resume':child_resume_surface}))

    finally:
        server.shutdown();server.server_close()


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--codex')
    parser.add_argument('--claude')
    parser.add_argument('--codex-mode',choices=('v1','v2'),default='v2')
    args=parser.parse_args()
    if not args.codex and not args.claude:
        parser.error('provide at least one absolute native binary path')
    temp=os.environ.get('TMPDIR','/var/tmp')
    if not str(Path(temp).resolve()).startswith('/var/tmp/') and str(Path(temp).resolve())!='/var/tmp':
        parser.error('TMPDIR must be under /var/tmp')
    with tempfile.TemporaryDirectory(prefix='native-attribution-',dir=temp) as directory:
        for client,binary in (('codex',args.codex),('claude',args.claude)):
            if binary:
                if not Path(binary).is_absolute(): parser.error('binary path must be absolute')
                run(binary,client,Path(directory),args.codex_mode)

if __name__=='__main__':main()
