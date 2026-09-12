#!/usr/bin/env python3
"""Real Claude CLI adapter. Comitia transports the exact worker answer to reviewer."""
import argparse, json, os, secrets, subprocess, sys, tempfile, time
from pathlib import Path
HERE=Path(__file__).resolve().parent; ROOT=HERE.parents[1]
SYSTEM='Complete the provided bounded task. Return only the requested JSON object, no Markdown fences. Do not use tools. Do not claim tests were executed. Keep explanations concise.'

def main():
 ap=argparse.ArgumentParser()
 for k in ('spec','mode','case','replicate','seed'):ap.add_argument('--eval-'+k,required=True)
 args=ap.parse_args();spec=json.loads(Path(args.eval_spec).read_text());case=next(c for c in spec['cases'] if c['id']==args.eval_case)
 runid=f'{args.eval_case}--{args.eval_replicate}--{args.eval_mode}';out=HERE/'artifacts'/runid;out.mkdir(parents=True,exist_ok=True)
 calls=[];cli_cost=0.;complete=True;events=[];model_seconds=0.;comitia_seconds=0.;responses=[];ledgers=[]
 def model(prompt,stage):
  nonlocal cli_cost,complete,model_seconds
  env=dict(os.environ,DISABLE_PROMPT_CACHING='1',CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1')
  for k in ('COMITIA_CREDENTIAL','COMITIA_BOOTSTRAP_CREDENTIAL','COMITIA_DB','COMITIA_SERVER_URL'):env.pop(k,None)
  with tempfile.TemporaryDirectory(prefix='comitia-model-') as wd:
   command=['claude','-p','--model',spec['metadata']['model'],'--output-format','json','--tools','','--strict-mcp-config','--mcp-config','{"mcpServers":{}}','--disable-slash-commands','--no-session-persistence','--setting-sources','','--settings','{"disableAllHooks":true}','--system-prompt',SYSTEM]
   (out/(stage+'-prompt.txt')).write_text(prompt)
   t=time.monotonic()
   p=subprocess.run(command,input=prompt,capture_output=True,text=True,cwd=wd,env=env,timeout=100)
   elapsed=time.monotonic()-t;model_seconds+=elapsed
  (out/(stage+'-raw.json')).write_text(p.stdout);(out/(stage+'-stderr.txt')).write_text(p.stderr)
  d=json.loads(p.stdout);ledgers.append(d);cli_cost+=d.get('total_cost_usd',0)
  if not d.get('modelUsage'):complete=False
  for name,u in d.get('modelUsage',{}).items():
   if name not in spec['pricing_usd_per_million_tokens']:complete=False;continue
   creation=u.get('cacheCreationInputTokens',0)
   call={'model':name,'input_tokens':u['inputTokens']+u.get('cacheReadInputTokens',0)+creation,'cached_input_tokens':u.get('cacheReadInputTokens',0),'output_tokens':u['outputTokens']}
   calls.append(call)
   if creation:complete=False
  if p.returncode or d.get('is_error') or d.get('subtype')!='success':raise RuntimeError('model execution failed: '+str(d.get('result',d.get('subtype'))))
  events.append({'stage':stage,'kind':'model','wall_seconds':elapsed,'reported_cost_usd':d.get('total_cost_usd'),'model_usage':d.get('modelUsage')})
  return d['result']
 try:
  with tempfile.TemporaryDirectory(prefix='comitia-trial-') as td:
   env=dict(os.environ,COMITIA_DB=td+'/trial.db',COMITIA_BOOTSTRAP_CREDENTIAL=secrets.token_urlsafe(32));env.pop('COMITIA_SERVER_URL',None);env.pop('COMITIA_CREDENTIAL',None)
   def board(command,*values,credential=None):
    nonlocal comitia_seconds
    e=dict(env)
    if credential:e['COMITIA_CREDENTIAL']=credential
    t=time.monotonic();p=subprocess.run([str(ROOT/'bin/comitia'),command,*values],env=e,capture_output=True,text=True,timeout=15);elapsed=time.monotonic()-t;comitia_seconds+=elapsed
    if p.returncode:raise RuntimeError('Comitia '+command+': '+p.stderr)
    result=json.loads(p.stdout)
    # Do not preserve credentials or claim tokens in exported artifacts.
    events.append({'kind':'comitia','command':command,'wall_seconds':elapsed,'success':True})
    return result
   if args.eval_mode=='comitia':
    owner=board('bootstrap','--name','eval-owner')['credential']['Token'];ws=board('workspace-create','--name',runid,credential=owner)
    ctx=board('context-create','--workspace-id',ws['ID'],'--name',case['id'],credential=owner)['ID']
    workers=[board('delegate','--name',n,'--context-id',ctx,'--permissions','read,write,claim',credential=owner)['credential']['Token'] for n in ('worker','reviewer')]
    task=board('task-create','--context-id',ctx,'--title',case['id'],'--body',case['task'],credential=owner)
    lease=board('task-claim','--task-id',task['ID'],'--lease','5m',credential=workers[0])
   draft=model(case['task'],'worker');responses.append(draft)
   if args.eval_mode!='single-agent':
    if args.eval_mode=='comitia':
     sent=board('send','--context-id',ctx,'--dedupe-key','worker-final','--body',draft,credential=workers[0])
     page=board('replay','--context-id',ctx,'--after','0','--limit','100',credential=workers[1]);assert len(page)==1 and page[0]['Body']==draft
     draft=page[0]['Body'];board('ack','--context-id',ctx,'--sequence',str(sent['Sequence']),credential=workers[1])
     board('task-complete','--task-id',task['ID'],'--claim-token',lease['Token'],'--lease-version',str(lease['Task']['LeaseVersion']),credential=workers[0])
     reviewtask=board('task-create','--context-id',ctx,'--title','review '+case['id'],credential=owner)
     reviewlease=board('task-claim','--task-id',reviewtask['ID'],'--lease','5m',credential=workers[1])
    final=model('Independently review the worker answer against the task. Correct any defects you find. Return the final requested JSON, with no review commentary.\n\nTASK:\n'+case['task']+'\n\nWORKER ANSWER:\n'+draft,'reviewer');responses.append(final)
    if args.eval_mode=='comitia':
     board('send','--context-id',ctx,'--dedupe-key','reviewer-final','--body',final,credential=workers[1])
     board('task-complete','--task-id',reviewtask['ID'],'--claim-token',reviewlease['Token'],'--lease-version',str(reviewlease['Task']['LeaseVersion']),credential=workers[1])
     tasks=board('task-list','--context-id',ctx,credential=owner);assert len(tasks)==2 and all(t['Status']=='completed' for t in tasks)
   else:final=draft
   (out/'answer.txt').write_text(final)
   p=subprocess.run([sys.executable,str(HERE/'grader.py'),case['id']],input=final,text=True,capture_output=True,timeout=8,cwd=td)
   evidence=json.loads(p.stdout);(out/'grader.json').write_text(json.dumps(evidence,indent=2)+'\n')
   quality=[{'name':c['name'],'earned':c['points'] if evidence.get('checks',{}).get(c['name'],{}).get('passed') else 0} for c in case['quality_checks']]
   status='completed';error=evidence.get('error')
 except Exception as exc:
  status='timeout' if isinstance(exc,subprocess.TimeoutExpired) else 'failed';error=str(exc);complete=False
  quality=[{'name':c['name'],'earned':0} for c in case['quality_checks']]
 computed=sum((c['input_tokens']-c['cached_input_tokens'])*spec['pricing_usd_per_million_tokens'][c['model']]['input']/1e6+c['cached_input_tokens']*spec['pricing_usd_per_million_tokens'][c['model']]['cached_input']/1e6+c['output_tokens']*spec['pricing_usd_per_million_tokens'][c['model']]['output']/1e6 for c in calls)
 if abs(computed-cli_cost)>1e-7:complete=False
 (out/'events.json').write_text(json.dumps(events,indent=2)+'\n')
 result={'status':status,'quality':quality,'model_calls':calls,'non_model_cost_usd':0,'cost_complete':complete,'metadata':{'artifact':str(out),'category':case['category'],'model_wall_seconds':model_seconds,'comitia_wall_seconds':comitia_seconds,'comitia_operations':sum(e['kind']=='comitia' for e in events),'cli_reported_cost_usd':cli_cost,'computed_cost_usd':computed,'model_sessions':len(ledgers)}}
 if error:result['error']=error
 (out/'observation.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result))
if __name__=='__main__':main()
