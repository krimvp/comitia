#!/usr/bin/env python3
"""Verify the Comitia MCP stdio adapter against the running HTTP daemon.

Spawns `comitia mcp` (which acts as a thin HTTP client), performs the MCP
handshake, lists tools, and calls a read-only tool plus a write/replay cycle.
"""
import json, os, pathlib, subprocess, sys

BIN = "/opt/comitia/bin/comitia"
SESS = pathlib.Path("/root/.comitia/sessions/claude.json")
d = json.loads(SESS.read_text())
TOKEN = d["credential"]["Token"]
CTX = [l.split("=", 1)[1].strip()
       for l in pathlib.Path("/root/.comitia/ids.env").read_text().splitlines()
       if l.startswith("COMITIA_CONTEXT_ID=")][0]

env = dict(os.environ)
env["COMITIA_SERVER_URL"] = "http://127.0.0.1:8390"
env["COMITIA_CREDENTIAL"] = TOKEN
# Ensure embedded SQLite is NOT used.
env.pop("COMITIA_DB", None)

p = subprocess.Popen([BIN, "mcp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                     stderr=subprocess.PIPE, env=env, text=True, bufsize=1)

def send(obj):
    p.stdin.write(json.dumps(obj) + "\n")
    p.stdin.flush()

def recv():
    line = p.stdout.readline()
    if not line:
        err = p.stderr.read()
        raise SystemExit("MCP server closed. stderr:\n" + err)
    return json.loads(line)

results = []

send({"jsonrpc": "2.0", "id": 1, "method": "initialize",
      "params": {"protocolVersion": "2025-06-18",
                 "capabilities": {}, "clientInfo": {"name": "verify", "version": "1"}}})
init = recv()
si = init.get("result", {}).get("serverInfo", {})
results.append(("initialize handshake", si.get("name") == "comitia", si))

send({"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}})

send({"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}})
tl = recv()
names = [t["name"] for t in tl.get("result", {}).get("tools", [])]
results.append(("tools/list returns 11 tools", len(names) == 11, names))

send({"jsonrpc": "2.0", "id": 3, "method": "tools/call",
      "params": {"name": "list_participants", "arguments": {"context_id": CTX}}})
res = recv()
ok = "result" in res and not res["result"].get("isError")
results.append(("read-only tool call (list_participants)", ok, res.get("error") or "ok"))

send({"jsonrpc": "2.0", "id": 4, "method": "tools/call",
      "params": {"name": "send_message",
                 "arguments": {"context_id": CTX, "dedupe_key": "verify-mcp-1",
                               "body": "hello from MCP verification"}}})
res = recv()
ok = "result" in res and not res["result"].get("isError")
results.append(("write tool call (send_message)", ok, res.get("error") or "ok"))

send({"jsonrpc": "2.0", "id": 5, "method": "tools/call",
      "params": {"name": "replay_messages",
                 "arguments": {"context_id": CTX, "after": 0, "limit": 100}}})
res = recv()
body = json.dumps(res)
results.append(("replay via MCP sees the message",
                "hello from MCP verification" in body, "ok" if "error" not in res else res["error"]))

p.stdin.close()
p.wait(timeout=10)

fails = 0
for name, ok, detail in results:
    print(f"{'PASS' if ok else 'FAIL'}  {name}  -> {detail}")
    fails += 0 if ok else 1
print(f"\n{len(results)-fails}/{len(results)} MCP checks passed")
sys.exit(1 if fails else 0)
