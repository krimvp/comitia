#!/usr/bin/env python3
"""Sequential counterbalanced execution using the repository's real evaluator API."""
import json, sys, time
from pathlib import Path
HERE=Path(__file__).resolve().parent;ROOT=HERE.parents[1];sys.path.insert(0,str(ROOT/'scripts'))
import evaluate_agents as ev
specpath=HERE/'benchmark.json';spec,raw=ev.load_spec(specpath);runs=[];started=time.monotonic()
for rep in range(spec['repetitions']):
 for ci,case in enumerate(spec['cases']):
  offset=(ci+rep)%3;modes=spec['modes'][offset:]+spec['modes'][:offset]
  for mode in modes:
   path=HERE/'observations'/f'{case["id"]}--{rep}--{mode}.json'
   if path.exists():run=ev.validate_run(json.loads(path.read_text()),spec,expected_key=(mode,case['id'],rep))
   else:
    run=ev.invoke_runner(str(HERE/'runner.py'),[],specpath,spec,case,mode,rep);ev.write_result(path,run)
   runs.append(run);q,p=ev.quality_of(run,case)
   print(json.dumps({'n':len(runs),'total':len(spec['cases'])*spec['repetitions']*3,'case':case['id'],'mode':mode,'replicate':rep,'status':run['status'],'quality':q,'pass':p,'cost_complete':run['cost_complete'],'wall_seconds':run['wall_seconds'],'elapsed_seconds':round(time.monotonic()-started,1)}),flush=True)
result=ev.result_document(spec,raw,runs);ev.write_result(HERE/'results.json',result);(HERE/'results.md').write_text(ev.markdown_report(result))
