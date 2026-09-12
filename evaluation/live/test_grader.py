import json, unittest
from pathlib import Path
from grader import grade
class GraderTests(unittest.TestCase):
 def allpass(self,cid,a):
  g=grade(cid,json.dumps(a));self.assertTrue(g);self.assertTrue(all(x['passed'] for x in g.values()),g)
 def source(self,i,marker):return json.loads(Path(__file__).with_name('benchmark.json').read_text())['cases'][i]['task'].split(marker)[1]
 def test_facts(self):self.allpass('factual-basics',dict(capital_of_japan='Tokyo',seconds_in_day=86400,binary_13='1101',chemical_symbol_gold='Au',smallest_prime=2))
 def test_facts_reject(self):self.assertFalse(grade('factual-basics','{}')['capital']['passed'])
 def test_refactor(self):self.allpass('stable-refactor',{'code':'def summarize(rows):\n d={}\n for r in rows:d[r["user"]]=d.get(r["user"],0)+r["amount"]\n return [{"user":k,"total":v} for k,v in d.items()]'})
 def test_quadratic_rejected(self):
  g=grade('stable-refactor',json.dumps({'code':self.source(1,'Original:\n')}));self.assertFalse(g['linear-work']['passed']);self.assertTrue(g['randomized']['passed'])
 def test_cache(self):self.allpass('ttl-cache-debug',{'code':'class Cache:\n def __init__(self):self.data={}\n def get(self,key,now,ttl,loader):\n  if key in self.data:\n   value,expires=self.data[key]\n   if now<expires:return value\n   del self.data[key]\n  value=loader()\n  if ttl>0:self.data[key]=(value,now+ttl)\n  return value'})
 def test_cache_buggy(self):
  g=grade('ttl-cache-debug',json.dumps({'code':self.source(2,'Buggy code:\n')}));self.assertFalse(g['boundary']['passed']);self.assertFalse(g['falsy']['passed']);self.assertFalse(g['loader-failure']['passed'])
 def test_architecture(self):self.allpass('outbox-architecture',dict(choice='C',atomic_writes=['order','outbox'],idempotency_key='order_id',broker_delivery='at-least-once',email_effect_count=1,email_api_call_count=3,rollback_email_count=0))
 def test_schedule(self):self.allpass('delegation-schedule',dict(schedule=[dict(task=t,worker=w,start=s) for t,w,s in [('A','w1',0),('B','w1',2),('C','w2',2),('D','w1',5),('E','w2',5)]],makespan=7,stale_completion='reject',recovery='reclaim-new-version',handoff=['task_id','lease_version','artifact','tests']))
 def test_schedule_conflict(self):
  g=grade('delegation-schedule',json.dumps(dict(schedule=[dict(task=t,worker='w1',start=0) for t in 'ABCDE'],makespan=3,stale_completion='accept',recovery='resume-old-lease',handoff=[])))
  for k in ['workers','dependencies','file-conflict','optimal','stale-lease']:self.assertFalse(g[k]['passed'])
 def test_communication(self):self.allpass('incident-handoff',dict(customer_update='Between 09:10 and 09:35 UTC, checkout errors affected 12% of attempts. We rolled back the release and restored service at 09:35 UTC. The root cause remains unconfirmed and our investigation is ongoing. We have found no evidence of data loss, but the investigation continues. We will provide the next customer update at 10:30 UTC. Thank you for your patience while we investigate.',impact_percent=12,start_utc='09:10',restored_utc='09:35',root_cause_status='unconfirmed',data_loss_status='no-evidence-investigating',next_update_utc='10:30',actions=[dict(owner='Maya',task='investigate pool limit',due_utc='10:15'),dict(owner='Luis',task='compare v42 to v41',due_utc='10:20')]))
if __name__=='__main__':unittest.main()
