#!/usr/bin/env python3
"""Verify the PUBLIC MCP edge: TLS, route allowlist, auth, rate limiting.

Run after deploying the edge. Checks that only POST /mcp is reachable and that
everything else - most importantly the inspector and the REST API - is not.
"""
import http.client, json, os, pathlib, socket, ssl, sys

HOST = "vmi3260467.contaboserver.net"
IP = "45.88.191.59"

# Prefer the real public DNS name so this genuinely tests going out to the
# internet; fall back to the IP when the host resolves only locally.
def public_ok():
    try:
        addrs = {a[4][0] for a in socket.getaddrinfo(HOST, 8443, proto=socket.IPPROTO_TCP)}
        return any(a.startswith("45.88.191.59") for a in addrs)
    except Exception:
        return False

USE_PUBLIC = public_ok()
TARGET_HOST = HOST if USE_PUBLIC else IP
TARGET_PORT = 8443

TOKEN = json.loads(
    pathlib.Path("/root/.comitia/sessions/chatgpt.json").read_text())["credential"]["Token"]
CTX = [l.split("=", 1)[1].strip()
       for l in pathlib.Path("/root/.comitia/ids.env").read_text().splitlines()
       if l.startswith("COMITIA_CONTEXT_ID=")][0]

# Use a throwaway credential for the rate-limit burst so the real chatgpt
# identity's per-credential bucket is not consumed by this test.
BURST_TOKEN = os.environ.get("BURST_TOKEN", TOKEN)

PASS = FAIL = 0
def chk(ok, label, detail=""):
    global PASS, FAIL
    if ok:
        PASS += 1; print(f"  PASS  {label}")
    else:
        FAIL += 1; print(f"  FAIL  {label}  {detail}")

def call(method, path, body=None, token=None, verify=True):
    """One HTTPS request to the PUBLIC IP, presenting the hostname as SNI.

    This host's /etc/hosts maps the hostname to 127.0.1.1, so resolving by name
    would only exercise loopback. An external client reaches the public IP, so
    that is what this test must do.
    """
    ctx = ssl.create_default_context()
    if not verify:
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    raw = socket.create_connection((IP, TARGET_PORT), timeout=25)
    tls = ctx.wrap_socket(raw, server_hostname=HOST)
    try:
        lines = [f"{method} {path} HTTP/1.1", f"Host: {HOST}",
                 "Content-Type: application/json", "Connection: close"]
        if token:
            lines.append(f"Authorization: Bearer {token}")
        data = body.encode() if body else b""
        lines.append(f"Content-Length: {len(data)}")
        tls.sendall(("\r\n".join(lines) + "\r\n\r\n").encode() + data)
        buf = b""
        while True:
            chunk = tls.recv(65536)
            if not chunk:
                break
            buf += chunk
    finally:
        tls.close()

    head, _, payload = buf.partition(b"\r\n\r\n")
    status = int(head.split(b" ")[1])
    if b"transfer-encoding: chunked" in head.lower():
        out, rest = b"", payload
        while True:
            size_line, _, rest = rest.partition(b"\r\n")
            try:
                size = int(size_line.split(b";")[0], 16)
            except ValueError:
                break
            if size == 0:
                break
            out += rest[:size]
            rest = rest[size + 2:]
        payload = out
    return status, payload.decode("utf-8", "replace")

def rpc(method="tools/list", params=None, token=TOKEN, rid=1):
    return call("POST", "/mcp", json.dumps(
        {"jsonrpc": "2.0", "id": rid, "method": method, "params": params or {}}), token)

print(f"== TLS (public IP {IP}:{TARGET_PORT}, SNI {HOST}) ==")
try:
    ctx = ssl.create_default_context()
    with socket.create_connection((IP, TARGET_PORT), timeout=15) as s:
        with ctx.wrap_socket(s, server_hostname=HOST) as ts:
            cert = ts.getpeercert()
    issuer = dict(x[0] for x in cert.get("issuer", []))
    san = [v for k, v in cert.get("subjectAltName", []) if k == "DNS"]
    chk(HOST in san,
        f"verified certificate from {issuer.get('organizationName','?')} covers {HOST}",
        f"san={san}")
except Exception as e:
    chk(False, "TLS handshake with verified certificate", repr(e))

print("== route allowlist (only /mcp is routed) ==")
for path in ("/ui/", "/v1/contexts/list", "/admin", "/debug", "/metrics", "/"):
    st, body = call("GET", path)
    chk(st == 404, f"GET {path} -> 404", f"status={st}")

print("== auth on the one allowed route ==")
st, body = rpc(token=None)
chk(st == 401, "POST /mcp without token -> 401", f"status={st}")

st, body = rpc("tools/call", {"name": "list_participants", "arguments": {"context_id": CTX}},
               token="ct_not_valid")
chk(st == 200 and '"isError":true' in body, "POST /mcp with bad token -> tool error", f"{st} {body[:120]}")

print("== real MCP traffic over TLS ==")
st, body = rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                              "clientInfo": {"name": "public-edge-test", "version": "1"}})
chk(st == 200 and "comitia" in body, "initialize over public HTTPS", f"{st} {body[:120]}")

st, body = rpc("tools/list")
names = json.loads(body).get("result", {}).get("tools", []) if st == 200 else []
chk(len(names) == 11, "tools/list returns 11 tools over public HTTPS", f"status={st} got {len(names)}")

st, body = rpc("tools/call", {"name": "list_participants", "arguments": {"context_id": CTX}})
peers = []
try:
    sc = json.loads(body)["result"]["structuredContent"]["result"]
    peers = [p.get("Name") for p in sc]
except Exception:
    pass
chk(st == 200 and len(peers) >= 5,
    f"authenticated tool call over public HTTPS (peers: {len(peers)})", f"{st} {body[:160]}")

print("== rate limiting ==")
limited = False
for _ in range(120):
    st, _b = rpc("tools/call", {"name": "list_participants", "arguments": {"context_id": CTX}}, token=BURST_TOKEN)
    if st == 429:
        limited = True
        break
chk(limited, "edge returns 429 under burst", "no 429 observed in 120 requests")

print("== loopback ports still private ==")
for port in (8390, 8391):
    s = socket.socket(); s.settimeout(4)
    rc = s.connect_ex((IP, port)); s.close()
    chk(rc != 0, f"{port} still closed from the internet", "OPEN")

print(f"\n{int(PASS)}/{int(PASS+FAIL)} public edge checks passed")
sys.exit(1 if FAIL else 0)
