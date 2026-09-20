#!/usr/bin/env bash
# Create the shared workspace + a default coordination context.
# Requires /root/.comitia/owner.json from deploy-bootstrap.sh.
set -euo pipefail

BIN=/root/workspace/comitia/bin/comitia
export COMITIA_DB=/var/lib/comitia/comitia.db
export COMITIA_CREDENTIAL="$(python3 -c "import json;print(json.load(open('/root/.comitia/owner.json'))['credential']['Token'])")"

WS="harness-agents"
CTX="handoff"

ws_json=$("$BIN" workspace-create --name "$WS")
printf '%s' "$ws_json" > /root/.comitia/workspace.json
chmod 600 /root/.comitia/workspace.json

ws_id=$(python3 -c "import json;print(json.load(open('/root/.comitia/workspace.json'))['ID'])")

ctx_json=$("$BIN" context-create --workspace-id "$ws_id" --name "$CTX")
printf '%s' "$ctx_json" > /root/.comitia/context.json
chmod 600 /root/.comitia/context.json

python3 - <<PY
import json, pathlib
ws = json.loads(pathlib.Path('/root/.comitia/workspace.json').read_text())
cx = json.loads(pathlib.Path('/root/.comitia/context.json').read_text())
print('workspace id:', ws['ID'], '(', ws['Name'], ')')
print('context   id:', cx['ID'], '(', cx['Name'], ')')
pathlib.Path('/root/.comitia/ids.env').write_text(
    f"COMITIA_WORKSPACE_ID={ws['ID']}\nCOMITIA_CONTEXT_ID={cx['ID']}\n")
PY
