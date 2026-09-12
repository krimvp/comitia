import json,statistics
from pathlib import Path
HERE=Path(__file__).resolve().parent
runs=[json.loads(p.read_text()) for p in (HERE/'observations').glob('*.json')];summary={}
for mode in ['single-agent','multi-agent','comitia']:
 records=[]
 for r in runs:
  if r['mode']!=mode:continue
  for stage in ('worker','reviewer'):
   p=Path(r['metadata']['artifact'])/(stage+'-raw.json')
   if not p.exists():continue
   d=json.loads(p.read_text());records.append({'stage':stage,'case':r['case_id'],'replicate':r['replicate'],'thinking_tokens':sum(u.get('thinkingTokens',0) for u in d.get('modelUsage',{}).values()),'output_tokens':sum(u.get('outputTokens',0) for u in d.get('modelUsage',{}).values()),'duration_api_ms':d.get('duration_api_ms'),'ttft_ms':d.get('ttft_ms'),'cost_usd':d.get('total_cost_usd'),'num_turns':d.get('num_turns')})
 thinking=sum(r['thinking_tokens'] for r in records);output=sum(r['output_tokens'] for r in records)
 summary[mode]={'returned_client_sessions':len(records),'returned_worker_sessions':sum(x['stage']=='worker' for x in records),'returned_reviewer_sessions':sum(x['stage']=='reviewer' for x in records),'reported_thinking_tokens':thinking,'output_tokens':output,'thinking_fraction_of_output':thinking/output if output else None,'mean_reported_api_seconds':statistics.mean(x['duration_api_ms']/1000 for x in records if x['duration_api_ms'] is not None) if records else None,'records':records}
(HERE/'client-metrics.json').write_text(json.dumps(summary,indent=2)+'\n')
print(json.dumps({k:{a:b for a,b in v.items() if a!='records'} for k,v in summary.items()},indent=2))
