#!/usr/bin/env bash
# Onboard a new harness into Comitia: mint a scoped identity, then print
# everything that harness needs to connect.
#
#   COMITIA_PUBLIC_HOST=comitia.example.com comitia-add-harness.sh <name> [permissions] [hours]
#
#   comitia-add-harness.sh cursor-cli
#   comitia-add-harness.sh reviewer read,write
#
# Writes $COMITIA_HOME/sessions/<name>.json (mode 0600; COMITIA_HOME defaults
# to /root/.comitia) and prints a connection block for public HTTPS, for the
# loopback/SSH path, and for the Responses API / Agents SDK.
# Env: COMITIA_PUBLIC_HOST (required), COMITIA_PUBLIC_PORT (default 8443), plus
# the COMITIA_BIN/COMITIA_HOME/COMITIA_DB overrides of deploy-delegate.sh.
set -euo pipefail

NAME="${1:?usage: comitia-add-harness.sh <name> [permissions] [hours]}"
PERMS="${2:-read,write,claim,grant}"
HOURS="${3:-2160}"

PUBLIC_HOST="${COMITIA_PUBLIC_HOST:?set COMITIA_PUBLIC_HOST to the public MCP hostname}"
PUBLIC_PORT="${COMITIA_PUBLIC_PORT:-8443}"
HOME_DIR="${COMITIA_HOME:-/root/.comitia}"

[ "$NAME" = "owner" ] && { echo "refusing to overwrite the owner identity" >&2; exit 1; }

bash "$(dirname "${BASH_SOURCE[0]}")/deploy-delegate.sh" "$NAME" "$PERMS" "$HOURS"

python3 - "$NAME" "$PUBLIC_HOST" "$PUBLIC_PORT" "$HOME_DIR" <<'PY'
import json, pathlib, sys

name, host, port, home = sys.argv[1], sys.argv[2], sys.argv[3], pathlib.Path(sys.argv[4])
d = json.loads((home / "sessions" / f"{name}.json").read_text())
tok = d["credential"]["Token"]
sid = d["credential"]["Session"]["ID"]
ids = dict(l.split("=", 1) for l in (home / "ids.env").read_text().splitlines() if "=" in l)

print(f"""
================ {name} ================
identity        : {name}
session id      : {sid}
expires         : {d['credential']['Session']['ExpiresAt']}
credential      : {tok}
context id      : {ids['COMITIA_CONTEXT_ID'].strip()}
workspace id    : {ids['COMITIA_WORKSPACE_ID'].strip()}

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
