"""Diagnostic only: isolate generated modules with reference companion modules."""
import json,subprocess,sys
from pathlib import Path
from grader import extract
H=Path(__file__).resolve().parent
reference={p.name:p.read_text() for p in (H/'reference').iterdir() if p.is_file()}
rows=[]
for directory in sorted((H/'artifacts').iterdir()):
 for stage,checks in [('ledger',['ledger_balances','ledger_validation','ledger_dedupe_atomicity','ledger_snapshot']),('outbox',['outbox_claims','outbox_fencing','outbox_dedupe_snapshot'])]:
  for p in directory.glob(stage+'-*-raw.json'):
   try:
    answer=json.loads(p.read_text())['result'];candidate=extract(answer)['files'];files={**reference,stage+'.py':candidate[stage+'.py']}
    proc=subprocess.run([sys.executable,str(H/'grader.py')],input=json.dumps({'files':files}),capture_output=True,text=True,timeout=12)
    graded=json.loads(proc.stdout);result={n:graded[n] for n in checks}
    if 'load_error' in graded:result['load_error']=graded['load_error']
   except Exception as e:result={'error':str(e)}
   rows.append({'trial':directory.name,'draft':p.name,'checks':result})
(H/'component-diagnostics.json').write_text(json.dumps(rows,indent=2)+'\n')
print(json.dumps(rows,indent=2))
