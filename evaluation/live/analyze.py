#!/usr/bin/env python3
"""Post-run tables and independent worker-vs-final grader audit."""
import collections, json, statistics, subprocess, sys, tempfile
from pathlib import Path
HERE=Path(__file__).resolve().parent;ROOT=HERE.parents[1];sys.path.insert(0,str(ROOT/'scripts'))
import evaluate_agents as ev
spec,raw=ev.load_spec(HERE/'benchmark.json');cases={c['id']:c for c in spec['cases']}
runs=[json.loads(p.read_text()) for p in sorted((HERE/'observations').glob('*.json'))]
rows=[];failures=[];reviews=[];sensitivity=[]
for case in spec['cases']:
 for mode in spec['modes']:
  rr=[r for r in runs if r['case_id']==case['id'] and r['mode']==mode]
  if not rr:continue
  rows.append({'case':case['id'],'category':case['category'],'mode':mode,'n':len(rr),'quality':statistics.mean(ev.quality_of(r,case)[0] for r in rr),'passes':sum(ev.quality_of(r,case)[1] for r in rr),'completed':sum(r['status']=='completed' for r in rr),'cost_usd':sum(ev.cost_of(r,spec) for r in rr),'mean_wall_seconds':statistics.mean(r['wall_seconds'] for r in rr),'median_wall_seconds':statistics.median(r['wall_seconds'] for r in rr),'input_tokens':sum(c['input_tokens'] for r in rr for c in r['model_calls']),'output_tokens':sum(c['output_tokens'] for r in rr for c in r['model_calls']),'complete_cost_runs':sum(r['cost_complete'] for r in rr)})
for r in runs:
 artifact=Path(r['metadata']['artifact']);case=cases[r['case_id']]
 if (artifact/'answer.txt').exists():
  answer=(artifact/'answer.txt').read_text();original_q=ev.quality_of(r,case)[0]
  try:
   # Post-hoc sensitivity: extract first JSON object; never modify primary scores.
   obj,end=json.JSONDecoder().raw_decode(answer[answer.index('{'):])
   with tempfile.TemporaryDirectory(prefix='comitia-format-audit-') as td:
    alt=subprocess.run([sys.executable,str(HERE/'grader.py'),r['case_id']],input=json.dumps(obj),text=True,capture_output=True,timeout=8,cwd=td)
   alt_evidence=json.loads(alt.stdout)
   alt_q=sum(c['points'] for c in case['quality_checks'] if alt_evidence.get('checks',{}).get(c['name'],{}).get('passed'))/sum(c['points'] for c in case['quality_checks'])
   (artifact/'extracted-json-grader.json').write_text(json.dumps(alt_evidence,indent=2)+'\n')
  except Exception:alt_q=original_q
  sensitivity.append({'case':r['case_id'],'mode':r['mode'],'replicate':r['replicate'],'primary_quality':original_q,'extracted_json_quality':alt_q,'artifact':str(artifact)})
 if not ev.quality_of(r,case)[1]:
  failures.append({'case':r['case_id'],'mode':r['mode'],'replicate':r['replicate'],'status':r['status'],'failed_checks':[q['name'] for q in r['quality'] if q['earned']!=next(c['points'] for c in case['quality_checks'] if c['name']==q['name'])],'artifact':str(artifact),'error':r.get('error')})
 if r['mode']!='single-agent' and (artifact/'worker-raw.json').exists():
  d=json.loads((artifact/'worker-raw.json').read_text())
  with tempfile.TemporaryDirectory(prefix='comitia-audit-') as td:
   p=subprocess.run([sys.executable,str(HERE/'grader.py'),r['case_id']],input=d.get('result',''),text=True,capture_output=True,timeout=8,cwd=td)
  evidence=json.loads(p.stdout);(artifact/'worker-grader.json').write_text(json.dumps(evidence,indent=2)+'\n')
  earned=sum(c['points'] for c in case['quality_checks'] if evidence.get('checks',{}).get(c['name'],{}).get('passed'));draftq=earned/sum(c['points'] for c in case['quality_checks']);finalq=ev.quality_of(r,case)[0]
  reviews.append({'case':r['case_id'],'mode':r['mode'],'replicate':r['replicate'],'draft_quality':draftq,'final_quality':finalq,'delta':finalq-draftq,'artifact':str(artifact)})
