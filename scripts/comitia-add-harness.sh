#!/usr/bin/env bash
# Onboard a new harness into Comitia: mint a scoped identity, then print
# everything that harness needs to connect.
#
#   comitia-add-harness.sh <name> [permissions] [hours]
#
#   comitia-add-harness.sh cursor-cli
#   comitia-add-harness.sh reviewer read,write
#
# Writes /root/.comitia/sessions/<name>.json (mode 0600) and prints a
# connection block for public HTTPS, for the loopback/SSH path, and for the
# Responses API / Agents SDK.
set -euo pipefail

NAME="${1:?usage: comitia-add-harness.sh <name> [permissions] [hours]}"
PERMS="${2:-read,write,claim,grant}"
HOURS="${3:-2160}"

PUBLIC_HOST="vmi3260467.contaboserver.net"
PUBLIC_PORT="8443"

[ "$NAME" = "owner" ] && { echo "refusing to overwrite the owner identity" >&2; exit 1; }

bash /root/workspace/comitia/scripts/deploy-delegate.sh "$NAME" "$PERMS" "$HOURS"

python3 - "$NAME" "$PUBLIC_HOST" "$PUBLIC_PORT" <<'PY'
import json, pathlib, sys

name, host, port = sys.argv[1], sys.argv[2], sys.argv[3]
d = json.loads(pathlib.Path(f"/root/.comitia/sessions/{name}.json").read_text())
tok = d["credential"]["Token"]
sid = d["credential"]["Session"]["ID"]
ctx = [l.split("=", 1)[1].strip()
       for l in pathlib.Path("/root/.comitia/ids.env").read_text().splitlines()
       if l.startswith("COMITIA_CONTEXT_ID=")][0]

print(f"""
================ {name} ================
identity        : {name}
session id      : {sid}
expires         : {d['credential']['Session']['ExpiresAt']}
credential      : {tok}
context id      : {ctx}
workspace id    : {[l.split('=',1)[1].strip() for l in pathlib.Path('/root/.comitia/ids.env').read_text().splitlines() if l.startswith('COMITIA_WORKSPACE_ID=')][0]}

-- Public HTTPS (use from anywhere, incl. OpenAI-hosted clients) --
  URL    https://{host}:{port}/mcp
  Header Authorization: Bearer <credential>

-- SSH / loopback path (Codex CLI, Claude Code on the VPS) --
  export COMITIA_SERVER_URL=http://127.0.0.1:8391
  export COMITIA_CREDENTIAL=<credential>
  (the MCP stdio server needs COMITIA_SERVER_URL=http://127.0.0.1:8390)

-- Local session file, for the comitia CLI --
  --server-url https://{host}:{port}  (CLI REST is NOT exposed publicly)
  Use the loopback tunnel for CLI: see comitia-tunnel.sh
=======================================""")
PY
