#!/usr/bin/env python3
"""Reproduce Hermes' actual handshake strategy: discover-first, then fall back.

Hermes (tools/mcp_tool_transport.py) tries `server/discover` first and treats
-32022 / initialize -32601 as the signal to fall back to `initialize`. This
script probes the raw wire so we can see exactly what the gateway answers.
"""
import http.client, json, pathlib

TOKEN = json.loads(
    pathlib.Path("/root/.comitia/sessions/hermes.json").read_text())["credential"]["Token"]
URL_HOST, URL_PORT = "127.0.0.1", 8391


def raw(body, headers=None):
    conn = http.client.HTTPConnection(URL_HOST, URL_PORT, timeout=30)
    h = {"Content-Type": "application/json",
         "Authorization": "Bearer " + TOKEN}
    h.update(headers or {})
    conn.request("POST", "/mcp", body=json.dumps(body), headers=h)
    r = conn.getresponse()
    data = r.read().decode()
    conn.close()
    return r.status, data


print("=== 1. server/discover (what Hermes probes first) ===")
st, body = raw({"jsonrpc": "2.0", "id": 1, "method": "server/discover",
                "params": {"_meta": {"io.modelcontextprotocol/protocolVersion": "2026-07-28",
                                     "io.modelcontextprotocol/clientCapabilities": {}}}})
print("status:", st)
print("body  :", body[:400])

print()
print("=== 2. initialize with modern version header ===")
st, body = raw({"jsonrpc": "2.0", "id": 2, "method": "initialize",
                "params": {"protocolVersion": "2026-07-28",
                           "capabilities": {},
                           "clientInfo": {"name": "probe", "version": "1"}}},
               {"mcp-protocol-version": "2026-07-28"})
print("status:", st)
print("body  :", body[:400])

print()
print("=== 3. initialize as legacy client ===")
st, body = raw({"jsonrpc": "2.0", "id": 3, "method": "initialize",
                "params": {"protocolVersion": "2025-06-18",
                           "capabilities": {},
                           "clientInfo": {"name": "probe", "version": "1"}}})
print("status:", st)
print("body  :", body[:400])

print()
print("=== 4. tools/list without prior initialize (stateless) ===")
st, body = raw({"jsonrpc": "2.0", "id": 4, "method": "tools/list", "params": {}})
print("status:", st)
print("body  :", body[:200])
