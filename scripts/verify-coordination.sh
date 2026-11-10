#!/usr/bin/env bash
# Exercise a realistic two-harness coordination flow through the CLI,
# as each harness's own scoped credential. Proves the documented workflow.
set -euo pipefail

BIN="${COMITIA_BIN:-/opt/comitia/bin/comitia}"
export COMITIA_SERVER_URL=http://127.0.0.1:8390
CTX_ID="$(grep '^COMITIA_CONTEXT_ID=' /root/.comitia/ids.env | cut -d= -f2)"

tok() { python3 -c "import json;print(json.load(open('/root/.comitia/sessions/$1.json'))['credential']['Token'])"; }
sid() { python3 -c "import json;print(json.load(open('/root/.comitia/sessions/$1.json'))['credential']['Session']['ID'])"; }

CLAUDE=$(tok claude); CODEX=$(tok codex)
CLAUDE_SID=$(sid claude); CODEX_SID=$(sid codex)

echo "== 1. each harness discovers peers in the context =="
COMITIA_CREDENTIAL="$CLAUDE" "$BIN" participants --context-id "$CTX_ID" \
  | python3 -c "import json,sys;[print('  ',p['Name'],p['SessionID'][:8]) for p in json.load(sys.stdin)]"

echo "== 2. claude creates a task =="
TASK=$(COMITIA_CREDENTIAL="$CLAUDE" "$BIN" task-create --context-id "$CTX_ID" \
        --title "review the parser" --body "check edge cases")
TASK_ID=$(echo "$TASK" | python3 -c "import json,sys;print(json.load(sys.stdin)['ID'])")
echo "   task: $TASK_ID"

echo "== 3. claude sends an addressed message to codex's session =="
ENV_FILE=$(mktemp)
python3 -c "
import json,sys
json.dump({'schema':'comitia.review-request.v1',
 'recipient_session_ids':['$CODEX_SID'],
 'task_id':'$TASK_ID',
 'payload':{'artifact_refs':['git:deadbeef:internal/parser.go'],
            'checks':['stale-lease rejection']}}, open('$ENV_FILE','w'))
"
COMITIA_CREDENTIAL="$CLAUDE" "$BIN" send --context-id "$CTX_ID" \
  --dedupe-key "req-$TASK_ID" --body "please review" --envelope-file "$ENV_FILE" > /dev/null
rm -f "$ENV_FILE"
echo "   sent"

echo "== 4. codex replays and sees it =="
COMITIA_CREDENTIAL="$CODEX" "$BIN" replay --context-id "$CTX_ID" --after 0 --limit 100 \
  | python3 -c "
import json,sys
msgs=json.load(sys.stdin)
addressed=[m for m in msgs
           if (m.get('envelope') or m.get('Envelope') or {}).get('recipient_session_ids')
           and '$CODEX_SID' in (m.get('envelope') or m.get('Envelope'))['recipient_session_ids']]
print('   total messages:', len(msgs))
print('   addressed to codex session:', len(addressed))
if addressed:
    print('   schema:', addressed[0]['envelope']['schema'])
    print('   task ref:', addressed[0]['envelope']['task_id'])
"

echo "== 5. codex claims the task =="
CLAIM=$(COMITIA_CREDENTIAL="$CODEX" "$BIN" task-claim --task-id "$TASK_ID" --lease 10m)
echo "$CLAIM" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print('   claim token present:', bool(d.get('Token')), '| lease version:', d['Task']['LeaseVersion'], '| status:', d['Task']['Status'])
"

echo "== 6. codex acks + completes =="
SEQ=$(COMITIA_CREDENTIAL="$CODEX" "$BIN" replay --context-id "$CTX_ID" --after 0 --limit 100 \
      | python3 -c "import json,sys;print(max(m['Sequence'] for m in json.load(sys.stdin)))")
COMITIA_CREDENTIAL="$CODEX" "$BIN" ack --context-id "$CTX_ID" --sequence "$SEQ" > /dev/null
echo "   acked through sequence $SEQ"
COMITIA_CREDENTIAL="$CODEX" "$BIN" cursor --context-id "$CTX_ID" | python3 -c "import json,sys;print('   persisted cursor:', json.load(sys.stdin))"

CT=$(echo "$CLAIM" | python3 -c "import json,sys;print(json.load(sys.stdin)['Token'])")
LV=$(echo "$CLAIM" | python3 -c "import json,sys;print(json.load(sys.stdin)['Task']['LeaseVersion'])")
COMITIA_CREDENTIAL="$CODEX" "$BIN" task-complete --task-id "$TASK_ID" --claim-token "$CT" --lease-version "$LV" > /dev/null
echo "   task completed"

echo "== 7. claude lists tasks =="
COMITIA_CREDENTIAL="$CLAUDE" "$BIN" task-list --context-id "$CTX_ID" \
  | python3 -c "import json,sys;[print('  ',t['Title'],'->',t['Status']) for t in json.load(sys.stdin)]"

echo
echo "ALL STEPS OK"
