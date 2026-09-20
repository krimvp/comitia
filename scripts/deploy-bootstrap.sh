#!/usr/bin/env bash
# One-time Comitia bootstrap: owner principal + local workspace/context.
# Idempotent-ish: bootstrap only succeeds once per database.
set -euo pipefail

BIN=/root/workspace/comitia/bin/comitia
export COMITIA_DB=/var/lib/comitia/comitia.db
export COMITIA_BOOTSTRAP_CREDENTIAL="$(cat /root/.comitia/bootstrap.secret)"

owner_json=$("$BIN" bootstrap --name owner --ttl 8760h)
printf '%s' "$owner_json" > /root/.comitia/owner.json
chmod 600 /root/.comitia/owner.json

python3 - <<'PY'
import json, pathlib
d = json.loads(pathlib.Path('/root/.comitia/owner.json').read_text())
print('owner principal id :', d['principal']['ID'])
print('owner session  id  :', d['credential']['Session']['ID'])
print('token prefix       :', d['credential']['Token'][:10])
print('session expires    :', d['credential']['Session']['ExpiresAt'])
PY
