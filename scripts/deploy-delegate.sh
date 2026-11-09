#!/usr/bin/env bash
# Delegate one scoped Comitia session per harness and write a private session file.
# Usage: deploy-delegate.sh <harness-name> [permissions] [hours]
# Env: COMITIA_BIN (default /opt/comitia/bin/comitia), COMITIA_HOME (default
# /root/.comitia), COMITIA_DB (default /var/lib/comitia/comitia.db).
set -euo pipefail
umask 077

NAME="${1:?usage: deploy-delegate.sh <harness-name> [perms] [hours]}"
PERMS="${2:-read,write,claim,grant}"
HOURS="${3:-2160}"

# The name becomes a file name under sessions/: no separators, no leading dot.
[[ "$NAME" =~ ^[A-Za-z0-9_-][A-Za-z0-9._-]*$ ]] || {
  echo "invalid harness name '$NAME': use letters, digits, '.', '_' or '-', not starting with '.'" >&2; exit 2; }

BIN="${COMITIA_BIN:-/opt/comitia/bin/comitia}"
HOME_DIR="${COMITIA_HOME:-/root/.comitia}"
export COMITIA_DB="${COMITIA_DB:-/var/lib/comitia/comitia.db}"
export COMITIA_CREDENTIAL="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['credential']['Token'])" "$HOME_DIR/owner.json")"

CTX_ID="$(grep '^COMITIA_CONTEXT_ID=' "$HOME_DIR/ids.env" | cut -d= -f2)"

install -d -m 0700 "$HOME_DIR" "$HOME_DIR/sessions"
out=$("$BIN" delegate --name "$NAME" --ttl "${HOURS}h" \
        --context-id "$CTX_ID" --permissions "$PERMS" --grant-ttl "${HOURS}h")

# mktemp creates the file 0600; the rename publishes it complete or not at all.
SESSION="$HOME_DIR/sessions/${NAME}.json"
tmp=$(mktemp "$HOME_DIR/sessions/.${NAME}.json.XXXXXX")
trap 'rm -f "$tmp"' EXIT
printf '%s' "$out" > "$tmp"
mv -f "$tmp" "$SESSION"

NAME="$NAME" SESSION="$SESSION" python3 - <<'PY'
import json, os, pathlib
n = os.environ["NAME"]
d = json.loads(pathlib.Path(os.environ["SESSION"]).read_text())
print(f"{n}:")
print("  principal id:", d["principal"]["ID"])
print("  session id  :", d["credential"]["Session"]["ID"])
print("  token       :", d["credential"]["Token"][:10] + "...")
print("  expires     :", d["credential"]["Session"]["ExpiresAt"])
for g in d.get("grants", []):
    print("  grant       :", g["ContextID"], "scope", g["Permissions"], "until", g["ExpiresAt"])
PY
