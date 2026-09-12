"""Two durable coordination adapters. File baseline includes explicit fencing."""
import fcntl,json,os,secrets,subprocess,time
from contextlib import contextmanager
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
STAGES=['architect','ledger','outbox','integrator','reviewer']
def atomic(path,value):
 path=Path(path);tmp=path.with_name(path.name+'.'+secrets.token_hex(4)+'.tmp')
 with tmp.open('w') as f:json.dump(value,f);f.flush();os.fsync(f.fileno())
 os.replace(tmp,path)
def log(path,value):
 with open(path,'a') as f:
  fcntl.flock(f,fcntl.LOCK_EX);f.write(json.dumps(value)+'\n');f.flush()
class FileStore:
 def __init__(self,root,audit,init=False):
  self.root=Path(root);self.audit=Path(audit)
  if init:atomic(self.root/'state.json',dict(tasks={n:dict(status='pending',version=0) for n in STAGES},messages=[],cursors={}))
 @contextmanager
 def state(self,op):
  start=time.monotonic()
  with open(self.root/'lock','a') as f:
   fcntl.flock(f,fcntl.LOCK_EX);s=json.loads((self.root/'state.json').read_text());yield s;atomic(self.root/'state.json',s)
  log(self.audit,{'kind':'storage','operation':op,'seconds':time.monotonic()-start,'backend':'files'})
 def claim(self,stage,actor,ttl=900):
  with self.state('claim') as s:
   t=s['tasks'][stage]
   if t['status']=='completed' or (t['status']=='leased' and t['expires']>time.time()):raise RuntimeError('conflict')
   t.update(status='leased',version=t['version']+1,actor=actor,expires=time.time()+ttl,token=secrets.token_hex(16))
   return dict(t)
 def put(self,stage,actor,lease,body):
  with self.state('publish-and-complete') as s:
   key=f'{stage}:{lease["version"]}';matches=[m for m in s['messages'] if m['key']==key]
   if matches:
    if matches[0]['body']!=body:raise RuntimeError('dedupe conflict')
   else:s['messages'].append(dict(key=key,stage=stage,version=lease['version'],body=body))
   t=s['tasks'][stage]
   good=t['status']=='leased' and t['token']==lease['token'] and t['version']==lease['version'] and t['actor']==actor and t['expires']>time.time()
   if good:t['status']='completed'
   return good
 def get(self,stage):
  with self.state('replay') as s:
   t=s['tasks'][stage]
   if t['status']!='completed':return None
   return next((m['body'] for m in s['messages'] if m['stage']==stage and m['version']==t['version']),None)
 def ack(self,actor):
  with self.state('ack') as s:s['cursors'][actor]=len(s['messages'])
 def dump(self):
  with self.state('inspect') as s:return {k:{'status':v['status'],'version':v['version']} for k,v in s['tasks'].items()}
class ComitiaStore:
 def __init__(self,root,audit,init=False):
  self.root=Path(root);self.audit=Path(audit)
  if init:
   self.cfg={'bootstrap':secrets.token_urlsafe(32)}
   self.cfg['owner']=self.call('bootstrap','--name','recovery-eval')['credential']['Token']
   ws=self.call('workspace-create','--name','recovery',actor='owner')['ID']
   self.cfg['ctx']=self.call('context-create','--workspace-id',ws,'--name','LIVE · recovery eval · worker replacement',actor='owner')['ID']
   for actor in STAGES+['replacement']:
    self.cfg[actor]=self.call('delegate','--name',actor,'--context-id',self.cfg['ctx'],'--permissions','read,write,claim',actor='owner')['credential']['Token']
   self.cfg['tasks']={stage:self.call('task-create','--context-id',self.cfg['ctx'],'--title',stage,actor='owner')['ID'] for stage in STAGES}
   self.cfg['sessions']={p['Name']:p['SessionID'] for p in self.call('participants','--context-id',self.cfg['ctx'],actor='owner')}
   atomic(self.root/'config.json',self.cfg)
  else:self.cfg=json.loads((self.root/'config.json').read_text())
 def call(self,cmd,*args,actor=None,allow_error=False):
  env=dict(os.environ,COMITIA_DB=str(self.root/'board.db'),COMITIA_BOOTSTRAP_CREDENTIAL=self.cfg['bootstrap'])
  env.pop('COMITIA_SERVER_URL',None);env.pop('COMITIA_CREDENTIAL',None)
  if actor:env['COMITIA_CREDENTIAL']=self.cfg[actor]
  start=time.monotonic();p=subprocess.run([str(ROOT/'bin/comitia'),cmd,*args],env=env,text=True,capture_output=True,timeout=20)
  log(self.audit,{'kind':'storage','backend':'comitia','operation':cmd,'seconds':time.monotonic()-start,'success':p.returncode==0})
  if p.returncode:
   if allow_error:return {'error':json.loads(p.stderr).get('error',p.stderr)}
   raise RuntimeError(cmd+': '+p.stderr)
  return json.loads(p.stdout)
 def claim(self,stage,actor,ttl=900):
  r=self.call('task-claim','--task-id',self.cfg['tasks'][stage],'--lease',str(ttl)+'s',actor=actor)
  return dict(token=r['Token'],version=r['Task']['LeaseVersion'])
 def put(self,stage,actor,lease,body):
  envelope=json.dumps(dict(stage=stage,version=lease['version'],body=body))
  # Publication alone is not authoritative: get() checks the completed generation.
  routes={'architect':['ledger','outbox','integrator','reviewer'],'ledger':['integrator'],'outbox':['integrator'],'integrator':['reviewer'],'reviewer':[]}
  meta={'schema':'comitia.discussion.v1','task_id':self.cfg['tasks'][stage],'recipient_session_ids':[self.cfg['sessions'][n] for n in routes[stage]],'payload':{}}
  ep=self.root/f'envelope-{stage}-{lease["version"]}.json';atomic(ep,meta)
  self.call('send','--context-id',self.cfg['ctx'],'--dedupe-key',f'{stage}:{lease["version"]}','--body',envelope,'--envelope-file',str(ep),actor=actor)
  r=self.call('task-complete','--task-id',self.cfg['tasks'][stage],'--claim-token',lease['token'],'--lease-version',str(lease['version']),actor=actor,allow_error=True)
  if 'error' in r and r['error'] not in ('stale task claim','conflict'):raise RuntimeError(str(r))
  return 'error' not in r
 def get(self,stage):
  t=self.call('task-get','--task-id',self.cfg['tasks'][stage],actor='owner')
  if t['Status']!='completed':return None
  page=self.call('replay','--context-id',self.cfg['ctx'],'--after','0','--limit','100',actor='owner')
  for m in page:
   envelope=json.loads(m['Body'])
   if envelope['stage']==stage and envelope['version']==t['LeaseVersion']:return envelope['body']
  return None
 def ack(self,actor):
  page=self.call('replay','--context-id',self.cfg['ctx'],'--after','0','--limit','100',actor=actor)
  if page:self.call('ack','--context-id',self.cfg['ctx'],'--sequence',str(page[-1]['Sequence']),actor=actor)
 def dump(self):
  tasks=self.call('task-list','--context-id',self.cfg['ctx'],actor='owner')
  return {t['Title']:{'status':t['Status'],'version':t['LeaseVersion']} for t in tasks}
def make_store(mode,root,audit,init=False):return (ComitiaStore if mode=='comitia' else FileStore)(root,audit,init)
