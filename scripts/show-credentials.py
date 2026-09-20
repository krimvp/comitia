#!/usr/bin/env python3
"""Print all active Comitia identities and their connection details.

Writes a consolidated handout to /root/.comitia/credentials.txt (mode 0600).
"""
import json, pathlib, datetime

D = pathlib.Path("/root/.comitia")
S = D / "sessions"

def ids():
    out = {}
    for line in (D / "ids.env").read_text().splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip()
    return out

env = ids()
rows = []

owner = json.loads((D / "owner.json").read_text())
rows.append(("owner", owner, "admin (full authority)"))

for f in sorted(S.glob("*.json")):
    d = json.loads(f.read_text())
    perms = ", ".join(sorted({str(g.get("Permissions")) for g in d.get("grants", [])})) or "-"
    rows.append((f.stem, d, f"context-scoped, permissions bitmap {perms}"))

lines = []
w = lines.append
w("COMITIA CREDENTIALS")
w(f"generated: {datetime.datetime.now().isoformat(timespec='seconds')}")
w("")
w("Public MCP endpoint : https://vmi3260467.contaboserver.net:8443/mcp")
w("HTTP header         : Authorization: Bearer <credential>")
w("Host                : 45.88.191.59  (vmi3260467.contaboserver.net)")
w("")
w(f"workspace id        : {env.get('COMITIA_WORKSPACE_ID')}")
w(f"context id          : {env.get('COMITIA_CONTEXT_ID')}")
w("")
w("=" * 72)

for name, d, note in rows:
    cred = d["credential"]
    w("")
    w(f"identity    : {name}")
    w(f"note        : {note}")
    w(f"principal   : {d['principal']['ID']}")
    w(f"session     : {cred['Session']['ID']}")
    w(f"expires     : {cred['Session']['ExpiresAt']}")
    w(f"credential  : {cred['Token']}")
    w(f"revoke with : comitia revoke --session-id {cred['Session']['ID']}")
    w("-" * 72)

w("")
w("KEEP THIS PRIVATE. Anyone with a credential can act as that identity")
w("within its granted contexts. Tokens are stored per-harness in")
w("/root/.comitia/sessions/<name>.json (mode 0600); this file is 0600 too.")

text = "\n".join(lines) + "\n"
out = D / "credentials.txt"
out.write_text(text)
out.chmod(0o600)
print(text)
print(f"[written to {out} (mode 0600)]")
