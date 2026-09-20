#!/usr/bin/env bash
# Prove SSH TCP forwarding works end-to-end, using a temporary key that is
# removed afterwards. authorized_keys is backed up and restored byte-for-byte.
set -uo pipefail

AK=/root/.ssh/authorized_keys
BAK=/root/.ssh/authorized_keys.comitia-tunnel-test.bak
TMPKEY=/tmp/comitia_tunnel_test_key

cp -a "$AK" "$BAK"
BEFORE=$(sha256sum "$AK" | awk '{print $1}')
echo "authorized_keys sha256 before: $BEFORE"

rm -f "$TMPKEY" "$TMPKEY.pub"
ssh-keygen -t ed25519 -N '' -f "$TMPKEY" -C "comitia-tunnel-test" -q
cat "$TMPKEY.pub" >> "$AK"
echo "temporary key appended"

pkill -f "18390:127.0.0.1:8390" 2>/dev/null; sleep 1

ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o ExitOnForwardFailure=yes -o IdentitiesOnly=yes -i "$TMPKEY" \
    -N -f -L 18390:127.0.0.1:8390 root@127.0.0.1 2>&1 | head -3
sleep 2

RC=1
if ss -tln 2>/dev/null | grep -q ":18390"; then
  echo "tunnel listener established on 18390"
  CRED=$(python3 -c "import json;print(json.load(open('/root/.comitia/owner.json'))['credential']['Token'])")
  CTX=$(grep '^COMITIA_CONTEXT_ID=' /root/.comitia/ids.env | cut -d= -f2)
  code=$(timeout 15 curl -s -o /tmp/tun.json -w '%{http_code}' \
      -X POST http://127.0.0.1:18390/v1/contexts/list \
      -H "Content-Type: application/json" -H "Authorization: Bearer $CRED" \
      -d '{"limit":10}')
  echo "authenticated API call through tunnel -> HTTP $code"
  if [ "$code" = "200" ] && grep -q "$CTX" /tmp/tun.json; then
    echo "RESULT: SSH tunnel verified end-to-end (context visible through tunnel)"
    RC=0
  else
    echo "RESULT: tunnel up but API call did not return expected data"
  fi
else
  echo "RESULT: no tunnel listener"
fi

echo
echo "=== cleanup: revoke temp key, restore authorized_keys ==="
pkill -f "18390:127.0.0.1:8390" 2>/dev/null
cp -a "$BAK" "$AK"
rm -f "$TMPKEY" "$TMPKEY.pub" "$BAK" /tmp/tun.json
AFTER=$(sha256sum "$AK" | awk '{print $1}')
echo "authorized_keys sha256 after : $AFTER"
[ "$BEFORE" = "$AFTER" ] && echo "authorized_keys restored intact" || echo "WARNING: authorized_keys differs!"
echo "tunnel ports still listening: $(ss -tln 2>/dev/null | grep -c ':18390')"
exit $RC
