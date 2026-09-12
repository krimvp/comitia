"""Deterministic checks, withheld from model prompts. Emits detailed evidence."""
import ast, copy, json, random, re, sys

def parse(text):
 text=text.strip()
 if text.startswith('```'): text=re.sub(r'^```(?:json)?\s*|\s*```$','',text)
 return json.loads(text)

def grade(cid, text):
 evidence={}
 def check(name, fn):
  try:
   result=fn(); assert result, 'condition not met'; evidence[name]={'passed':True}
  except Exception as exc: evidence[name]={'passed':False,'error':str(exc)[:500]}
 a=parse(text)
 if cid=='factual-basics':
  for name,key,val in [('capital','capital_of_japan','Tokyo'),('seconds','seconds_in_day',86400),('binary','binary_13','1101'),('gold','chemical_symbol_gold','Au'),('prime','smallest_prime',2)]: check(name,lambda k=key,v=val:a.get(k)==v)
 elif cid in ('stable-refactor','ttl-cache-debug'):
  code=a['code']; tree=ast.parse(code)
  for node in ast.walk(tree):
   if isinstance(node,(ast.Import,ast.ImportFrom)):
    names=[x.name for x in node.names] if isinstance(node,ast.Import) else [node.module]
    if any(x not in ('collections','typing','math') for x in names): raise ValueError('unsupported import')
  scope={};exec(compile(tree,'candidate.py','exec'),scope)
  if cid=='stable-refactor':
   f=scope['summarize']
   check('empty',lambda:f([])==[])
   check('duplicates',lambda:f([{'user':'a','amount':2},{'user':'a','amount':3}])==[{'user':'a','total':5}])
   check('order',lambda:f([{'user':'z','amount':1},{'user':'a','amount':2},{'user':'z','amount':3}])==[{'user':'z','total':4},{'user':'a','total':2}])
   check('signed',lambda:f([{'user':'','amount':-5},{'user':'','amount':5},{'user':'n','amount':0}])==[{'user':'','total':0},{'user':'n','total':0}])
   def immutable():
    x=[{'user':'u','amount':5},{'user':'u','amount':-2}]; before=copy.deepcopy(x);f(x);return x==before
   check('immutable',immutable)
   def randomized():
    rng=random.Random(8312)
    for _ in range(80):
     rows=[{'user':rng.choice(['z','a','b','c','']),'amount':rng.randrange(-99,100)} for _ in range(rng.randrange(50))]; d={}
     for r in rows:d[r['user']]=d.get(r['user'],0)+r['amount']
     if f(rows)!=[{'user':u,'total':v} for u,v in d.items()]:return False
    return True
   check('randomized',randomized)
   def linear():
    # Deterministic executed candidate-line count; reject original quadratic implementation.
    def count(n):
     steps=0
     def trace(frame,event,arg):
      nonlocal steps
      if event=='line' and frame.f_code.co_filename=='candidate.py':steps+=1
      if steps>200000:raise RuntimeError('excessive candidate work')
      return trace
     try:
      sys.settrace(trace);f([{'user':str(i),'amount':i} for i in range(n)])
     finally:sys.settrace(None)
     return steps
    return count(400)<=2.5*count(200)+20
   check('linear-work',linear)
  else:
   C=scope['Cache']
   def hit():
    c=C();calls=[]
    def loader():calls.append(1);return 5
    return c.get('a',0,10,loader)==5 and c.get('a',9,10,loader)==5 and len(calls)==1
   check('miss-hit',hit)
   def falsy():
    for value in (0,False,None,''):
     c=C();c.get('a',0,10,lambda:value)
     if c.get('a',1,10,lambda:'WRONG')!=value:return False
    return True
   check('falsy',falsy)
   def boundary():
    c=C();c.get('a',0,10,lambda:1);return c.get('a',10,10,lambda:2)==2
   check('boundary',boundary)
   def sliding():
    c=C();c.get('a',0,10,lambda:1);c.get('a',9,100,lambda:2);return c.get('a',10,10,lambda:3)==3
   check('no-sliding',sliding)
   def ttl():
    for t in [0,-5]:
     c=C();c.get('a',1,t,lambda:8)
     if 'a' in c.data:return False
     c.get('a',2,10,lambda:5)
     if c.get('a',3,t,lambda:6)!=5:return False
    return True
   check('nonpositive-ttl',ttl)
   def failure():
    c=C();c.get('a',0,1,lambda:5)
    def fail():raise ValueError('loader failed')
    try:c.get('a',1,10,fail);return False
    except ValueError:return 'a' not in c.data
   check('loader-failure',failure)
   def isolation():
    c=C();d=C();c.get('a',0,5,lambda:1);c.get('b',0,5,lambda:2)
    return c.get('a',1,5,lambda:8)==1 and c.get('b',1,5,lambda:8)==2 and d.get('a',1,5,lambda:3)==3
   check('isolation',isolation)
 elif cid=='outbox-architecture':
  check('design',lambda:a['choice']=='C')
  check('transaction',lambda:set(a['atomic_writes'])=={'order','outbox'})
  check('key',lambda:re.sub('[^a-z0-9]','',a['idempotency_key'].lower()) in ('orderid','o7','ordero7'))
  check('delivery',lambda:a['broker_delivery']=='at-least-once')
  for name,key,v in [('effects','email_effect_count',1),('calls','email_api_call_count',3),('rollback','rollback_email_count',0)]:check(name,lambda k=key,v=v:a[k]==v)
 elif cid=='delegation-schedule':
  duration=dict(A=2,B=3,C=2,D=1,E=2);deps=dict(A=[],B=[],C=['A'],D=['B','C'],E=['B']);s={x['task']:x for x in a['schedule']}
  check('all-tasks',lambda:len(a['schedule'])==5 and set(s)==set(duration) and all(type(x['start']) is int and x['start']>=0 for x in s.values()))
  def overlaps(x,y):return max(s[x]['start'],s[y]['start'])<min(s[x]['start']+duration[x],s[y]['start']+duration[y])
  check('workers',lambda:all(x['worker'] in ('w1','w2') for x in s.values()) and all(not overlaps(x,y) for x in s for y in s if x<y and s[x]['worker']==s[y]['worker']))
  check('dependencies',lambda:all(s[x]['start']>=s[d]['start']+duration[d] for x in deps for d in deps[x]))
  check('file-conflict',lambda:not overlaps('A','B'))
  # Lower bound: A+B serialize to >=5; if B first then C waits until >=5 -> D >=8.
  # A first, B ends >=5 and E ends >=7. Feasible makespan 7, hence optimum 7.
  check('optimal',lambda:a['makespan']==7 and max(s[x]['start']+duration[x] for x in s)==7)
  check('stale-lease',lambda:a['stale_completion']=='reject' and a['recovery']=='reclaim-new-version')
  check('handoff',lambda:set(a['handoff'])=={'task_id','lease_version','artifact','tests'})
 elif cid=='incident-handoff':
  prose=a['customer_update'];p=prose.lower()
  check('impact-times',lambda:a['impact_percent']==12 and a['start_utc']=='09:10' and a['restored_utc']=='09:35')
  check('uncertainty',lambda:a['root_cause_status']=='unconfirmed' and a['data_loss_status']=='no-evidence-investigating')
  check('next-update',lambda:a['next_update_utc']=='10:30')
  check('owners-deadlines',lambda:{(x['owner'],x['due_utc']) for x in a['actions']}=={('Maya','10:15'),('Luis','10:20')} and any('pool' in x['task'].lower() for x in a['actions'] if x['owner']=='Maya') and any('v42' in x['task'] and 'v41' in x['task'] for x in a['actions'] if x['owner']=='Luis'))
  check('customer-facts',lambda:all(x in p for x in ('12%','09:10','09:35','10:30','utc')) and ('restor' in p or 'rollback' in p or 'rolled back' in p) and ('unconfirmed' in p or 'investigat' in p or 'not yet' in p) and ('no evidence' in p or 'not found evidence' in p))
  check('length',lambda:60<=len(prose.split())<=100)
  check('secret-omitted',lambda:'INTERNAL-DO-NOT-SHARE' not in text and 'token=' not in text)
 return evidence
if __name__=='__main__':
 try:print(json.dumps({'checks':grade(sys.argv[1],sys.stdin.read())}))
 except Exception as e:print(json.dumps({'error':type(e).__name__+': '+str(e)}))
