#!/usr/bin/env python3
"""Print all active Comitia identities and their connection details.

Writes a consolidated handout to $COMITIA_HOME/credentials.txt (mode 0600;
COMITIA_HOME defaults to /root/.comitia). Set COMITIA_PUBLIC_HOST (required),
COMITIA_PUBLIC_PORT (default 8443) and optionally COMITIA_PUBLIC_IP.
"""
import json, os, pathlib, datetime, sys, tempfile

D = pathlib.Path(os.environ.get("COMITIA_HOME", "/root/.comitia"))
S = D / "sessions"
HOST = os.environ.get("COMITIA_PUBLIC_HOST") or sys.exit("set COMITIA_PUBLIC_HOST to the public MCP hostname")
PORT = os.environ.get("COMITIA_PUBLIC_PORT", "8443")
IP = os.environ.get("COMITIA_PUBLIC_IP", "")

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
w(f"Public MCP endpoint : https://{HOST}:{PORT}/mcp")
w("HTTP header         : Authorization: Bearer <credential>")
w(f"Host                : {IP}  ({HOST})" if IP else f"Host                : {HOST}")
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
w(f"{S}/<name>.json (mode 0600); this file is 0600 too.")

text = "\n".join(lines) + "\n"
out = D / "credentials.txt"
# mkstemp creates the file 0600 from the start; the rename publishes it whole.
fd, tmp = tempfile.mkstemp(dir=D, prefix=".credentials.txt.")
try:
    with os.fdopen(fd, "w") as fh:
        fh.write(text)
    os.replace(tmp, out)
except BaseException:
    os.unlink(tmp)
    raise
print(text)
print(f"[written to {out} (mode 0600)]")
