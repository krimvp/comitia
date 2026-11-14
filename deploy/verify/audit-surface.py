#!/usr/bin/env python3
"""What would a network caller see if the whole Comitia HTTP server were exposed?

Counts public vs auth-requiring routes by probing the live daemon with no
credential. This informs the allowlist design for the public gateway.
"""
import http.client, json

LOOP = ("127.0.0.1", 8390)

def probe(method, path, body=None):
    conn = http.client.HTTPConnection(*LOOP, timeout=10)
    headers = {"Content-Type": "application/json"} if body else {}
    conn.request(method, path, body=json.dumps(body) if body else None, headers=headers)
    r = conn.getresponse()
    data = r.read(300).decode("utf-8", "replace")
    conn.close()
    return r.status, data

# Unauthenticated GETs - anything 200 here is served without a credential.
print("=== unauthenticated GET (what the shell exposes) ===")
for p in ["/", "/ui/", "/ui/app.js", "/ui/style.css", "/ui/graph.mjs",
          "/ui/model.mjs", "/ui/sequence.mjs",
          "/v1/contexts/list", "/debug", "/metrics", "/admin",
          "/.well-known/oauth-protected-resource", "/mcp"]:
    try:
        st, body = probe("GET", p)
        note = ""
        if st == 200:
            note = "PUBLIC (no auth)"
        elif st == 404:
            note = "not found"
        elif st == 405:
            note = "route exists, wrong method"
        elif st == 401:
            note = "auth required"
        print(f"  GET  {p:<42} -> {st}  {note}")
    except Exception as e:
        print(f"  GET  {p:<42} -> ERROR {type(e).__name__}")

print()
print("=== unauthenticated POST to MCP-ish paths ===")
for p in ["/mcp", "/v1/messages/replay"]:
    try:
        st, body = probe("POST", p, {"context_id": "x", "after": 0, "limit": 1})
        print(f"  POST {p:<42} -> {st}  {body[:80]!r}")
    except Exception as e:
        print(f"  POST {p:<42} -> ERROR {type(e).__name__}")
