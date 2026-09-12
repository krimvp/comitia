#!/usr/bin/env python3
"""Behavioral checks fixed before model execution. Also supplies public feedback."""
import ast, copy, importlib, json, re, sys, tempfile, traceback
from pathlib import Path
CHECKS=['ledger_balances','ledger_validation','ledger_dedupe_atomicity','ledger_snapshot','outbox_claims','outbox_fencing','outbox_dedupe_snapshot','service_ingest','service_rollback','service_crash_delivery','service_snapshot','handoff']

def extract(text):
 decoder=json.JSONDecoder()
 for m in re.finditer(r'\{',text):
  try:
   obj,_=decoder.raw_decode(text[m.start():])
   if isinstance(obj,dict) and isinstance(obj.get('files'),dict):return obj
  except ValueError:pass
 raise ValueError('No JSON object with files mapping')

def event(i='e1',kind='reserve',amount=100,order='o1'):
 return dict(id=i,order_id=order,kind=kind,amount=amount)
def raises(fn,cls=ValueError):
 try:fn()
 except cls:return
 raise AssertionError('expected '+cls.__name__)
def rt(x):return json.loads(json.dumps(x))

def evaluate(text,public=False):
 result={n:{'passed':False} for n in CHECKS}
 try:
  files=extract(text)['files']
  for name in ('ledger.py','outbox.py','service.py'):
   code=files[name];tree=ast.parse(code)
   for n in ast.walk(tree):
    if isinstance(n,(ast.Import,ast.ImportFrom)):
     imports=[a.name.split('.')[0] for a in n.names] if isinstance(n,ast.Import) else [str(n.module).split('.')[0]]
     if not set(imports)<= {'copy','json','typing','dataclasses','collections','math','ledger','outbox'}:raise ValueError('unapproved import in '+name)
    if isinstance(n,ast.Name) and n.id in {'open','exec','eval','compile','__import__','input','breakpoint'}:raise ValueError('unapproved builtin in '+name)
  with tempfile.TemporaryDirectory(prefix='recovery-grade-') as td:
   for name in ('ledger.py','outbox.py','service.py'):Path(td,name).write_text(files[name])
   sys.path.insert(0,td)
   for name in ('ledger','outbox','service'):sys.modules.pop(name,None)
   L=importlib.import_module('ledger').Ledger;Q=importlib.import_module('outbox').Outbox;S=importlib.import_module('service').Service
   def ledger_balances():
    l=L();assert l.apply(event()) is True
    l.apply(event('e2','capture',70));l.apply(event('e3','refund',20))
    assert l.balances('o1')==dict(reserved=100,captured=70,refunded=20,available=30,net=50)
    l.apply(event('e4','reserve',35,'o2'));assert l.balances('o2')['available']==35
    raises(lambda:l.balances('missing'),KeyError)
   def ledger_validation():
    for e in [event(amount=True),event(amount=0),event(amount=-1),event(amount=1.5),event(i=''),event(kind='other'),event(order='')]:
     l=L();before=rt(l.snapshot());raises(lambda:l.apply(e));assert l.snapshot()==before
    l=L();raises(lambda:l.apply(event(kind='capture')));l.apply(event());before=rt(l.snapshot())
    for e in [event('e2','capture',101),event('e2','refund',1),event('e2','reserve',1)]:raises(lambda:l.apply(e));assert l.snapshot()==before
   def ledger_dedupe_atomicity():
    l=L();e=event();assert l.apply(e);assert l.apply(dict(e)) is False
    raises(lambda:l.apply(event(amount=99)))
    raises(lambda:l.apply(event('reusable','capture',101)))
    assert l.apply(event('reusable','capture',100));assert l.balances('o1')['captured']==100
    e['amount']=666;assert l.apply(event()) is False
   def ledger_snapshot():
    l=L();l.apply(event());snap=rt(l.snapshot());other=L(snap);other.apply(event('e2','capture',30))
    assert l.balances('o1')['captured']==0 and other.balances('o1')['captured']==30
    assert snap==l.snapshot();assert other.apply(event()) is False
    balance=other.balances('o1');balance['captured']=888;assert other.balances('o1')['captured']==30
    exposed=other.snapshot();exposed.clear();assert other.balances('o1')['captured']==30
    source=rt(l.snapshot());restored=L(source);source.clear();assert restored.balances('o1')['reserved']==100
   def outbox_claims():
    q=Q();assert q.claim('a',0,2) is None
    q.enqueue('a',{'n':1});q.enqueue('b',{'n':2});x=q.claim('w1',10,5);assert x['key']=='a' and x['version']==1
    assert q.claim('w2',11,5)['key']=='b';assert q.claim('w3',14,5) is None
    y=q.claim('w3',15,5);assert y['key']=='a' and y['version']==2
    assert q.complete('a','w3',2,16) is True
    assert q.claim('w4',99,5)['key']=='b'
   def outbox_fencing():
    q=Q();q.enqueue('a',{});x=q.claim('old',0,5)
    assert q.complete('a','old',x['version'],5) is False
    y=q.claim('new',5,5)
    assert q.complete('a','old',x['version'],6) is False
    assert q.complete('a','old',y['version'],6) is False
    assert q.complete('a','new',x['version'],6) is False
    assert q.complete('absent','new',y['version'],6) is False
    assert q.complete('a','new',y['version'],6) is True
    assert q.complete('a','new',y['version'],7) is False
    assert q.claim('w',100,1) is None
    raises(lambda:q.claim('',0,1));raises(lambda:q.claim('w',0,0))
   def outbox_dedupe_snapshot():
    q=Q();p={'items':[1]};assert q.enqueue('a',p);assert q.enqueue('a',copy.deepcopy(p)) is False
    raises(lambda:q.enqueue('a',{'items':[2]}));p['items'].append(99)
    x=q.claim('w',0,5);assert x['payload']=={'items':[1]};x['payload']['items'].append(88)
    snap=rt(q.snapshot());other=Q(snap);assert other.claim('z',4,1) is None
    y=other.claim('z',5,3);assert y['version']==2 and y['payload']=={'items':[1]}
    assert snap==q.snapshot();assert other.complete('a','z',2,6)
    assert q.complete('a','w',1,1)
    exposed=other.snapshot();exposed.clear();assert other.claim('w',100,1) is None
    src=rt(q.snapshot());fresh=Q(src);src.clear();assert fresh.enqueue('a',{'items':[1]}) is False
   def service_ingest():
    s=S();assert s.ingest(event());assert s.ingest(event()) is False
    got=[];assert s.dispatch('w',0,5,lambda k,p:got.append((k,p))) is True
    assert s.dispatch('w',1,5,lambda *x:None) is False
    assert got==[('e1',event())]
   def service_rollback():
    s=S();s.outbox.enqueue('e1',{'conflicting':'payload'});before=rt(s.snapshot())
    raises(lambda:s.ingest(event()));assert s.snapshot()==before
    raises(lambda:s.ledger.balances('o1'),KeyError)
   def service_crash_delivery():
    s=S();s.ingest(event());effects={};calls=[]
    def interrupted(k,p):
     calls.append(k);effects.setdefault(k,p);raise RuntimeError('crash after durable sink effect')
    raises(lambda:s.dispatch('old',10,5,interrupted),RuntimeError)
    recovered=S(rt(s.snapshot()));assert recovered.dispatch('new',14,5,lambda *x:None) is False
    def sink(k,p):calls.append(k);effects.setdefault(k,p)
    assert recovered.dispatch('new',15,5,sink) is True
    assert recovered.dispatch('next',100,5,sink) is False
    assert len(calls)==2 and len(effects)==1
   def service_snapshot():
    s=S();s.ingest(event());snap=rt(s.snapshot());s2=S(snap)
    s2.ingest(event('e2','capture',30));assert s.ledger.balances('o1')['captured']==0
    assert s.snapshot()==snap;assert s2.ingest(event()) is False
    got=[]
    for t in range(3):s2.dispatch('w',t,5,lambda k,p:got.append(k))
    assert got==['e1','e2']
    exposed=s2.snapshot();exposed.clear();assert s2.ledger.balances('o1')['captured']==30
    src=rt(s.snapshot());fresh=S(src);src.clear();assert fresh.ledger.balances('o1')['reserved']==100
   def handoff():
    doc=files.get('HANDOFF.md','');words=re.findall(r"\b[\w'-]+\b",doc);assert 120<=len(words)<=220, f'word count {len(words)}'
    low=doc.lower()
    for choices in [('ledger',),('outbox',),('service',),('idempot',),('expir','lease'),('stale',),('sink',),('crash',),('transaction',),('database',)]:assert any(w in low for w in choices),str(choices)
   local=locals()
   for n in CHECKS:
    try:local[n]();result[n]={'passed':True}
    except Exception as e:result[n]={'passed':False,'error':type(e).__name__+': '+str(e),'trace':traceback.format_exc(limit=2)}
   sys.path.remove(td)
 except Exception as e:
  result['load_error']={'passed':False,'error':type(e).__name__+': '+str(e)}
 return result
if __name__=='__main__':print(json.dumps(evaluate(sys.stdin.read()),indent=2))
