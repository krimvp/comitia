import json
from pathlib import Path
from audit_scores import assess
HERE=Path(__file__).resolve().parent
spec=json.loads((HERE/'audited-benchmark.json').read_text());cases={c['id']:c for c in spec['cases']};out=[]
for p in sorted((HERE/'observations').glob('*.json')):
 r=json.loads(p.read_text())
 if r['status']!='timeout':continue
 a=Path(r['metadata']['artifact']);row={'case':r['case_id'],'mode':r['mode'],'replicate':r['replicate'],'stage':'reviewer' if (a/'reviewer-prompt.txt').exists() else 'worker','wall_seconds':r['wall_seconds'],'board_operations':r['metadata']['comitia_operations'],'captured_cost_usd':r['metadata']['cli_reported_cost_usd'],'error':r['error']}
 if (a/'worker-raw.json').exists():
  d=json.loads((a/'worker-raw.json').read_text());q,e=assess(cases[r['case_id']],d['result'],True);row['returned_worker_content_score']=sum(x['earned'] for x in q)/sum(x['points'] for x in cases[r['case_id']]['quality_checks']);row['returned_worker_checks']=q
 out.append(row)
(HERE/'timeout-audit.json').write_text(json.dumps(out,indent=2)+'\n');print(json.dumps(out,indent=2))
