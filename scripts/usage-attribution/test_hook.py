import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

spec = importlib.util.spec_from_file_location('hook', Path(__file__).with_name('hook.py'))
hook = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hook)

class Tests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='attribution-', dir=os.environ.get('TMPDIR', '/var/tmp'))
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        subprocess.run(['git','init','-q',str(self.root)],check=True)
    def remote(self, remote):
        subprocess.run(['git','-C',str(self.root),'config','remote.origin.url',remote],check=True)
    def test_portable_identity_does_not_publish_remote(self):
        self.remote('https://secret:password@github.com/owner/repo.git?token=secret')
        a = hook.project(self.root,{})
        self.remote('git@github.com:owner/repo.git')
        self.assertEqual(a,hook.project(self.root,{}))
        self.assertNotIn('secret',json.dumps(a))
    def test_worktree_and_alias(self):
        subprocess.run(['git','-C',str(self.root),'-c','user.name=Synthetic','-c','user.email=test@example.invalid','commit','--allow-empty','-qm','init'],check=True)
        branch = self.root / 'branch'
        subprocess.run(['git','-C',str(self.root),'worktree','add','-qb','other',str(branch)],check=True)
        cfg={'projects':[{'path':str(self.root),'id':'portable-id','name':'Demo'}]}
        self.assertEqual(hook.project(branch,cfg),('portable-id','Demo'))
    def test_child_inherits_and_resume_retains_id(self):
        event={'hook_event_name':'SessionStart','session_id':'root-A','cwd':str(self.root)}
        first=hook.registration(event,'codex',{})
        event['source']='resume'
        self.assertEqual(first,hook.registration(event,'codex',{}))
        self.assertIs(first['preserve_existing'],True)
        self.assertTrue(first['task_name'].endswith('root-A'))
        child=hook.registration({'hook_event_name':'SubagentStart','session_id':'root-A','agent_id':'child-B'},'codex',{})
        self.assertEqual(child,{'client_kind':'codex','session_id':'child-B','parent_session_id':'root-A'})
    def test_no_prompt_or_transcript_in_payload(self):
        event={'hook_event_name':'SessionStart','session_id':'root-A','cwd':str(self.root),'transcript_path':'/never/read','prompt':'private prompt'}
        serialized=json.dumps(hook.registration(event,'codex',{}))
        self.assertNotIn('private',serialized)
        self.assertNotIn(str(self.root),serialized)
    def test_registration_no_redirect(self):
        hits=[]
        class Handler(BaseHTTPRequestHandler):
            def log_message(self,*args): pass
            def do_POST(self):
                hits.append(self.path)
                self.send_response(302)
                self.send_header('Location','http://127.0.0.1:1/leak')
                self.end_headers()
        server=ThreadingHTTPServer(('127.0.0.1',0),Handler)
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        self.addCleanup(server.server_close);self.addCleanup(server.shutdown)
        os.environ['SYNTHETIC_ATTRIBUTION_KEY']='fake'
        self.addCleanup(os.environ.pop,'SYNTHETIC_ATTRIBUTION_KEY')
        with self.assertRaises(hook.urllib.error.HTTPError):
            hook.send({'session_id':'A'},{'base_url':f'http://127.0.0.1:{server.server_port}','api_key_env':'SYNTHETIC_ATTRIBUTION_KEY'})
        self.assertEqual(hits,['/api/v1/usage-attribution/sessions'])
    def test_remote_http_rejected(self):
        with self.assertRaises(ValueError):
            hook.send({}, {'base_url':'http://example.com','api_key_env':'SYNTHETIC_ATTRIBUTION_KEY'})

    def test_existing_codex_credential_reference_and_permissions(self):
        source=self.root/'existing.toml'
        source.write_text('[model_providers.sub2api]\nexperimental_bearer_token="synthetic-only"\n')
        source.chmod(0o600)
        config={'credential_source':{'kind':'codex-config','path':str(source),'provider':'sub2api'}}
        self.assertEqual(hook.credential(config),'synthetic-only')
        source.chmod(0o644)
        with self.assertRaises(ValueError): hook.credential(config)
    def test_existing_claude_credential_reference(self):
        source=self.root/'existing.json'
        source.write_text(json.dumps({'env':{'ANTHROPIC_AUTH_TOKEN':'synthetic-only'}}))
        source.chmod(0o600)
        self.assertEqual(hook.credential({'credential_source':{'kind':'claude-settings','path':str(source)}}),'synthetic-only')
    def test_render_idempotent_and_private(self):
        import importlib.util
        spec=importlib.util.spec_from_file_location('configure',Path(__file__).with_name('configure.py'))
        configure=importlib.util.module_from_spec(spec);spec.loader.exec_module(configure)
        output=self.root/'staged'
        cfg={'base_url':'https://example.invalid/v1','api_key_env':'EXISTING_KEY'}
        files=configure.render(output,'codex',cfg)
        before={p:Path(p).read_bytes() for p in files}
        self.assertEqual(files,configure.render(output,'codex',cfg))
        self.assertEqual(before,{p:Path(p).read_bytes() for p in files})
        self.assertTrue(all(Path(p).stat().st_mode & 0o777 == 0o600 for p in files))
        self.assertEqual(output.stat().st_mode & 0o777,0o700)
    def test_parallel_payloads_do_not_share_project_or_task(self):
        from concurrent.futures import ThreadPoolExecutor
        def payload(n):
            return hook.registration({'hook_event_name':'SessionStart','session_id':str(n),'cwd':str(self.root)},'codex',{'projects':[{'path':str(self.root),'id':'project-'+str(n)}]})
        with ThreadPoolExecutor(max_workers=8) as pool:
            results=list(pool.map(payload,range(20)))
        for n,result in enumerate(results):
            self.assertEqual(result['project_id'],'project-'+str(n))
            self.assertEqual(result['task_id'],str(n))

if __name__ == '__main__': unittest.main()
