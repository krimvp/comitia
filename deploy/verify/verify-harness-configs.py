#!/usr/bin/env python3
"""Verify that each harness's *configured* MCP command actually works.

Reads the command/args that Claude Code and Codex recorded, spawns exactly that,
and performs an MCP handshake against the live daemon.
"""
import json, os, pathlib, subprocess, re

INIT = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize",
                   "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                              "clientInfo": {"name": "verify", "version": "1"}}}) + "\n"

def probe(label, cmd, args, env_extra=None):
    env = dict(os.environ)
    env["COMITIA_SERVER_URL"] = "http://127.0.0.1:8390"
    env.update(env_extra or {})
    try:
        p = subprocess.run([cmd] + args, input=INIT, capture_output=True,
                           text=True, timeout=30, env=env)
        line = (p.stdout or "").strip().splitlines()
        if not line:
            return label, False, f"no output; stderr={p.stderr[:200]}"
        d = json.loads(line[0])
        si = d.get("result", {}).get("serverInfo", {})
        return label, si.get("name") == "comitia", f"serverInfo={si}"
    except Exception as e:
        return label, False, f"{type(e).__name__}: {e}"

results = []

# --- Claude Code: ~/.claude.json -> mcpServers.comitia
cj = json.loads(pathlib.Path("/root/.claude.json").read_text())
c = cj["mcpServers"]["comitia"]
results.append(probe("Claude Code (verbatim config command)",
                     c["command"], c.get("args", []), c.get("env")))

# --- Codex: ~/.codex/config.toml -> mcp_servers.comitia
toml = pathlib.Path("/root/.codex/config.toml").read_text()
m = re.search(r'\[mcp_servers\.comitia\](.*?)(?=\n\[|\Z)', toml, re.S)
block = m.group(1) if m else ""
cmd = re.search(r'command\s*=\s*"([^"]+)"', block)
args = re.findall(r'"([^"]+)"', (re.search(r'args\s*=\s*\[([^\]]*)\]', block) or
                                  re.match(r'()', '')).group(1))
if cmd:
    results.append(probe("Codex (verbatim config command)",
                         cmd.group(1), args))
else:
    results.append(("Codex config parse", False, "no mcp_servers.comitia block"))

fails = 0
for name, ok, detail in results:
    print(f"{'PASS' if ok else 'FAIL'}  {name}  -> {detail}")
    fails += 0 if ok else 1
print(f"\n{len(results)-fails}/{len(results)} harness configs verified")
raise SystemExit(1 if fails else 0)
