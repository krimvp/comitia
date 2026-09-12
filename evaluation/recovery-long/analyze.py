#!/usr/bin/env python3
import collections,hashlib,json,statistics,sys
from pathlib import Path
H=Path(__file__).resolve().parent;R=H.parents[1];sys.path.insert(0,str(R/'scripts'))
import evaluate_agents as ev
from grader import extract
control='--control' in sys.argv;prefix='awake-control-' if control else ''
spec,raw=ev.load_spec(H/('awake-control-spec.json' if control else 'benchmark.json'));runs=[json.loads(p.read_text()) for p in sorted((H/(prefix+'observations')).glob('*.json'))]
caseby={c['id']:c for c in spec['cases']};details=[]
evidence_path=H/(prefix+'timing-wall-evidence.json')
wall_evidence=json.loads(evidence_path.read_text()) if evidence_path.exists() else {}
for r in runs:
 out=Path(r['metadata']['artifact']);events=[json.loads(s) for s in (out/'events.jsonl').read_text().splitlines()]
 q,passed=ev.quality_of(r,caseby[r['case_id']])
 def read(name,default):return json.loads((out/name).read_text()) if (out/name).exists() else default
 grade=read('grader.json',{});integration=read('integration-checks.json',{})
 changed_files=None
 if (out/'final.json').exists() and (out/'integrator-0-raw.json').exists():
  try:
   oldfiles=extract(read('integrator-0-raw.json',{})['result'])['files'];newfiles=read('final.json',{})['files'];changed_files=sorted(n for n in set(oldfiles)|set(newfiles) if oldfiles.get(n)!=newfiles.get(n))
  except (ValueError,KeyError):pass
 stages=[]
 for p in sorted(out.glob('*-timing.json')):
  d=json.loads(p.read_text());key=str(p.relative_to(H));wall_evidence.setdefault(key,p.stat().st_mtime);d['civil_span_seconds']=wall_evidence[key]-d['started_at'];d['host_sleep_gap_seconds']=max(0,d['civil_span_seconds']-d['seconds']);rp=Path(str(p).replace('-timing.json','-raw.json'));rawcall=json.loads(rp.read_text()) if rp.exists() else {}
  stages.append({**d,'api_seconds':rawcall.get('duration_api_ms')/1000 if isinstance(rawcall.get('duration_api_ms'),(int,float)) else None,'visible_ttft_seconds':rawcall.get('ttft_ms')/1000 if isinstance(rawcall.get('ttft_ms'),(int,float)) else None,'stream_ttft_seconds':rawcall.get('ttft_stream_ms')/1000 if isinstance(rawcall.get('ttft_stream_ms'),(int,float)) else None,'thinking_tokens':sum(u['thinkingTokens'] for u in rawcall.get('modelUsage',{}).values()) if rawcall.get('modelUsage') and all('thinkingTokens' in u for u in rawcall['modelUsage'].values()) else None,'input_tokens':sum(u.get('inputTokens',0)+u.get('cacheReadInputTokens',0)+u.get('cacheCreationInputTokens',0) for u in rawcall.get('modelUsage',{}).values()),'output_tokens':sum(u.get('outputTokens',0) for u in rawcall.get('modelUsage',{}).values()),'cost_usd':rawcall.get('total_cost_usd',0)})
 restarts=[e for e in events if e['kind']=='injected_sigkill'];recoveries=[e for e in events if e['kind']=='recovered'];delayed=[e for e in events if e['kind']=='delayed_completion'];pids=[e['pid'] for e in events if e['kind']=='engine_start']
 requested=caseby[r['case_id']]['fault']!='none';lease=caseby[r['case_id']]['fault']=='lease'
 recovery_ok=bool(restarts and recoveries and delayed and len(set(pids))==2 and delayed[0]['authoritative_unchanged'] and (not lease or not delayed[0]['accepted'])) if requested else None
 overlap=0.
 if r['mode']!='single-agent':
  pairs={v['stage']:v for v in stages if v['stage'] in ('ledger','outbox') and v['attempt']==0}
  if len(pairs)==2:overlap=max(0,min(v['started_at']+v['seconds'] for v in pairs.values())-max(v['started_at'] for v in pairs.values()))
 resumed_request_times=[json.loads(p.read_text())['started_at'] for p in out.glob('*-request.json') if restarts and json.loads(p.read_text())['started_at']>=restarts[0]['at']]
 recovery_to_next_call=min(resumed_request_times)-restarts[0]['at'] if resumed_request_times else None
 details.append(dict(review_changed_files=changed_files,review_fixed_checks=[n['name'] for n in caseby[r['case_id']]['quality_checks'] if integration and not integration.get(n['name'],{}).get('passed') and grade.get(n['name'],{}).get('passed')],review_added_failures=[n['name'] for n in caseby[r['case_id']]['quality_checks'] if integration.get(n['name'],{}).get('passed') and grade and not grade.get(n['name'],{}).get('passed')],code_quality=sum(v['earned'] for v in r['quality'] if v['name']!='handoff')/11,handoff_pass=next(v['earned'] for v in r['quality'] if v['name']=='handoff')==1,thinking_tokens=sum(v['thinking_tokens'] or 0 for v in stages),thinking_coverage_calls=sum(v['thinking_tokens'] is not None for v in stages),model_calls_started=len(stages),host_sleep_affected=any(v['host_sleep_gap_seconds']>60 for v in stages),implementation_overlap_seconds=overlap,recovery_to_next_call_seconds=recovery_to_next_call,retained_work_tokens=sum(v['input_tokens']+v['output_tokens'] for v in stages if recoveries and v['stage'] in recoveries[0]['stages'] and v['attempt']==0),case=r['case_id'],replicate=r['replicate'],mode=r['mode'],status=r['status'],quality=q,passed=passed,wall_seconds=r['wall_seconds'],cost_complete=r['cost_complete'],cost_usd=r['metadata']['reported_cost_usd'],input_tokens=sum(c['input_tokens'] for c in r['model_calls']),output_tokens=sum(c['output_tokens'] for c in r['model_calls']),stages=stages,failed_checks={n:v.get('error') for n,v in grade.items() if not v.get('passed')},integration_score=sum(integration.get(n['name'],{}).get('passed',False) for n in caseby[r['case_id']]['quality_checks'])/12 if integration else None,storage_seconds=sum(e['seconds'] for e in events if e['kind']=='storage'),storage_operations=sum(e['kind']=='storage' for e in events),recovery_verified=recovery_ok,restarts=len(restarts),recovered_stages=recoveries[0]['stages'] if recoveries else [],delayed=delayed,error=r.get('error'),artifact=str(out),discarded_draft_cost_usd=sum(s['cost_usd'] for s in stages if lease and s['stage']=='ledger' and s['attempt']==0),discarded_draft_tokens=sum(s['input_tokens']+s['output_tokens'] for s in stages if lease and s['stage']=='ledger' and s['attempt']==0)))
