"""Expose all handoff subchecks; do not change as-run scores."""
import json,re
import sys
from pathlib import Path
H=Path(__file__).resolve().parent
prefix="awake-control-" if "--control" in sys.argv else ""
choices=[('ledger',),('outbox',),('service',),('idempot',),('expir','lease'),('stale',),('sink',),('crash',),('transaction',),('database',)]
rows=[]
for p in sorted(H.glob(prefix+'artifacts/*/final.json')):
 doc=json.loads(p.read_text())['files'].get('HANDOFF.md','');low=doc.lower();count=len(re.findall(r"\b[\w'-]+\b",doc))
 missing=['/'.join(ws) for ws in choices if not any(w in low for w in ws)]
 rows.append(dict(trial=p.parent.name,word_count=count,whitespace_count=len(doc.split()),length_pass=120<=count<=220,missing_topic_terms=missing))
(H/(prefix+'handoff-diagnostics.json')).write_text(json.dumps(rows,indent=2)+'\n')
print(json.dumps(rows,indent=2))
