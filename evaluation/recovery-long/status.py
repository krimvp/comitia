import json,time,sys
from pathlib import Path
H=Path(__file__).resolve().parent
control='--control' in sys.argv
prefix='awake-control-' if control else ''
progress=(H/(prefix+'progress.jsonl')).read_text().splitlines()
print('completed',len(progress),'/3' if control else '/18')
for line in progress[-2:]:print(line)
for p in H.glob(prefix+'artifacts/*/*-request.json'):
 if not Path(str(p).replace('-request.json','-timing.json')).exists():print('active',p.parent.name,p.stem,round(time.time()-json.loads(p.read_text())['started_at']),'seconds')
for d in H.glob(prefix+'artifacts/*'):
 if not (d/'observation.json').exists() and (d/'integration-checks.json').exists():
  results=json.loads((d/'integration-checks.json').read_text());print('integration',d.name,sum(v.get('passed',False) for v in results.values()),{k:v.get('error') for k,v in results.items() if not v.get('passed')})
print('errors:',(H/(prefix+'errors.txt')).read_text())