def aggregate(rows):
 reviewed=[d for d in rows if d['status']=='completed' and d['integration_score'] is not None]
 return dict(review_no_file_changes=sum(d['review_changed_files']==[] for d in rows),mean_code_quality=statistics.mean(d['code_quality'] for d in rows),code_full_passes=sum(d['code_quality']==1 for d in rows),handoff_passes=sum(d['handoff_pass'] for d in rows),thinking_tokens=sum(d['thinking_tokens'] for d in rows),thinking_coverage_calls=sum(d['thinking_coverage_calls'] for d in rows),model_calls_started=sum(d['model_calls_started'] for d in rows),host_sleep_affected=sum(d['host_sleep_affected'] for d in rows),reviewer_improved=sum(d['quality']>d['integration_score'] for d in reviewed),reviewer_regressed=sum(d['quality']<d['integration_score'] for d in reviewed),reviewer_tied=sum(d['quality']==d['integration_score'] for d in reviewed),implementation_overlap_seconds=sum(d['implementation_overlap_seconds'] for d in rows),recovery_to_next_call_seconds=[d['recovery_to_next_call_seconds'] for d in rows if d['recovery_to_next_call_seconds'] is not None],retained_work_tokens=sum(d['retained_work_tokens'] for d in rows),n=len(rows),mean_quality=statistics.mean(d['quality'] for d in rows),passes=sum(d['passed'] for d in rows),completed=sum(d['status']=='completed' for d in rows),cost_usd=sum(d['cost_usd'] for d in rows),cost_complete=sum(d['cost_complete'] for d in rows),input_tokens=sum(d['input_tokens'] for d in rows),output_tokens=sum(d['output_tokens'] for d in rows),mean_wall_seconds=statistics.mean(d['wall_seconds'] for d in rows),mean_storage_seconds=statistics.mean(d['storage_seconds'] for d in rows),recovery_verified=sum(d['recovery_verified'] is True for d in rows),recovery_requested=sum(d['recovery_verified'] is not None for d in rows),discarded_draft_tokens=sum(d['discarded_draft_tokens'] for d in rows),discarded_draft_cost_usd=sum(d['discarded_draft_cost_usd'] for d in rows))
