#!/usr/bin/env bash
# Verify credential isolation: revoking one identity must not affect the others.
set -uo pipefail

BIN="${COMITIA_BIN:-/opt/comitia/bin/comitia}"
export COMITIA_SERVER_URL=http://127.0.0.1:8390
CTX_ID="$(grep '^COMITIA_CONTEXT_ID=' /root/.comitia/ids.env | cut -d= -f2)"
OWNER=$(python3 -c "import json;print(json.load(open('/root/.comitia/owner.json'))['credential']['Token'])")

PASS=0; FAIL=0
chk() { if [ "$1" = "1" ]; then echo "  PASS  $2"; PASS=$((PASS+1)); else echo "  FAIL  $2"; FAIL=$((FAIL+1)); fi; }

echo "== mint a throwaway identity =="
export COMITIA_CREDENTIAL="$OWNER"
OUT=$("$BIN" delegate --name revoke-probe --ttl 1h --context-id "$CTX_ID" \
        --permissions read,write,claim --grant-ttl 1h)
PROBE_TOK=$(echo "$OUT" | python3 -c "import json,sys;print(json.load(sys.stdin)['credential']['Token'])")
PROBE_SID=$(echo "$OUT" | python3 -c "import json,sys;print(json.load(sys.stdin)['credential']['Session']['ID'])")
echo "  probe session: ${PROBE_SID:0:8}..."

echo "== probe works before revocation =="
COMITIA_CREDENTIAL="$PROBE_TOK" "$BIN" replay --context-id "$CTX_ID" --after 0 --limit 1 >/dev/null 2>&1 \
  && chk 1 "probe session authorized before revoke" || chk 0 "probe session authorized before revoke"

echo "== revoke the probe =="
"$BIN" revoke --session-id "$PROBE_SID" >/dev/null 2>&1 && chk 1 "revoke succeeded" || chk 0 "revoke succeeded"

echo "== probe is now denied =="
if COMITIA_CREDENTIAL="$PROBE_TOK" "$BIN" replay --context-id "$CTX_ID" --after 0 --limit 1 >/dev/null 2>&1; then
  chk 0 "revoked session is rejected"
else
  chk 1 "revoked session is rejected"
fi

echo "== other identities unaffected =="
for h in claude codex chatgpt hermes; do
  T=$(python3 -c "import json;print(json.load(open('/root/.comitia/sessions/$h.json'))['credential']['Token'])")
  if COMITIA_CREDENTIAL="$T" "$BIN" replay --context-id "$CTX_ID" --after 0 --limit 1 >/dev/null 2>&1; then
    chk 1 "$h still works"
  else
    chk 0 "$h still works"
  fi
done

echo
echo "Isolation: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
