#!/usr/bin/env bash
# Create the shared workspace + a default coordination context.
# Requires $COMITIA_HOME/owner.json from deploy-bootstrap.sh.
# Env: COMITIA_BIN (default /opt/comitia/bin/comitia), COMITIA_HOME (default
# /root/.comitia), COMITIA_DB (default /var/lib/comitia/comitia.db).
set -euo pipefail
umask 077

BIN="${COMITIA_BIN:-/opt/comitia/bin/comitia}"
HOME_DIR="${COMITIA_HOME:-/root/.comitia}"
export COMITIA_DB="${COMITIA_DB:-/var/lib/comitia/comitia.db}"
export COMITIA_CREDENTIAL="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['credential']['Token'])" "$HOME_DIR/owner.json")"

WS="harness-agents"
CTX="handoff"

install -d -m 0700 "$HOME_DIR"
tmp=""
trap 'rm -f "$tmp"' EXIT
# write_private <path> <content>: mktemp creates it 0600, the rename publishes it whole.
write_private() {
  tmp=$(mktemp "$(dirname "$1")/.$(basename "$1").XXXXXX")
  printf '%s' "$2" > "$tmp"
  mv -f "$tmp" "$1"
}

ws_json=$("$BIN" workspace-create --name "$WS")
write_private "$HOME_DIR/workspace.json" "$ws_json"

ws_id=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['ID'])" "$HOME_DIR/workspace.json")

ctx_json=$("$BIN" context-create --workspace-id "$ws_id" --name "$CTX")
write_private "$HOME_DIR/context.json" "$ctx_json"

HOME_DIR="$HOME_DIR" python3 - <<'PY'
import json, os, pathlib
d = pathlib.Path(os.environ['HOME_DIR'])
ws = json.loads((d / 'workspace.json').read_text())
cx = json.loads((d / 'context.json').read_text())
print('workspace id:', ws['ID'], '(', ws['Name'], ')')
print('context   id:', cx['ID'], '(', cx['Name'], ')')
(d / 'ids.env').write_text(
    f"COMITIA_WORKSPACE_ID={ws['ID']}\nCOMITIA_CONTEXT_ID={cx['ID']}\n")
PY
