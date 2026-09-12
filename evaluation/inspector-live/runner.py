#!/usr/bin/env python3
"""Five-stage model workflow, externally SIGKILLed and restarted at fault barriers."""
from contextlib import nullcontext
import argparse,json,os,signal,subprocess,sys,tempfile,time,traceback
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from storage import atomic,log,make_store,STAGES
from grader import extract,CHECKS
H=Path(__file__).resolve().parent
SYSTEM='Solve the supplied engineering task. Return the requested JSON object. No tools are available. Treat prior agent drafts as proposals to verify against the specification. Never claim you executed tests. Keep plans concise; code must be complete.'

def model(prompt,stage,cfg):
 out=Path(cfg['out']);n=len(list(out.glob(stage+'-*-request.json')));prefix=out/f'{stage}-{n}'
 atomic(str(prefix)+'-request.json',{'prompt':prompt,'stage':stage,'started_at':time.time()})
 start=time.monotonic();env=dict(os.environ,DISABLE_PROMPT_CACHING='1',CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1')
 for k in list(env):
  if k.startswith('COMITIA_'):env.pop(k)
 meta={'stage':stage,'attempt':n,'started_at':time.time(),'cost_complete':False}
 try:
  if cfg.get('fixture'):
   files={p.name:p.read_text() for p in (H/'reference').iterdir() if p.is_file()}
   answer=json.dumps({'plan':'fixture'} if stage=='architect' else {'files':files})
   raw={'result':answer,'modelUsage':{},'total_cost_usd':0,'subtype':'success'};meta['fixture']=True
  else:
   cmd=['claude','-p','--model',cfg['model'],'--output-format','json','--tools','','--strict-mcp-config','--mcp-config','{"mcpServers":{}}','--disable-slash-commands','--no-session-persistence','--setting-sources','','--settings','{"disableAllHooks":true}','--system-prompt',SYSTEM]
   with tempfile.TemporaryDirectory(prefix='recovery-model-') as td:
    p=subprocess.Popen(cmd,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,cwd=td,env=env,start_new_session=True)
    try:stdout,stderr=p.communicate(prompt,timeout=cfg['timeout'])
    except subprocess.TimeoutExpired:
     os.killpg(p.pid,signal.SIGKILL);stdout,stderr=p.communicate()
     Path(str(prefix)+'-partial.txt').write_text(stdout);Path(str(prefix)+'-stderr.txt').write_text(stderr)
     raise TimeoutError('model call exceeded '+str(cfg['timeout'])+'s')
   Path(str(prefix)+'-stderr.txt').write_text(stderr);Path(str(prefix)+'-raw.txt').write_text(stdout)
   raw=json.loads(stdout)
   # Save returned usage even if provider reports a failed result.
   atomic(str(prefix)+'-raw.json',raw)
   if p.returncode or raw.get('is_error') or raw.get('subtype')!='success':raise RuntimeError('model failure '+str(raw.get('subtype')))
  atomic(str(prefix)+'-raw.json',raw);meta['cost_complete']=bool(raw.get('modelUsage')) or cfg.get('fixture',False)
  return raw['result']
 except Exception as e:meta['error']=type(e).__name__+': '+str(e);raise
 finally:
  meta['seconds']=time.monotonic()-start;atomic(str(prefix)+'-timing.json',meta)
  log(out/'events.jsonl',dict(kind='model',**meta))

def grade(answer):
 p=subprocess.run([sys.executable,str(H/'grader.py')],input=answer,capture_output=True,text=True,timeout=12)
 if p.returncode:raise RuntimeError('grader failed '+p.stderr)
 return json.loads(p.stdout)

def engine(config):
 cfg=json.loads(Path(config).read_text());out=Path(cfg['out']);store=make_store(cfg['mode'],cfg['state'],out/'events.jsonl');fault=cfg['fault'];task=cfg['task']
 recovered=(Path(cfg['state'])/'restarted').exists();log(out/'events.jsonl',{'kind':'engine_start','pid':os.getpid(),'resumed':recovered})
 prior={n:store.get(n) for n in STAGES};prior={n:v for n,v in prior.items() if v is not None}
 if recovered:log(out/'events.jsonl',{'kind':'recovered','stages':list(prior)})
 deferred=[]
 def run(stage,instruction,dependencies):
  if stage in prior:
   store.ack(stage);return prior[stage]
  actor='replacement' if stage=='ledger' and recovered and fault=='lease' else stage
  interrupted=stage=='ledger' and fault=='lease' and not recovered
  lease=store.claim(stage,actor,.01 if interrupted else 900)
  shared={n:prior[n] for n in dependencies if n in prior}
  if cfg['mode']=='single-agent':shared=dict(prior)
  identity='You are the same engineer continuing your staged implementation.' if cfg['mode']=='single-agent' else 'You are the '+stage+' agent in a five-role engineering team.'
  prompt=identity+'\n\nSPECIFICATION:\n'+task+'\n\nYOUR STAGE:\n'+instruction+'\n\nPREVIOUS WORK:\n'+json.dumps(shared)
  answer=model(prompt,stage,cfg)
  if interrupted:
   atomic(Path(cfg['state'])/'delayed.json',dict(stage=stage,actor=actor,lease=lease,body=answer));deferred.append(stage)
   # This result is deliberately not published or exposed to resumed prompts.
   return None
  if not store.put(stage,actor,lease,answer):raise RuntimeError('current completion rejected')
  if stage=='architect' and fault=='handoff' and not recovered:
   atomic(Path(cfg['state'])/'delayed.json',dict(stage=stage,actor=actor,lease=lease,body=answer))
   crash_barrier(cfg,'published-before-ack')
  store.ack(stage);prior[stage]=answer;return answer
 run('architect','Design module interfaces, invariants, rollback strategy, and crash recovery in at most 350 words. Return {"plan":"..."}; do not implement files.',[])
 implementations=[('ledger','Implement only ledger.py. Return {"files":{"ledger.py":"complete code"}}.', ['architect']),('outbox','Implement only outbox.py. Return {"files":{"outbox.py":"complete code"}}.', ['architect'])]
 if cfg['mode']=='single-agent':
  for args in implementations:run(*args)
 else:
  with ThreadPoolExecutor(max_workers=2) as pool:
   jobs=[pool.submit(run,*args) for args in implementations]
   errors=[]
   for job in jobs:
    try:job.result()
    except Exception as e:errors.append(e)
   if errors:raise errors[0]
 if deferred:crash_barrier(cfg,'worker-result-before-publish')
 if recovered:
  delayed=json.loads((Path(cfg['state'])/'delayed.json').read_text())
  authoritative=store.get(delayed['stage'])
  accepted=store.put(**delayed)
  after=store.get(delayed['stage'])
  # Replay identical publication after an ambiguous acknowledgement; completed
  # task need not accept completion twice, but the authoritative result must survive.
  log(out/'events.jsonl',{'kind':'delayed_completion','fault':fault,'accepted':accepted,'authoritative_unchanged':authoritative==after})
  if fault=='lease' and (accepted or after!=authoritative):raise RuntimeError('stale result corrupted replacement')
  if fault=='handoff' and after!=authoritative:raise RuntimeError('handoff retry corrupted result')
 run('integrator','Integrate the two implementations with service.py and HANDOFF.md. Resolve defects and interface mismatches. Return all four complete files in the files mapping.', ['architect','ledger','outbox'])
 evidence=grade(prior['integrator']);atomic(out/'integration-checks.json',evidence)
 public={n:evidence.get(n) for n in ['ledger_balances','outbox_claims','service_ingest','service_crash_delivery']}
 if 'load_error' in evidence:public['load_error']=evidence['load_error']
 run('reviewer','Review and repair the complete implementation. Check all requirements, including aliasing, atomicity, and stale ownership. Return all four final files. Public smoke-test feedback (other checks remain hidden):\n'+json.dumps(public),['architect','integrator'])
 final=store.get('reviewer');atomic(out/'final.json',extract(final));(out/'answer.txt').write_text(final)
 atomic(out/'grader.json',grade(final));atomic(out/'tasks.json',store.dump())
 atomic(out/'engine-result.json',{'status':'completed'})

def crash_barrier(cfg,label):
 marker=Path(cfg['state'])/'fault-ready.json';atomic(marker,{'label':label,'pid':os.getpid(),'at':time.time()})
 # Parent observes this durable test signal, kills this process, and starts a new
 # interpreter. No model calls are in flight at these precisely defined barriers.
 os.kill(os.getpid(),signal.SIGSTOP)
 raise RuntimeError('stopped process unexpectedly continued')

def main():
 if len(sys.argv)>1 and sys.argv[1]=='--engine':
  try:engine(sys.argv[2])
  except Exception as e:
   cfg=json.loads(Path(sys.argv[2]).read_text());atomic(Path(cfg['out'])/'engine-result.json',{'status':'timeout' if isinstance(e,TimeoutError) else 'failed','error':type(e).__name__+': '+str(e),'trace':traceback.format_exc()});raise
  return
 ap=argparse.ArgumentParser()
 for n in ('spec','mode','case','replicate','seed'):ap.add_argument('--eval-'+n,required=True)
 a=ap.parse_args();spec=json.loads(Path(a.eval_spec).read_text());case=next(c for c in spec['cases'] if c['id']==a.eval_case)
 out=Path(spec['metadata'].get('artifact_root',str(H/'artifacts')))/f'{a.eval_case}--{a.eval_replicate}--{a.eval_mode}';out.mkdir(parents=True,exist_ok=True)
 status='failed';error=None;restarts=0;start=time.monotonic()
 try:
  with nullcontext(str(H/'state')) as td:
   Path(td).mkdir(mode=0o700,exist_ok=False)
   store=make_store(a.eval_mode,td,out/'events.jsonl',True)
   cfg=dict(mode=a.eval_mode,state=td,out=str(out),fault=case['fault'],task=case['task'],model=spec['metadata']['model'],timeout=spec['metadata']['model_timeout_seconds'],fixture=spec['metadata'].get('fixture_only',False))
   config=Path(td)/'engine-config.json';atomic(config,cfg)
   while True:
    with (out/f'engine-{restarts}-stdout.txt').open('w') as stdout,(out/f'engine-{restarts}-stderr.txt').open('w') as stderr:
     p=subprocess.Popen([sys.executable,str(H/'runner.py'),'--engine',str(config)],stdout=stdout,stderr=stderr,start_new_session=True)
     killed=False
     while p.poll() is None:
      marker=Path(td)/'fault-ready.json'
      if marker.exists() and not restarts:
       data=json.loads(marker.read_text());os.killpg(p.pid,signal.SIGKILL);p.wait();restarts+=1;killed=True
       log(out/'events.jsonl',{'kind':'injected_sigkill','old_pid':p.pid,'label':data['label'],'at':time.time()})
       (Path(td)/'restarted').touch();break
      time.sleep(.1)
     if killed:continue
    result=json.loads((out/'engine-result.json').read_text());status=result['status'];error=result.get('error');break
 except Exception as e:error=type(e).__name__+': '+str(e)
 calls=[];cost=0.;complete=True;requests=list(out.glob('*-request.json'))
 for req in requests:
  prefix=str(req)[:-len('-request.json')];rawpath=Path(prefix+'-raw.json');timing=Path(prefix+'-timing.json')
  if not timing.exists() or not json.loads(timing.read_text()).get('cost_complete'):complete=False
  if not rawpath.exists():complete=False;continue
  raw=json.loads(rawpath.read_text());cost+=raw.get('total_cost_usd',0)
  for name,u in raw.get('modelUsage',{}).items():
   if name not in spec['pricing_usd_per_million_tokens']:complete=False;continue
   cached=u.get('cacheReadInputTokens',0);creation=u.get('cacheCreationInputTokens',0)
   calls.append(dict(model=name,input_tokens=u['inputTokens']+cached+creation,cached_input_tokens=cached,output_tokens=u['outputTokens']))
   if creation:complete=False
 computed=sum((c['input_tokens']-c['cached_input_tokens'])*spec['pricing_usd_per_million_tokens'][c['model']]['input']/1e6+c['cached_input_tokens']*spec['pricing_usd_per_million_tokens'][c['model']]['cached_input']/1e6+c['output_tokens']*spec['pricing_usd_per_million_tokens'][c['model']]['output']/1e6 for c in calls)
 if abs(computed-cost)>1e-7:complete=False
 evidence=json.loads((out/'grader.json').read_text()) if (out/'grader.json').exists() else {}
 quality=[dict(name=c['name'],earned=c['points'] if status=='completed' and evidence.get(c['name'],{}).get('passed') else 0) for c in case['quality_checks']]
 obs=dict(status=status,quality=quality,model_calls=calls,non_model_cost_usd=0,cost_complete=complete,metadata=dict(artifact=str(out),restarts=restarts,model_sessions=len(requests),reported_cost_usd=cost,computed_cost_usd=computed,total_seconds=time.monotonic()-start))
 if error:obs['error']=error
 atomic(out/'observation.json',obs);print(json.dumps(obs))
if __name__=='__main__':main()
