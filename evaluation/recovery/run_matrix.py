#!/usr/bin/env python3
"""Balanced matrix through the existing evaluator, persisted one cell at a time."""
import hashlib,json,sys,time
from pathlib import Path
H=Path(__file__).resolve().parent;R=H.parents[1];sys.path.insert(0,str(R/'scripts'))
import evaluate_agents as ev
if (H/'SERIES-STOPPED.md').exists():
 raise SystemExit('This calibration series is closed. Use evaluation/recovery-long; see SERIES-STOPPED.md.')
specpath=H/'benchmark.json';spec,raw=ev.load_spec(specpath)
files=['benchmark.json','task.md','runner.py','storage.py','grader.py']
hashes={name:hashlib.sha256((H/name).read_bytes()).hexdigest() for name in files}
provenance=H/'provenance.json'
if provenance.exists():
 if json.loads(provenance.read_text())['execution_hashes']!=hashes:raise RuntimeError('execution files changed; use a new experiment directory')
else:ev.write_result(provenance,{'execution_hashes':hashes,'started_at':time.time(),'notes':'Fixed before first live matrix cell. No fixtures are part of live scores.'})
runs=[];start=time.monotonic()
for rep in range(spec['repetitions']):
 for ci,case in enumerate(spec['cases']):
  offset=(ci+rep)%3
  for mode in spec['modes'][offset:]+spec['modes'][:offset]:
   path=H/'observations'/f'{case["id"]}--{rep}--{mode}.json'
   if path.exists():run=ev.validate_run(json.loads(path.read_text()),spec,expected_key=(mode,case['id'],rep))
   else:
    artifact=H/'artifacts'/path.stem
    if artifact.exists():raise RuntimeError('Unrecorded artifacts exist; inspect before retrying '+str(artifact))
    run=ev.invoke_runner(str(H/'runner.py'),[],specpath,spec,case,mode,rep);ev.write_result(path,run)
   runs.append(run);q,passed=ev.quality_of(run,case)
   print(json.dumps(dict(n=len(runs),total=18,case=case['id'],mode=mode,replicate=rep,status=run['status'],quality=q,passed=passed,wall_seconds=run['wall_seconds'],cost_complete=run['cost_complete'],elapsed_seconds=time.monotonic()-start)),flush=True)
result=ev.result_document(spec,raw,runs);ev.write_result(H/'results.json',result);(H/'results.md').write_text(ev.markdown_report(result))
