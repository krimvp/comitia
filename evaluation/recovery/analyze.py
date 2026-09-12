#!/usr/bin/env python3
import collections,hashlib,json,statistics,sys
from pathlib import Path
H=Path(__file__).resolve().parent;R=H.parents[1];sys.path.insert(0,str(R/'scripts'))
import evaluate_agents as ev
spec,raw=ev.load_spec(H/'benchmark.json');runs=[json.loads(p.read_text()) for p in sorted((H/'observations').glob('*.json'))]
caseby={c['id']:c for c in spec['cases']};details=[]
for r in runs:
 out=Path(r['metadata']['artifact']);events=[json.loads(s) for s in (out/'events.jsonl').read_text().splitlines()]
 q,passed=ev.quality_of(r,caseby[r['case_id']])
 def read(name,default):return json.loads((out/name).read_text()) if (out/name).exists() else default
 grade=read('grader.json',{});integration=read('integration-checks.json',{})
 stages=[]
 for p in sorted(out.glob('*-timing.json')):
  d=json.loads(p.read_text());rp=Path(str(p).replace('-timing.json','-raw.json'));rawcall=json.loads(rp.read_text()) if rp.exists() else {}
  stages.append({**d,'input_tokens':sum(u.get('inputTokens',0)+u.get('cacheReadInputTokens',0)+u.get('cacheCreationInputTokens',0) for u in rawcall.get('modelUsage',{}).values()),'output_tokens':sum(u.get('outputTokens',0) for u in rawcall.get('modelUsage',{}).values()),'cost_usd':rawcall.get('total_cost_usd',0)})
 restarts=[e for e in events if e['kind']=='injected_sigkill'];recoveries=[e for e in events if e['kind']=='recovered'];delayed=[e for e in events if e['kind']=='delayed_completion'];pids=[e['pid'] for e in events if e['kind']=='engine_start']
 requested=caseby[r['case_id']]['fault']!='none';lease=caseby[r['case_id']]['fault']=='lease'
 recovery_ok=bool(restarts and recoveries and delayed and len(set(pids))==2 and delayed[0]['authoritative_unchanged'] and (not lease or not delayed[0]['accepted'])) if requested else None
 details.append(dict(case=r['case_id'],replicate=r['replicate'],mode=r['mode'],status=r['status'],quality=q,passed=passed,wall_seconds=r['wall_seconds'],cost_complete=r['cost_complete'],cost_usd=r['metadata']['reported_cost_usd'],input_tokens=sum(c['input_tokens'] for c in r['model_calls']),output_tokens=sum(c['output_tokens'] for c in r['model_calls']),stages=stages,failed_checks={n:v.get('error') for n,v in grade.items() if not v.get('passed')},integration_score=sum(integration.get(n['name'],{}).get('passed',False) for n in caseby[r['case_id']]['quality_checks'])/12 if integration else None,storage_seconds=sum(e['seconds'] for e in events if e['kind']=='storage'),storage_operations=sum(e['kind']=='storage' for e in events),recovery_verified=recovery_ok,restarts=len(restarts),recovered_stages=recoveries[0]['stages'] if recoveries else [],delayed=delayed,error=r.get('error'),artifact=str(out),discarded_draft_cost_usd=sum(s['cost_usd'] for s in stages if lease and s['stage']=='ledger' and s['attempt']==0),discarded_draft_tokens=sum(s['input_tokens']+s['output_tokens'] for s in stages if lease and s['stage']=='ledger' and s['attempt']==0)))
def aggregate(rows):
 return dict(n=len(rows),mean_quality=statistics.mean(d['quality'] for d in rows),passes=sum(d['passed'] for d in rows),completed=sum(d['status']=='completed' for d in rows),cost_usd=sum(d['cost_usd'] for d in rows),cost_complete=sum(d['cost_complete'] for d in rows),input_tokens=sum(d['input_tokens'] for d in rows),output_tokens=sum(d['output_tokens'] for d in rows),mean_wall_seconds=statistics.mean(d['wall_seconds'] for d in rows),mean_storage_seconds=statistics.mean(d['storage_seconds'] for d in rows),recovery_verified=sum(d['recovery_verified'] is True for d in rows),recovery_requested=sum(d['recovery_verified'] is not None for d in rows),discarded_draft_tokens=sum(d['discarded_draft_tokens'] for d in rows),discarded_draft_cost_usd=sum(d['discarded_draft_cost_usd'] for d in rows))
summary={m:aggregate([d for d in details if d['mode']==m]) for m in spec['modes'] if any(d['mode']==m for d in details)}
bycase={c['id']:{m:aggregate([d for d in details if d['case']==c['id'] and d['mode']==m]) for m in spec['modes'] if any(d['case']==c['id'] and d['mode']==m for d in details)} for c in spec['cases']}
result={'trials':details,'summary':summary,'by_case':bycase,'execution_hashes_unchanged':all(hashlib.sha256((H/name).read_bytes()).hexdigest()==digest for name,digest in json.loads((H/'provenance.json').read_text())['execution_hashes'].items())}
ev.write_result(H/'analysis.json',result)
lines=['# Extended recovery evaluation results','',f'{len(runs)} of 18 trials recorded. Two repetitions per fault condition and mode. One multi-file engineering workload, three fault profiles.','', '| Mode | Checks passed | Full passes | Completed | Captured cost | Cost coverage | Tokens in / out | Mean elapsed | Mean storage |', '|---|---:|---:|---:|---:|---:|---:|---:|---:|']
for m,d in summary.items():lines.append(f"| {m} | {d['mean_quality']:.1%} | {d['passes']}/{d['n']} | {d['completed']}/{d['n']} | ${d['cost_usd']:.4f} | {d['cost_complete']}/{d['n']} | {d['input_tokens']:,} / {d['output_tokens']:,} | {d['mean_wall_seconds']:.1f}s | {d['mean_storage_seconds']:.3f}s |")
lines+=['','## Individual trials','','| Fault | Rep | Mode | Checks | Status | Seconds | Captured cost | Recovery verified |','|---|---:|---|---:|---|---:|---:|---|']
for d in details:lines.append(f"| {caseby[d['case']]['fault']} | {d['replicate']} | {d['mode']} | {d['quality']:.1%} | {d['status']} | {d['wall_seconds']:.1f} | ${d['cost_usd']:.4f} | {d['recovery_verified']} |")
lines+=['','## Failure evidence','']
for d in details:
 if d['failed_checks'] or d['error']:lines+=['- '+f"{d['case']} / {d['replicate']} / {d['mode']}: "+json.dumps(d['failed_checks'] or d['error'])]
lines+=['','Recovery verification requires a new engine PID, recovered stages, and an unchanged authoritative result after retry/stale submission. A task can recover correctly and still fail behavioral checks later. Missing model timeout usage makes cost a lower bound. Full methodological limits are in README.md.','']
(H/'analysis.md').write_text('\n'.join(lines));print(json.dumps(summary,indent=2))
