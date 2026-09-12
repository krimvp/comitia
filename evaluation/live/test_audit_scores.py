import json, unittest
from pathlib import Path
from audit_scores import assess, audit_spec
SPEC=json.loads(Path(__file__).with_name('benchmark.json').read_text())
CASES={c['id']:c for c in SPEC['cases']}
class AuditScoreTests(unittest.TestCase):
 def test_capitalization_correction(self):
  a=dict(choice='C',atomic_writes=['Order','Outbox'],idempotency_key='order_id',broker_delivery='at-least-once',email_effect_count=1,email_api_call_count=3,rollback_email_count=0)
  q,e=assess(CASES['outbox-architecture'],json.dumps(a));self.assertTrue(all(x['earned']==1 for x in q));self.assertEqual(e['corrections'],['case_insensitive_entity_names'])
 def test_equivalent_outbox_entity(self):
  q,e=assess(CASES['outbox-architecture'],json.dumps(dict(atomic_writes=['Order','OutboxMessage'])))
  self.assertEqual(next(x['earned'] for x in q if x['name']=='transaction'),1)
 def test_wrong_transaction_still_fails(self):
  q,e=assess(CASES['outbox-architecture'],json.dumps(dict(atomic_writes=['Order','Email'])));self.assertEqual(next(x['earned'] for x in q if x['name']=='transaction'),0)
 def test_format_sensitivity_separate_from_primary(self):
  answer=json.dumps(dict(capital_of_japan='Tokyo',seconds_in_day=86400,binary_13='1101',chemical_symbol_gold='Au',smallest_prime=2))+'\nExtra commentary'
  q,e=assess(CASES['factual-basics'],answer);self.assertTrue(all(x['earned']==0 for x in q))
  q,e=assess(CASES['factual-basics'],answer,True);self.assertTrue(all(x['earned']==1 for x in q))
 def test_content_sensitivity_does_not_fix_wrong_facts(self):
  answer=json.dumps(dict(capital_of_japan='Osaka',seconds_in_day=86400,binary_13='1101',chemical_symbol_gold='Au',smallest_prime=2))+'\nExtra commentary'
  q,e=assess(CASES['factual-basics'],answer,True);self.assertEqual(next(x['earned'] for x in q if x['name']=='capital'),0)
class ExcludedCheckTests(unittest.TestCase):
 def case(self):return next(c for c in audit_spec(SPEC,b'original','audited')['cases'] if c['id']=='delegation-schedule')
 def answer(self):return dict(schedule=[dict(task=t,worker=w,start=s) for t,w,s in [('A','w1',0),('B','w1',2),('C','w2',2),('D','w1',5),('E','w2',5)]],makespan=7,stale_completion='reject',recovery='reclaim-new-version',handoff=None)
 def test_ambiguous_handoff_excluded(self):
  q,e=assess(self.case(),json.dumps(self.answer()));self.assertEqual(len(q),6);self.assertTrue(all(x['earned']==1 for x in q))
 def test_real_conflict_not_excluded(self):
  a=self.answer();a['schedule'][1]['start']=0
  q,e=assess(self.case(),json.dumps(a));self.assertEqual(next(x['earned'] for x in q if x['name']=='file-conflict'),0)
if __name__=='__main__':unittest.main()
