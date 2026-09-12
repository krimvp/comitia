#!/usr/bin/env python3
"""Regrade saved answers consistently; preserve original scores and no model reruns.

Audited: correct architecture entity capitalization (not specified by task).
Content sensitivity: additionally allow extracting JSON from surrounding prose.
"""
import copy, hashlib, json, subprocess, sys, tempfile
from pathlib import Path
HERE=Path(__file__).resolve().parent;ROOT=HERE.parents[1];sys.path.insert(0,str(ROOT/'scripts'))
import evaluate_agents as ev
from grader import parse

def assess(case,answer,content=False):
 corrections=[]
 try:
  if content:
   obj,end=json.JSONDecoder().raw_decode(answer[answer.index('{'):]);corrections.append('extract_first_json_object')
  else:obj=parse(answer)
  if case['id']=='outbox-architecture' and isinstance(obj.get('atomic_writes'),list):
   before=obj['atomic_writes'];obj['atomic_writes']=[x.lower() if isinstance(x,str) else x for x in before]
   if before!=obj['atomic_writes']:corrections.append('case_insensitive_entity_names')
   aliases={'outboxmessage':'outbox','outbox_message':'outbox','outboxentry':'outbox','outbox_entry':'outbox'}
   normalized=[aliases.get(x,x) if isinstance(x,str) else x for x in obj['atomic_writes']]
   if normalized!=obj['atomic_writes']:corrections.append('equivalent_outbox_entity_name')
   obj['atomic_writes']=normalized
  payload=json.dumps(obj)
 except Exception:payload=answer
 with tempfile.TemporaryDirectory(prefix='comitia-audited-grader-') as td:
  p=subprocess.run([sys.executable,str(HERE/'grader.py'),case['id']],input=payload,text=True,capture_output=True,timeout=8,cwd=td)
 evidence=json.loads(p.stdout)
 quality=[{'name':c['name'],'earned':c['points'] if evidence.get('checks',{}).get(c['name'],{}).get('passed') else 0} for c in case['quality_checks']]
 return quality,{'corrections':corrections,**evidence}

def audit_spec(spec, raw, label):
 revised=copy.deepcopy(spec)
 for case in revised['cases']:
  if case['id']=='delegation-schedule':
   case['quality_checks']=[c for c in case['quality_checks'] if c['name']!='handoff']
   case['pass_score']=sum(c['points'] for c in case['quality_checks'])
 revised['metadata']['score_audit']={'label':label,'original_spec_sha256':hashlib.sha256(raw).hexdigest(),'excluded_check':'delegation-schedule/handoff','reason':'Prompt ambiguous between schema field names and concrete values; original grader also accepts dict keys without checking values. Excluded uniformly, original observations preserved.','capitalization':'Architecture entity names case-insensitive.'}
 return revised

def main():
 spec,raw=ev.load_spec(HERE/'benchmark.json');cases={c['id']:c for c in spec['cases']};source=[json.loads(p.read_text()) for p in sorted((HERE/'observations').glob('*.json'))]
 changes=[];reviews=[]
 for label,content in [('audited',False),('content-sensitivity',True)]:
  results=[];scoring_spec=audit_spec(spec,raw,label)
  specpath=HERE/(label+'-benchmark.json');ev.write_result(specpath,scoring_spec);scoring_spec,scoring_raw=ev.load_spec(specpath)
  scoring_cases={c['id']:c for c in scoring_spec['cases']}
  for r in source:
   revised=copy.deepcopy(r);case=scoring_cases[r['case_id']];artifact=Path(r['metadata']['artifact']);names={c['name'] for c in case['quality_checks']};revised['quality']=[q for q in revised['quality'] if q['name'] in names]
   if r['status']=='completed' and (artifact/'answer.txt').exists():
    quality,evidence=assess(case,(artifact/'answer.txt').read_text(),content)
    (artifact/(label+'-grader.json')).write_text(json.dumps(evidence,indent=2)+'\n')
    if r['mode']!='single-agent' and (artifact/'worker-raw.json').exists():
     worker=json.loads((artifact/'worker-raw.json').read_text()).get('result','')
     wq,we=assess(case,worker,content)
     total=sum(c['points'] for c in case['quality_checks'])
     reviews.append({'label':label,'case':r['case_id'],'mode':r['mode'],'replicate':r['replicate'],'worker_quality':sum(c['earned'] for c in wq)/total,'final_quality':sum(c['earned'] for c in quality)/total,'worker_checks':wq,'final_checks':quality})
     (artifact/(label+'-worker-grader.json')).write_text(json.dumps(we,indent=2)+'\n')
    revised['quality']=quality;revised['metadata']['score_audit']={'original_quality':r['quality'],'evidence':str(artifact/(label+'-grader.json')),'label':label}
    if evidence.get('error'):revised['error']=evidence['error']
    else:revised.pop('error',None)
    if quality!=r['quality']:changes.append({'label':label,'case':r['case_id'],'mode':r['mode'],'replicate':r['replicate'],'original_quality':ev.quality_of(r,cases[r['case_id']])[0],'revised_quality':ev.quality_of(revised,case)[0],'evidence':str(artifact/(label+'-grader.json'))})
   ev.write_result(HERE/(label+'-observations')/f'{r["case_id"]}--{r["replicate"]}--{r["mode"]}.json',revised);results.append(revised)
  if len(results)==len(cases)*spec['repetitions']*3:
   result=ev.result_document(scoring_spec,scoring_raw,results);ev.write_result(HERE/(label+'-results.json'),result);(HERE/(label+'-results.md')).write_text(ev.markdown_report(result))
 (HERE/'audited-review-deltas.json').write_text(json.dumps(reviews,indent=2)+'\n')
 (HERE/'score-corrections.json').write_text(json.dumps(changes,indent=2)+'\n');print(json.dumps(changes,indent=2))
if __name__=='__main__':main()
