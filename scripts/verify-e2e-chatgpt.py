#!/usr/bin/env python3
"""Phase 5 end-to-end: ChatGPT (public HTTPS) orchestrating Codex (loopback/SSH path).

Demonstrates the two-ingress topology: ChatGPT reaches Comitia over the public
TLS edge while Codex uses the loopback path an SSH tunnel would provide. Both are
first-class Comitia participants with separate identities.

Flow: chatgpt creates a task and addresses it to codex's session
      -> codex discovers it, claims, completes
      -> codex replies addressed to chatgpt's session
      -> chatgpt reads the reply.
"""
import json, pathlib, socket, ssl, sys

HOST = "vmi3260467.contaboserver.net"
IP = "45.88.191.59"
PORT = 8443

def sess(name):
    return json.loads(pathlib.Path(f"/root/.comitia/sessions/{name}.json").read_text())

CHATGPT = sess("chatgpt")
CODEX = sess("codex")
CG_TOKEN = CHATGPT["credential"]["Token"]
CG_SID = CHATGPT["credential"]["Session"]["ID"]
CX_TOKEN = CODEX["credential"]["Token"]
CX_SID = CODEX["credential"]["Session"]["ID"]

CTX = [l.split("=", 1)[1].strip()
       for l in pathlib.Path("/root/.comitia/ids.env").read_text().splitlines()
       if l.startswith("COMITIA_CONTEXT_ID=")][0]

PASS = FAIL = 0
def chk(ok, label, detail=""):
    global PASS, FAIL
    if ok:
        PASS += 1; print(f"  PASS  {label}")
    else:
        FAIL += 1; print(f"  FAIL  {label}  {detail}")

# ---- ChatGPT side: real MCP over the public TLS edge -------------------------
def public_rpc(method, params=None, token=CG_TOKEN, rid=1):
    ctx = ssl.create_default_context()
    raw = socket.create_connection((IP, PORT), timeout=30)
    tls = ctx.wrap_socket(raw, server_hostname=HOST)
    try:
        body = json.dumps({"jsonrpc": "2.0", "id": rid, "method": method,
                           "params": params or {}})
        req = (f"POST /mcp HTTP/1.1\r\nHost: {HOST}\r\n"
               f"Content-Type: application/json\r\n"
               f"Authorization: Bearer {token}\r\n"
               f"MCP-Protocol-Version: 2025-06-18\r\n"
               f"Content-Length: {len(body.encode())}\r\n"
               f"Connection: close\r\n\r\n{body}")
        tls.sendall(req.encode())
        buf = b""
        while True:
            c = tls.recv(65536)
            if not c:
                break
            buf += c
    finally:
        tls.close()
    head, _, payload = buf.partition(b"\r\n\r\n")
    if b"transfer-encoding: chunked" in head.lower():
        out, rest = b"", payload
        while True:
            line, _, rest = rest.partition(b"\r\n")
            try:
                n = int(line.split(b";")[0], 16)
            except ValueError:
                break
            if n == 0:
                break
            out += rest[:n]
            rest = rest[n + 2:]
        payload = out
    return json.loads(payload.decode("utf-8", "replace"))

def public_tool(name, args, rid=1):
    r = public_rpc("tools/call", {"name": name, "arguments": args}, rid=rid)
    res = r.get("result", {})
    return res.get("structuredContent", {}).get("result"), res.get("isError")

# ---- Codex side: same MCP server over loopback, as an SSH tunnel would give --
def loop_rpc(method, params=None, token=CX_TOKEN, rid=1):
    import http.client
    conn = http.client.HTTPConnection("127.0.0.1", 8391, timeout=30)
    body = json.dumps({"jsonrpc": "2.0", "id": rid, "method": method,
                       "params": params or {}})
    conn.request("POST", "/mcp", body=body, headers={
        "Content-Type": "application/json",
        "Authorization": "Bearer " + token,
        "MCP-Protocol-Version": "2025-06-18"})
    r = conn.getresponse(); data = r.read().decode(); conn.close()
    return json.loads(data)

def loop_tool(name, args, rid=1):
    r = loop_rpc("tools/call", {"name": name, "arguments": args}, rid=rid)
    res = r.get("result", {})
    return res.get("structuredContent", {}).get("result"), res.get("isError")