paired=[]
lookup={(r['case_id'],r['replicate'],r['mode']):r for r in runs}
for c in cases:
 for rep in range(spec['repetitions']):
  a=lookup.get((c,rep,'multi-agent'));b=lookup.get((c,rep,'comitia'))
  if a and b:paired.append({'case':c,'replicate':rep,'quality_delta':ev.quality_of(b,cases[c])[0]-ev.quality_of(a,cases[c])[0],'cost_delta_usd':ev.cost_of(b,spec)-ev.cost_of(a,spec) if a['cost_complete'] and b['cost_complete'] else None,'wall_delta_seconds':b['wall_seconds']-a['wall_seconds']})
transport={}
if paired:
 for key in ['quality_delta','cost_delta_usd','wall_delta_seconds']:
  values=[r[key] for r in paired if r[key] is not None]
  transport[key]={'mean':statistics.mean(values),'95ci':ev._bootstrap_ci(values,5000,spec['seed']+501),'n':len(values)} if values else None
 transport['coordination_seconds_mean']=statistics.mean(r['metadata']['comitia_wall_seconds'] for r in runs if r['mode']=='comitia')
 transport['coordination_operations_total']=sum(r['metadata']['comitia_operations'] for r in runs if r['mode']=='comitia')
analysis={'score_basis':'original-as-run; use audited-results.json for corrected comparisons','comitia_vs_multi_agent':transport,'rows':rows,'failures':failures,'review_deltas':reviews,'format_sensitivity':sensitivity}
(HERE/'analysis.json').write_text(json.dumps(analysis,indent=2)+'\n')
lines=['# Per-case real-model results (original as-run scores)' ,'','Original as-run scores before the separate score audit. See the main report for corrected comparisons. Every cell: five trials, same task and grader; quality is deterministic requirement compliance.','','| Category / case | Mode | Quality | Passes | Total USD | Mean seconds | Input / output tokens |','|---|---|---:|---:|---:|---:|---:|']
for x in rows:lines.append(f'| {x["category"]} / {x["case"]} | {x["mode"]} | {100*x["quality"]:.1f}% | {x["passes"]}/{x["n"]} | {x["cost_usd"]:.6f} | {x["mean_wall_seconds"]:.2f} | {x["input_tokens"]:,} / {x["output_tokens"]:,} |')
lines+=['','## Failed checks','']
for f in failures:lines.append(f'- {f["case"]}, {f["mode"]}, repetition {f["replicate"]}: {", ".join(f["failed_checks"])}. [Evidence]({f["artifact"]}/grader.json).')
if not failures:lines.append('None.')
lines+=['','## Worker/reviewer changes','']
for mode in ['multi-agent','comitia']:
 rr=[x for x in reviews if x['mode']==mode];lines.append(f'{mode}: {sum(x["delta"]>0 for x in rr)} improved, {sum(x["delta"]<0 for x in rr)} worsened, {sum(x["delta"]==0 for x in rr)} unchanged.')
for r in reviews:
 if r['delta']:lines.append(f'- {r["case"]}, {r["mode"]}, repetition {r["replicate"]}: {100*r["draft_quality"]:.1f}% → {100*r["final_quality"]:.1f}%.')
lines+=['','## Post-hoc format sensitivity','','Primary scores are unchanged. This secondary analysis extracts the first JSON object and applies the same checks, allowing trailing commentary.']
for mode in spec['modes']:
 rr=[x for x in sensitivity if x['mode']==mode]
 if rr:lines.append(f'{mode}: {100*statistics.mean(x["primary_quality"] for x in rr):.1f}% primary vs {100*statistics.mean(x["extracted_json_quality"] for x in rr):.1f}% extracted-JSON quality; {sum(x["primary_quality"]<x["extracted_json_quality"] for x in rr)} affected runs.')
(HERE/'per-case.md').write_text('\n'.join(lines)+'\n');print(json.dumps({'runs':len(runs),'failures':failures,'review_changes':[r for r in reviews if r['delta']]},indent=2))
