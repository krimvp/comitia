#!/usr/bin/env python3
"""End-to-end verification of the Comitia HTTP API.

Checks auth rejection and a real authenticated round trip: send -> replay -> ack.
"""
import http.client, json, pathlib, sys

HOST = sys.argv[1] if len(sys.argv) > 1 else "127.0.0.1"
PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 8390
TLS = len(sys.argv) > 3 and sys.argv[3] == "tls"

cred = json.loads(pathlib.Path("/root/.comitia/owner.json").read_text())["credential"]["Token"]
ctx = pathlib.Path("/root/.comitia/ids.env").read_text()
ctx_id = [l.split("=", 1)[1].strip() for l in ctx.splitlines() if l.startswith("COMITIA_CONTEXT_ID=")][0]

def call(path, body, token=None, extra_headers=None):
    conn = (http.client.HTTPSConnection(HOST, PORT, timeout=15) if TLS
            else http.client.HTTPConnection(HOST, PORT, timeout=15))
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    if extra_headers:
        headers.update(extra_headers)
    payload = json.dumps(body)
    conn.request("POST", path, body=payload, headers=headers)
    r = conn.getresponse()
    data = r.read().decode()
    conn.close()
    return r.status, data

results = []

# 1. no token -> 401
st, _ = call("/v1/contexts/list", {"limit": 10})
results.append(("no token rejected (401)", st == 401, st))

# 2. wrong token -> 401
st, _ = call("/v1/contexts/list", {"limit": 10}, token="ct_deadbeef")
results.append(("bad token rejected (401)", st == 401, st))

# 3. valid token -> 200 and sees our context
st, body = call("/v1/contexts/list", {"limit": 100}, token=cred)
ok = st == 200 and ctx_id in body
results.append(("valid token lists context", ok, st))

# 4. send a message
dedupe = "verify-http-1"
st, body = call("/v1/messages/append",
                {"context_id": ctx_id,
                 "message": {"DedupeKey": dedupe, "Body": "deploy verification"}},
                token=cred)
results.append(("send message", st == 200, st))

# 5. replay it back
st, body = call("/v1/messages/replay",
                {"context_id": ctx_id, "after": 0, "limit": 100}, token=cred)
results.append(("replay returns message", st == 200 and "deploy verification" in body, st))

# 6. ack (HTTP adapter names this field `after`, not `sequence`)
st, body = call("/v1/messages/ack",
                {"context_id": ctx_id, "after": 1}, token=cred)
results.append(("ack sequence", st == 200, st))

# 7. cursor reflects the ack
st, body = call("/v1/messages/cursor", {"context_id": ctx_id}, token=cred)
results.append(("cursor persisted", st == 200 and '"cursor":1' in body.replace(" ", ""), st))

fails = 0
for name, ok, detail in results:
    print(f"{'PASS' if ok else 'FAIL'}  {name}  (status={detail})")
    fails += 0 if ok else 1
print(f"\n{len(results) - fails}/{len(results)} passed against {HOST}:{PORT}")
sys.exit(1 if fails else 0)
