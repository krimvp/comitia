#!/usr/bin/env bash
# Phase 1: prove the ChatGPT Comitia identity can do everything an agent needs,
# using the existing Comitia client (no new API surface).
set -uo pipefail

BIN=/opt/comitia/bin/comitia
export COMITIA_SERVER_URL=http://127.0.0.1:8390
CTX_ID="$(grep '^COMITIA_CONTEXT_ID=' /root/.comitia/ids.env | cut -d= -f2)"
SESS=/root/.comitia/sessions/chatgpt.json

TOK=$(python3 -c "import json;print(json.load(open('$SESS'))['credential']['Token'])")
SID=$(python3 -c "import json;print(json.load(open('$SESS'))['credential']['Session']['ID'])")
export COMITIA_CREDENTIAL="$TOK"

echo "chatgpt session: ${SID:0:8}..."
PASS=0; FAIL=0
chk() { if [ "$1" = "1" ]; then echo "  PASS  $2"; PASS=$((PASS+1)); else echo "  FAIL  $2"; FAIL=$((FAIL+1)); fi; }

echo "== identity =="
python3 -c "
import json
d=json.load(open('$SESS'))
print('  principal:', d['principal']['Name'], d['principal']['ID'][:8])
print('  admin:', d['principal']['Admin'])
print('  expires:', d['credential']['Session']['ExpiresAt'])
print('  grants :', [(g['ContextID'][:8], g['Permissions']) for g in d.get('grants',[])])
"

echo "== 1. discover peers =="
PEERS=$("$BIN" participants --context-id "$CTX_ID")
N=$(echo "$PEERS" | python3 -c "import json,sys;print(len(json.load(sys.stdin)))")
echo "  sees $N participants: $(echo "$PEERS" | python3 -c "import json,sys;print(', '.join(p['Name'] for p in json.load(sys.stdin)))")"
[ "$N" -ge 4 ] && chk 1 "peer discovery" || chk 0 "peer discovery"

echo "== 2. send a message =="
OUT=$("$BIN" send --context-id "$CTX_ID" --dedupe-key "chatgpt-hello-1" --body "hello from chatgpt" 2>&1)
echo "$OUT" | grep -q '"Sequence"' && chk 1 "send message" || chk 0 "send message :: $OUT"

echo "== 3. replay =="
REP=$("$BIN" replay --context-id "$CTX_ID" --after 0 --limit 100)
echo "$REP" | grep -q "hello from chatgpt" && chk 1 "replay messages" || chk 0 "replay messages"

echo "== 4. ack =="
SEQ=$(echo "$REP" | python3 -c "import json,sys;print(max(m['Sequence'] for m in json.load(sys.stdin)))")
"$BIN" ack --context-id "$CTX_ID" --sequence "$SEQ" >/dev/null 2>&1 && chk 1 "ack messages" || chk 0 "ack messages"

echo "== 5. cursor =="
CUR=$("$BIN" cursor --context-id "$CTX_ID")
echo "$CUR" | grep -q "$SEQ" && chk 1 "cursor read" || chk 0 "cursor read :: $CUR"

echo "== 6. create task =="
T=$("$BIN" task-create --context-id "$CTX_ID" --title "chatgpt orchestration probe" --body "phase 1")
TID=$(echo "$T" | python3 -c "import json,sys;print(json.load(sys.stdin)['ID'])" 2>/dev/null)
[ -n "${TID:-}" ] && chk 1 "create task ($TID)" || chk 0 "create task :: $T"

echo "== 7. claim task =="
CL=$("$BIN" task-claim --task-id "$TID" --lease 10m)
CT=$(echo "$CL" | python3 -c "import json,sys;print(json.load(sys.stdin)['Token'])" 2>/dev/null)
LV=$(echo "$CL" | python3 -c "import json,sys;print(json.load(sys.stdin)['Task']['LeaseVersion'])" 2>/dev/null)
[ -n "${CT:-}" ] && chk 1 "claim task (lease v$LV)" || chk 0 "claim task :: $CL"

echo "== 8. inspect task state =="
G=$("$BIN" task-get --task-id "$TID")
echo "$G" | grep -q '"leased"' && chk 1 "inspect task state" || chk 0 "inspect task state :: $G"

echo "== 9. complete task =="
"$BIN" task-complete --task-id "$TID" --claim-token "$CT" --lease-version "$LV" >/dev/null 2>&1 \
  && chk 1 "complete task" || chk 0 "complete task"

echo "== 10. addressed message to another session =="
CODEX_SID=$(python3 -c "import json;print(json.load(open('/root/.comitia/sessions/codex.json'))['credential']['Session']['ID'])")
EF=$(mktemp)
python3 -c "
import json
json.dump({'schema':'comitia.coordination.v1','recipient_session_ids':['$CODEX_SID'],
           'payload':{'note':'chatgpt addressing codex'}}, open('$EF','w'))
"
"$BIN" send --context-id "$CTX_ID" --dedupe-key "chatgpt-to-codex-1" --body "directed" --envelope-file "$EF" >/dev/null 2>&1
rm -f "$EF"
R=$("$BIN" replay --context-id "$CTX_ID" --after 0 --limit 100)
echo "$R" | python3 -c "
import json,sys
msgs=json.load(sys.stdin)
hit=[m for m in msgs if (m.get('envelope') or {}).get('recipient_session_ids')==['$CODEX_SID']]
sys.exit(0 if hit else 1)
" && chk 1 "addressed message with envelope" || chk 0 "addressed message with envelope"

echo
echo "Phase 1: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
