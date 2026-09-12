import json,subprocess,sys,tempfile,unittest
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
from storage import make_store
H=Path(__file__).resolve().parent
class RecoveryTests(unittest.TestCase):
 def test_real_process_restarts(self):
  with tempfile.TemporaryDirectory() as td:
   spec=json.loads((H/'benchmark.json').read_text());spec['metadata'].update(fixture_only=True,artifact_root=td+'/artifacts');path=Path(td)/'spec.json';path.write_text(json.dumps(spec))
   for mode in spec['modes']:
    for c in spec['cases']:
     with self.subTest(mode=mode,fault=c['fault']):
      p=subprocess.run([sys.executable,str(H/'runner.py'),'--eval-spec',str(path),'--eval-mode',mode,'--eval-case',c['id'],'--eval-replicate','0','--eval-seed','1'],capture_output=True,text=True,timeout=25)
      self.assertEqual(p.returncode,0,p.stderr);obs=json.loads(p.stdout);self.assertEqual(obs['status'],'completed',obs)
      self.assertTrue(all(x['earned']==1 for x in obs['quality']),obs)
      self.assertEqual(obs['metadata']['restarts'],int(c['fault']!='none'))
      self.assertEqual(obs['metadata']['model_sessions'],6 if c['fault']=='lease' else 5)
      ev=[json.loads(s) for s in (Path(obs['metadata']['artifact'])/'events.jsonl').read_text().splitlines()]
      if c['fault']!='none':
       starts=[e for e in ev if e['kind']=='engine_start'];self.assertEqual(len(starts),2);self.assertNotEqual(starts[0]['pid'],starts[1]['pid'])
       delayed=next(e for e in ev if e['kind']=='delayed_completion');self.assertTrue(delayed['authoritative_unchanged'])
       if c['fault']=='lease':self.assertFalse(delayed['accepted'])
 def test_contended_claims(self):
  for mode in ['multi-agent','comitia']:
   with tempfile.TemporaryDirectory() as td:
    st=make_store(mode,td,Path(td)/'events',True)
    def claim():
     try:st.claim('ledger','ledger');return True
     except RuntimeError:return False
    with ThreadPoolExecutor(max_workers=4) as ex:self.assertEqual(sum(ex.map(lambda _:claim(),range(4))),1)
if __name__=='__main__':unittest.main()