print("== 1. ChatGPT connects over public HTTPS ==")
r = public_rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                              "clientInfo": {"name": "chatgpt", "version": "1"}})
chk(r.get("result", {}).get("serverInfo", {}).get("name") == "comitia",
    "chatgpt initializes against the public edge")

peers, _ = public_tool("list_participants", {"context_id": CTX})
names = [p.get("Name") for p in (peers or [])]
chk("codex" in names, f"chatgpt discovers codex (sees: {names})")

print("== 2. ChatGPT creates a task for Codex ==")
task, err = public_tool("create_task", {"context_id": CTX,
                                        "title": "inspect issue #42",
                                        "body": "identify the root cause"})
TID = (task or {}).get("ID")
chk(bool(TID) and not err, f"task created over public HTTPS ({TID[:8] if TID else '?'}...)")

print("== 3. ChatGPT addresses the task to Codex's session ==")
_, err = public_tool("send_message", {
    "context_id": CTX, "dedupe_key": f"assign-{TID}", "body": "please investigate",
    "envelope": {"schema": "comitia.assignment.v1",
                 "recipient_session_ids": [CX_SID],
                 "task_id": TID,
                 "payload": {"instructions": "inspect issue #42 and identify the root cause",
                             "acceptance_criteria": ["a named root cause",
                                                     "a file and line reference"]}}})
chk(not err, "addressed assignment sent with envelope")

print("== 4. Codex (loopback path) sees and claims it ==")
msgs, _ = loop_tool("replay_messages", {"context_id": CTX, "after": 0, "limit": 200})
addressed = [m for m in (msgs or [])
             if (m.get("envelope") or {}).get("recipient_session_ids") == [CX_SID]]
chk(len(addressed) >= 1, f"codex found {len(addressed)} message(s) addressed to its session")

claim, err = loop_tool("claim_task", {"task_id": TID, "lease_seconds": 600})
CT = (claim or {}).get("Token")
LV = ((claim or {}).get("Task") or {}).get("LeaseVersion")
chk(bool(CT), f"codex claimed the task (lease v{LV})")

print("== 5. Codex completes it and replies to ChatGPT ==")
state, _ = loop_tool("get_task", {"task_id": TID})
chk((state or {}).get("Status") == "leased", "codex sees the lease it owns")

_, err = loop_tool("send_message", {
    "context_id": CTX, "dedupe_key": f"result-{TID}",
    "body": "root cause: off-by-one in the tokenizer",
    "envelope": {"schema": "comitia.work-submitted.v1",
                 "recipient_session_ids": [CG_SID],
                 "task_id": TID,
                 "payload": {"artifact_refs": ["git:head:internal/tokenizer.go#L118"],
                             "summary": "off-by-one in the tokenizer index"}}})
chk(not err, "codex reply addressed to chatgpt's session")

_, err = loop_tool("complete_task", {"task_id": TID, "claim_token": CT, "lease_version": LV})
chk(not err, "codex completed the task")

print("== 6. ChatGPT reads the reply from Codex ==")
msgs, _ = public_tool("replay_messages", {"context_id": CTX, "after": 0, "limit": 200})
replies = [m for m in (msgs or [])
           if (m.get("envelope") or {}).get("recipient_session_ids") == [CG_SID]
           and (m.get("envelope") or {}).get("task_id") == TID]
chk(len(replies) >= 1, f"chatgpt received {len(replies)} reply addressed to it")
if replies:
    print("        reply body:", replies[-1].get("Body"))
    print("        from session:", replies[-1].get("SessionID")[:8], "(codex =", CX_SID[:8] + ")")

tasks, _ = public_tool("list_tasks", {"context_id": CTX, "limit": 100})
final = [t for t in (tasks or []) if (t or {}).get("ID") == TID]
chk(final and final[0].get("Status") == "completed",
    f"chatgpt sees task status = {final[0].get('Status') if final else '?'}")

print(f"\n{int(PASS)}/{int(PASS+FAIL)} end-to-end checks passed")
sys.exit(1 if FAIL else 0)
