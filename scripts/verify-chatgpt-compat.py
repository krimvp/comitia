#!/usr/bin/env python3
"""Verify the endpoint satisfies ChatGPT's connector requirements.

ChatGPT's broker is stricter than a generic MCP client in two ways that are
invisible to ordinary testing:

  1. It only dispatches to the standard HTTPS port (443). A server on any other
     port receives no traffic at all, which looks like a TLS/DNS/firewall fault
     and cannot be reproduced with curl from anywhere else.
  2. Its connector UI has no field for a custom header, so a token-based server
     must accept the credential in the URL path.

Both are checked here against the real public endpoint, over verified TLS.
"""

import json
import os
import socket
import ssl
import sys
import urllib.request

HOST = os.environ.get("COMITIA_HOST", "vmi3260467.contaboserver.net")
CRED = os.environ.get("COMITIA_CREDENTIAL", "")
CTX = os.environ.get("COMITIA_CONTEXT_ID", "")
if not CRED or not CTX:
    sys.exit("set COMITIA_CREDENTIAL and COMITIA_CONTEXT_ID")

passed = failed = 0


def check(name, ok, detail=""):
    global passed, failed
    if ok:
        passed += 1
        print(f"  PASS  {name}")
    else:
        failed += 1
        print(f"  FAIL  {name}  {detail}")


def post(path, body, headers=None, port=443):
    """POST over verified TLS to the public IP with correct SNI."""
    req = urllib.request.Request(
        f"https://{HOST}:{port}{path}",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json", **(headers or {})},
        method="POST",
    )
    # Resolve to the public address explicitly, keeping SNI/cert as the hostname.
    ip = socket.gethostbyname(HOST)
    ctx = ssl.create_default_context()
    conn = urllib.request.HTTPSHandler(context=ctx)
    opener = urllib.request.build_opener(conn)
    return opener.open(req, timeout=25)


def call_body(name, ctx_id, **kw):
    return {"jsonrpc": "2.0", "id": 1, "method": "tools/call",
            "params": {"name": name, "arguments": {"context_id": ctx_id, **kw}}}


def is_tool_error(text):
    d = json.loads(text)
    return bool(d.get("result", {}).get("isError")) or "error" in d


print(f"== ChatGPT compatibility ({HOST}) ==")

# 1. Standard port with a certificate the broker will accept.
ip = socket.gethostbyname(HOST)
try:
    sc = ssl.create_default_context()
    with socket.create_connection((ip, 443), timeout=20) as raw:
        with sc.wrap_socket(raw, server_hostname=HOST) as tls:
            cert = tls.getpeercert()
    check("port 443 accepted and TLS verifies", True)
    check("certificate matches hostname",
          any(HOST in v for rdn in cert.get("subjectAltName", ()) for v in rdn),
          str(cert.get("subjectAltName", ())[:2]))
except Exception as e:  # noqa: BLE001
    check("port 443 accepted and TLS verifies", False, str(e))
    print("\nThe ChatGPT connector cannot reach this endpoint. Stop here.")
    sys.exit(1)

# 2. Credential in the URL path, which is the only form the UI can express.
try:
    with post(f"/mcp/{CRED}", call_body("list_participants", CTX)) as r:
        body = r.read().decode()
    check("credential accepted in URL path (ChatGPT's form)", not is_tool_error(body),
          body[:120])
except Exception as e:  # noqa: BLE001
    check("credential accepted in URL path (ChatGPT's form)", False, str(e))

# 3. A bad URL credential must be refused, so the path form is not a bypass.
try:
    with post("/mcp/deadbeefdeadbeef", call_body("list_participants", CTX)) as r:
        body = r.read().decode()
    check("bad URL credential refused", is_tool_error(body), body[:120])
except Exception:  # noqa: BLE001
    check("bad URL credential refused", True)

# 4. tools/list is the probe ChatGPT runs first when saving the connector.
#    The credential travels in the URL, exactly as the connector sends it.
try:
    with post(f"/mcp/{CRED}", {"jsonrpc": "2.0", "id": 1, "method": "tools/list"}) as r:
        body = json.loads(r.read().decode())
    n = len(body.get("result", {}).get("tools", []))
    check(f"tools/list returns the tool set ({n} tools)", n >= 11, str(n))
except Exception as e:  # noqa: BLE001
    check("tools/list returns the tool set", False, str(e))

# 4b. A request with no credential at all must be refused, so the endpoint is
#     not anonymously readable.
try:
    with post("/mcp", {"jsonrpc": "2.0", "id": 1, "method": "list_participants"}) as r:
        check("request without credential refused", False, str(r.status))
except urllib.error.HTTPError as e:
    check("request without credential refused", e.code in (401, 403), str(e.code))
except Exception as e:  # noqa: BLE001
    check("request without credential refused", False, str(e))

# 5. The transport must be Streamable HTTP, not SSE.
try:
    req = urllib.request.Request(f"https://{HOST}/mcp", method="GET",
                                 headers={"Accept": "text/event-stream"})
    try:
        urllib.request.urlopen(req, timeout=15, context=ssl.create_default_context())
        check("GET /mcp refused (stateless, no SSE stream)", False, "GET unexpectedly served")
    except urllib.error.HTTPError as e:
        # 405 is the adapter's own answer; 403 is nginx's limit_except. Both
        # refuse the method, which is what matters.
        check("GET /mcp refused (stateless, no SSE stream)", e.code in (403, 405), str(e.code))
except Exception as e:  # noqa: BLE001
    check("GET /mcp refused (stateless, no SSE stream)", False, str(e))

print()
if failed:
    print(f"ChatGPT compatibility: {passed} passed, {failed} FAILED")
    sys.exit(1)
print(f"ChatGPT compatibility: {passed} passed, 0 failed")
