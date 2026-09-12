"""Post-run diagnostic: exercise rollback after reserve, capture, and refund.

The fixed primary grader tests the reserve-collision case. These extra cases do
not alter primary scores or feed back to evaluated model sessions.
"""
import json,os,subprocess,sys,tempfile
import sys
from pathlib import Path
H=Path(__file__).resolve().parent
prefix="awake-control-" if "--control" in sys.argv else ""
PROGRAM='''
import json
from service import Service
def event(i,k,a):return dict(id=i,order_id='order',kind=k,amount=a)
results={}
for kind in ['reserve','capture','refund']:
 try:
  s=Service()
  if kind!='reserve':s.ingest(event('reserve','reserve',100))
  if kind=='refund':s.ingest(event('capture','capture',70))
  s.outbox.enqueue('collision',{'existing':'notification'})
  before=json.loads(json.dumps(s.snapshot()))
  e=event('collision',kind,20 if kind=='refund' else 30)
  try:s.ingest(e)
  except ValueError:pass
  else:raise AssertionError('enqueue collision did not raise ValueError')
  assert s.snapshot()==before, 'rollback changed state'
  if kind=='reserve':
   try:s.ledger.balances('order')
   except KeyError:pass
   else:raise AssertionError('failed reserve left order')
  else:
   b=s.ledger.balances('order')
   assert b['captured']==(70 if kind=='refund' else 0) and b['refunded']==0, str(b)
  results[kind]={'passed':True}
 except Exception as e:results[kind]={'passed':False,'error':type(e).__name__+': '+str(e)}
for exception_type in [ValueError, RuntimeError]:
 for after_enqueue in [False, True]:
  name='injected_'+exception_type.__name__+('_after_enqueue' if after_enqueue else '_before_enqueue')
  try:
   s=Service();before=json.loads(json.dumps(s.snapshot()));original=s.outbox.enqueue
   def fail(key,payload):
    if after_enqueue:original(key,payload)
    raise exception_type('injected enqueue failure')
   s.outbox.enqueue=fail
   try:s.ingest(event('injected','reserve',30))
   except exception_type:pass
   else:raise AssertionError('exception not propagated')
   assert s.snapshot()==before,'rollback changed state'
   results[name]={'passed':True}
  except Exception as e:results[name]={'passed':False,'error':type(e).__name__+': '+str(e)}
print(json.dumps(results))
'''
rows=[]
for p in sorted(H.glob(prefix+'artifacts/*/final.json')):
 if 'load_error' in json.loads((p.parent/'grader.json').read_text()):
  rows.append({'trial':p.parent.name,'error':'primary static/import validation failed; not executed'});continue
 with tempfile.TemporaryDirectory(prefix='rollback-audit-') as td:
  files=json.loads(p.read_text())['files']
  for n in ('ledger.py','outbox.py','service.py'):Path(td,n).write_text(files[n])
  try:
   proc=subprocess.run([sys.executable,'-c',PROGRAM],cwd=td,env={'PATH':os.environ.get('PATH',''),'PYTHONNOUSERSITE':'1'},capture_output=True,text=True,timeout=8)
   result=json.loads(proc.stdout) if not proc.returncode else {'error':proc.stderr}
  except Exception as e:result={'error':str(e)}
  rows.append({'trial':p.parent.name,'checks':result})
(H/(prefix+'rollback-diagnostics.json')).write_text(json.dumps(rows,indent=2)+'\n')
for r in rows:print(json.dumps(r))
