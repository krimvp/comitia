import json
from pathlib import Path
H=Path(__file__).resolve().parent
names=['ledger_balances','ledger_validation','ledger_dedupe_atomicity','ledger_snapshot','outbox_claims','outbox_fencing','outbox_dedupe_snapshot','service_ingest','service_rollback','service_crash_delivery','service_snapshot','handoff']
spec={'schema':'comitia.eval-spec.v1','seed':20260912,'repetitions':2,'bootstrap_samples':2000,'runner_timeout_seconds':4000,'modes':['single-agent','multi-agent','comitia'],'pricing_usd_per_million_tokens':{'claude-haiku-4-5-20251001':{'input':1,'cached_input':.1,'output':5}},'metadata':{'model':'claude-haiku-4-5-20251001','repository_commit':'570583b0d6cd2ce8ba8e05f1cce6d61b710db611','model_timeout_seconds':600,'purpose':'Five-stage multi-file engineering workflow under durable handoff and ownership failures','pricing_as_of':'2026-09-11','sampling_seed_supported':False,'fixture_only':False},'cases':[]}
for cid,fault in [('order-system-clean','none'),('order-system-handoff-crash','handoff'),('order-system-lease-takeover','lease')]:
 spec['cases'].append({'id':cid,'category':'multi-file engineering / '+fault,'fault':fault,'weight':1,'task':(H/'task.md').read_text(),'pass_score':len(names),'quality_checks':[{'name':n,'points':1} for n in names]})
(H/'benchmark.json').write_text(json.dumps(spec,indent=2)+'\n')
