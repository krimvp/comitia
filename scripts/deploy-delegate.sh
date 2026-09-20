#!/usr/bin/env bash
# Delegate one scoped Comitia session per harness and write a private session file.
# Usage: deploy-delegate.sh <harness-name> [permissions] [hours]
set -euo pipefail

NAME="${1:?usage: deploy-delegate.sh <harness-name> [perms] [hours]}"
PERMS="${2:-read,write,claim,grant}"
HOURS="${3:-2160}"

BIN=/opt/comitia/bin/comitia
export COMITIA_DB=/var/lib/comitia/comitia.db
export COMITIA_CREDENTIAL="$(python3 -c "import json;print(json.load(open('/root/.comitia/owner.json'))['credential']['Token'])")"

CTX_ID="$(grep '^COMITIA_CONTEXT_ID=' /root/.comitia/ids.env | cut -d= -f2)"

install -d -m 0700 /root/.comitia/sessions
out=$("$BIN" delegate --name "$NAME" --ttl "${HOURS}h" \
        --context-id "$CTX_ID" --permissions "$PERMS" --grant-ttl "${HOURS}h")

printf '%s' "$out" > "/root/.comitia/sessions/${NAME}.json"
chmod 600 "/root/.comitia/sessions/${NAME}.json"

NAME="$NAME" HOURS="$HOURS" python3 - <<'PY'
import json, os, pathlib
n = os.environ["NAME"]
d = json.loads(pathlib.Path(f"/root/.comitia/sessions/{n}.json").read_text())
print(f"{n}:")
print("  principal id:", d["principal"]["ID"])
print("  session id  :", d["credential"]["Session"]["ID"])
print("  token       :", d["credential"]["Token"][:10] + "...")
print("  expires     :", d["credential"]["Session"]["ExpiresAt"])
for g in d.get("grants", []):
    print("  grant       :", g["ContextID"], "scope", g["Permissions"], "until", g["ExpiresAt"])
PY
