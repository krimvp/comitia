#!/usr/bin/env bash
# One-time Comitia bootstrap: owner principal + local workspace/context.
# Idempotent-ish: bootstrap only succeeds once per database.
# Env: COMITIA_BIN (default /opt/comitia/bin/comitia), COMITIA_HOME (default
# /root/.comitia), COMITIA_DB (default /var/lib/comitia/comitia.db).
set -euo pipefail
umask 077

BIN="${COMITIA_BIN:-/opt/comitia/bin/comitia}"
HOME_DIR="${COMITIA_HOME:-/root/.comitia}"
export COMITIA_DB="${COMITIA_DB:-/var/lib/comitia/comitia.db}"
export COMITIA_BOOTSTRAP_CREDENTIAL="$(cat "$HOME_DIR/bootstrap.secret")"

install -d -m 0700 "$HOME_DIR"
owner_json=$("$BIN" bootstrap --name owner --ttl 8760h)

# mktemp creates the file 0600; the rename publishes it complete or not at all.
tmp=$(mktemp "$HOME_DIR/.owner.json.XXXXXX")
trap 'rm -f "$tmp"' EXIT
printf '%s' "$owner_json" > "$tmp"
mv -f "$tmp" "$HOME_DIR/owner.json"

HOME_DIR="$HOME_DIR" python3 - <<'PY'
import json, os, pathlib
d = json.loads((pathlib.Path(os.environ['HOME_DIR']) / 'owner.json').read_text())
print('owner principal id :', d['principal']['ID'])
print('owner session  id  :', d['credential']['Session']['ID'])
print('token prefix       :', d['credential']['Token'][:10])
print('session expires    :', d['credential']['Session']['ExpiresAt'])
PY
