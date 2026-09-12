import hashlib,json,sys,time
from pathlib import Path
H=Path(__file__).resolve().parent;sys.path.insert(0,str(H.parents[1]/'scripts'))
import evaluate_agents as ev
path=H/'awake-control-spec.json';spec,raw=ev.load_spec(path);runs=[]
proof={n:hashlib.sha256((H/n).read_bytes()).hexdigest() for n in ['awake-control-spec.json','runner.py','storage.py','grader.py','run_awake_control.py']}
provenance=H/'awake-control-provenance.json'
if provenance.exists():
 if json.loads(provenance.read_text())['execution_hashes']!=proof:raise RuntimeError('control definitions changed')
else:ev.write_result(provenance,{'started_at':time.time(),'execution_hashes':proof})
for mode in spec['modes']:
 case=spec['cases'][0];dest=H/'awake-control-observations'/f'{case["id"]}--0--{mode}.json'
 if dest.exists():run=ev.validate_run(json.loads(dest.read_text()),spec)
 else:
  if (H/'awake-control-artifacts'/dest.stem).exists():raise RuntimeError('Inspect incomplete control artifacts before rerunning')
  run=ev.invoke_runner(str(H/'runner.py'),[],path,spec,case,mode,0);ev.write_result(dest,run)
 runs.append(run);print(json.dumps(dict(mode=mode,status=run['status'],quality=ev.quality_of(run,case)[0],wall_seconds=run['wall_seconds'])),flush=True)
result=ev.result_document(spec,raw,runs);ev.write_result(H/'awake-control-results.json',result);(H/'awake-control-results.md').write_text(ev.markdown_report(result))