summary={m:aggregate([d for d in details if d['mode']==m]) for m in spec['modes'] if any(d['mode']==m for d in details)}
bycase={c['id']:{m:aggregate([d for d in details if d['case']==c['id'] and d['mode']==m]) for m in spec['modes'] if any(d['case']==c['id'] and d['mode']==m for d in details)} for c in spec['cases']}
result={'trials':details,'summary':summary,'by_case':bycase,'execution_hashes_unchanged':all(hashlib.sha256((H/name).read_bytes()).hexdigest()==digest for name,digest in json.loads((H/(prefix+'provenance.json')).read_text())['execution_hashes'].items())}
affected_pairs={(d['case'],d['replicate']) for d in details if d['host_sleep_affected']}
result['host_affected_pairs']=[list(p) for p in sorted(affected_pairs)]
complete_pairs={key for key in {(d['case'],d['replicate']) for d in details} if {d['mode'] for d in details if (d['case'],d['replicate'])==key}==set(spec['modes'])}
matched_pairs=complete_pairs-affected_pairs
result['matched_pairs']=[list(p) for p in sorted(matched_pairs)]
result['matched_without_host_affected_pairs']={m:aggregate([d for d in details if d['mode']==m and (d['case'],d['replicate']) in matched_pairs]) for m in spec['modes'] if matched_pairs}
bykey={(d['case'],d['replicate'],d['mode']):d for d in details}
comparisons={}
for i,(mode,baseline) in enumerate([('comitia','multi-agent'),('comitia','single-agent'),('multi-agent','single-agent')]):
 pairs=[(bykey[(case,rep,mode)],bykey[(case,rep,baseline)]) for case,rep in sorted(matched_pairs)]
 if not pairs:continue
 delta=[a['quality']-b['quality'] for a,b in pairs]
 comparisons[mode+' vs '+baseline]={'n_pairs':len(pairs),'mean_quality_difference':statistics.mean(delta),'quality_difference_95_ci':ev._bootstrap_ci(delta,spec['bootstrap_samples'],spec['seed']+i),'mean_elapsed_difference_seconds':statistics.mean(a['wall_seconds']-b['wall_seconds'] for a,b in pairs),'mean_captured_cost_difference_usd':statistics.mean(a['cost_usd']-b['cost_usd'] for a,b in pairs) if all(a['cost_complete'] and b['cost_complete'] for a,b in pairs) else None}
result['matched_paired_comparisons']=comparisons
ev.write_result(evidence_path,wall_evidence)
ev.write_result(H/(prefix+'analysis.json'),result)
lines=['# Extended recovery evaluation results','',f'{len(runs)} of {len(spec["cases"])*len(spec["modes"])*spec["repetitions"]} trials recorded. {spec["repetitions"]} repetitions per fault condition and mode. One multi-file engineering workload.','', '| Mode | Checks passed | Full passes | Completed | Captured cost | Cost coverage | Tokens in / out | Mean elapsed | Mean storage |', '|---|---:|---:|---:|---:|---:|---:|---:|---:|']
for m,d in summary.items():lines.append(f"| {m} | {d['mean_quality']:.1%} | {d['passes']}/{d['n']} | {d['completed']}/{d['n']} | ${d['cost_usd']:.4f} | {d['cost_complete']}/{d['n']} | {d['input_tokens']:,} / {d['output_tokens']:,} | {d['mean_wall_seconds']:.1f}s | {d['mean_storage_seconds']:.3f}s |")
lines+=['','## Individual trials','','| Fault | Rep | Mode | Checks | Status | Seconds | Captured cost | Recovery verified |','|---|---:|---|---:|---|---:|---:|---|']
for d in details:lines.append(f"| {caseby[d['case']]['fault']} | {d['replicate']} | {d['mode']} | {d['quality']:.1%} | {d['status']} | {d['wall_seconds']:.1f} | ${d['cost_usd']:.4f} | {d['recovery_verified']} |")
lines+=['','## Failure evidence','']
for d in details:
 if d['failed_checks'] or d['error']:lines+=['- '+f"{d['case']} / {d['replicate']} / {d['mode']}: "+json.dumps(d['failed_checks'] or d['error'])]
lines+=['','Recovery verification requires a new engine PID, recovered stages, and an unchanged authoritative result after retry/stale submission. A task can recover correctly and still fail behavioral checks later. Missing model timeout usage makes cost a lower bound. Full methodological limits are in README.md.','']
(H/(prefix+'analysis.md')).write_text('\n'.join(lines));print(json.dumps(summary,indent=2))
