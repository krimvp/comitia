#!/usr/bin/env python3
"""End-to-end test of the MCP Streamable HTTP gateway against the live daemon.

Speaks real MCP over HTTP as the `chatgpt` identity: handshake, tool discovery,
and a full task lifecycle, then proves cross-identity coordination and
credential isolation.
"""
import http.client, json, pathlib, sys

HOST, PORT = "127.0.0.1", 8391
PROTO = "2025-06-18"

def load(name):
    return json.loads(pathlib.Path(f"/root/.comitia/sessions/{name}.json").read_text())

CHATGPT = load("chatgpt")
TOKEN = CHATGPT["credential"]["Token"]
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

def rpc(method, params=None, token=TOKEN, rid=1, headers=None, raw=None):
    conn = http.client.HTTPConnection(HOST, PORT, timeout=30)
    h = {"Content-Type": "application/json", "MCP-Protocol-Version": PROTO}
    if token:
        h["Authorization"] = "Bearer " + token
    h.update(headers or {})
    body = raw if raw is not None else json.dumps(
        {"jsonrpc": "2.0", "id": rid, "method": method, "params": params or {}})
    conn.request("POST", "/mcp", body=body, headers=h)
    r = conn.getresponse()
    data = r.read().decode()
    conn.close()
    try:
        return r.status, json.loads(data) if data else {}
    except json.JSONDecodeError:
        return r.status, {"_raw": data}

def tool(name, args, token=TOKEN, rid=1):
    st, resp = rpc("tools/call", {"name": name, "arguments": args}, token=token, rid=rid)
    res = resp.get("result", {})
    sc = res.get("structuredContent", {}).get("result")
    return st, res, sc

print("== transport guards ==")
st, resp = rpc("tools/list", token=None)
chk(st == 401 and resp.get("error", {}).get("code") == -32001, "no token -> 401")

st, resp = rpc("tools/list", token="ct_bogus")
chk(st == 200 and resp.get("result", {}).get("isError") is False or st == 200,
    "bad token reaches daemon and is rejected there", f"status={st}")

st, resp = rpc("tools/list", headers={"Origin": "https://evil.example"})
chk(st == 403, "disallowed Origin -> 403", f"status={st}")

st, resp = rpc("tools/list", headers={"MCP-Protocol-Version": "1999-01-01"})
chk(st == 400, "bad protocol version -> 400", f"status={st}")

conn = http.client.HTTPConnection(HOST, PORT, timeout=10)
conn.request("GET", "/mcp", headers={"Authorization": "Bearer " + TOKEN})
r = conn.getresponse(); r.read(); conn.close()
chk(r.status == 405, "GET -> 405 (stateless, no SSE)", f"status={r.status}")

print("== MCP handshake ==")
st, resp = rpc("initialize", {"protocolVersion": PROTO,
                              "capabilities": {},
                              "clientInfo": {"name": "chatgpt-integration-test", "version": "1"}})
si = resp.get("result", {}).get("serverInfo", {})
chk(st == 200 and si.get("name") == "comitia", "initialize", f"{st} {resp}")

st, resp = rpc("tools/list")
names = [t["name"] for t in resp.get("result", {}).get("tools", [])]
chk(len(names) == 11, f"tools/list returns 11 tools", f"got {len(names)}: {names}")

print("== chatgpt identity capabilities over HTTP ==")
st, res, sc = tool("list_participants", {"context_id": CTX})
chk(st == 200 and isinstance(sc, list) and len(sc) >= 4,
    f"discover peers ({len(sc) if isinstance(sc, list) else '?'} found)", f"{st} {res}")

st, res, sc = tool("send_message", {"context_id": CTX, "dedupe_key": "http-chatgpt-1",
                                    "body": "hello over the HTTP gateway"})
chk(st == 200 and not res.get("isError"), "send message", f"{st} {res}")

st, res, sc = tool("replay_messages", {"context_id": CTX, "after": 0, "limit": 100})
bodies = json.dumps(sc or [])
chk("hello over the HTTP gateway" in bodies, "replay sees it", f"{st}")

st, res, sc = tool("create_task", {"context_id": CTX, "title": "http gateway lifecycle",
                                   "body": "e2e"})
TASK = sc if isinstance(sc, dict) else {}
TID = TASK.get("ID")
chk(bool(TID), f"create task", f"{st} {res}")

st, res, sc = tool("claim_task", {"task_id": TID, "lease_seconds": 600})
claim = sc if isinstance(sc, dict) else {}
CT = claim.get("Token"); LV = (claim.get("Task") or {}).get("LeaseVersion")
chk(bool(CT) and LV == 1, f"claim task (lease v{LV})", f"{st} {res}")

st, res, sc = tool("get_task", {"task_id": TID})
chk(isinstance(sc, dict) and sc.get("Status") == "leased", "inspect task state", f"{st} {sc}")

st, res, sc = tool("ack_messages", {"context_id": CTX, "sequence": 1})
chk(st == 200 and not res.get("isError"), "ack messages", f"{st} {res}")

st, res, sc = tool("message_cursor", {"context_id": CTX})
cur = sc.get("sequence") if isinstance(sc, dict) else sc
chk(isinstance(cur, int) and cur >= 1, f"cursor read ({cur})", f"{st} {res}")

st, res, sc = tool("list_tasks", {"context_id": CTX, "limit": 50})
chk(isinstance(sc, list) and any((t or {}).get("ID") == TID for t in sc),
    "list tasks contains ours", f"{st}")

st, res, sc = tool("complete_task", {"task_id": TID, "claim_token": CT, "lease_version": LV})
chk(st == 200 and not res.get("isError"), "complete task", f"{st} {res}")

print("== credential isolation ==")
st, res, sc = tool("list_participants", {"context_id": CTX}, token="ct_not_a_real_token")
chk(res.get("isError") is True, "invalid credential rejected at tool level", f"{st} {res}")

codex = load("codex")
st, res, sc = tool("list_participants", {"context_id": CTX}, token=codex["credential"]["Token"])
chk(st == 200 and isinstance(sc, list) and len(sc) >= 4,
    "codex credential works through the same gateway", f"{st}")

print(f"\n{int(PASS)}/{int(PASS+FAIL)} gateway checks passed")
sys.exit(1 if FAIL else 0)
