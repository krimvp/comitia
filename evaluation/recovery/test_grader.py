import json,unittest
from pathlib import Path
from grader import evaluate,CHECKS
class GraderTest(unittest.TestCase):
 def setUp(self):self.files={p.name:p.read_text() for p in (Path(__file__).parent/'reference').iterdir() if p.is_file()}
 def grade(self):return evaluate(json.dumps({'files':self.files}))
 def test_reference(self):self.assertTrue(all(self.grade()[n]['passed'] for n in CHECKS),self.grade())
 def test_stale_owner_mutation(self):
  self.files['outbox.py']=self.files['outbox.py'].replace("i['worker']==worker and i['version']==version and now<i['expiry']","True")
  self.assertFalse(self.grade()['outbox_fencing']['passed'])
 def test_no_rollback_mutation(self):
  self.files['service.py']=self.files['service.py'].replace("self.ledger=Ledger(before['ledger']);self.outbox=Outbox(before['outbox']);raise","raise")
  self.assertFalse(self.grade()['service_rollback']['passed'])
 def test_alias_mutation(self):
  self.files['ledger.py']=self.files['ledger.py'].replace('def snapshot(self):return deepcopy(self.state)','def snapshot(self):return self.state')
  self.assertFalse(self.grade()['ledger_snapshot']['passed'])
 def test_prose_extraction(self):
  from grader import extract
  self.assertEqual(extract('prefix '+json.dumps({'files':self.files})+' suffix')['files'],self.files)
 def test_missing_files(self):self.assertIn('load_error',evaluate('{}'))
if __name__=='__main__':unittest.main()
