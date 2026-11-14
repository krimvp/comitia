#!/usr/bin/env python3
"""Final security posture: nothing about Comitia is publicly reachable."""
import os, socket, subprocess, json, pathlib, sys, urllib.request

PUB = os.environ.get("COMITIA_PUBLIC_IP") or sys.exit("set COMITIA_PUBLIC_IP to the host's public address")
V6 = os.environ.get("COMITIA_PUBLIC_IP6", "")  # optional IPv6 address
results = []

# Comitia ports must be unreachable from outside.
for host, label in ((PUB, "v4"), (V6, "v6")) if V6 else ((PUB, "v4"),):
    for port in (8390, 18390):
        try:
            fam = socket.AF_INET6 if ":" in host else socket.AF_INET
            s = socket.socket(fam, socket.SOCK_STREAM); s.settimeout(4)
            rc = s.connect_ex((host, port)); s.close()
            results.append((f"port {port} closed from internet ({label})", rc != 0,
                            "closed" if rc != 0 else "OPEN"))
        except Exception as e:
            results.append((f"port {port} check ({label})", True, f"unreachable ({type(e).__name__})"))

# Public HTTPS on the bare IP belongs to the neighbouring site, never Comitia.
try:
    r = urllib.request.urlopen(f"https://{PUB}/v1/contexts/list", timeout=10,
                               context=__import__("ssl")._create_unverified_context())
    body = r.read(200).decode("utf-8", "replace")
    results.append(("public 443 does not serve Comitia", "comitia" not in body.lower(), body[:80]))
except urllib.error.HTTPError as e:
    body = e.read(200).decode("utf-8", "replace")
    results.append(("public 443 does not serve Comitia", "comitia" not in body.lower(), f"HTTP {e.code}"))
except Exception as e:
    results.append(("public 443 does not serve Comitia", True, f"{type(e).__name__}"))

# Loopback listener exists and is bound only to 127.0.0.1.
out = subprocess.run(["ss", "-tln"], capture_output=True, text=True).stdout
rows = [l for l in out.splitlines() if ":8390" in l]
results.append(("comitia listening on loopback only",
                bool(rows) and all("127.0.0.1:8390" in r for r in rows),
                rows[0].split()[3] if rows else "not listening"))

# Credential file hygiene.
f = pathlib.Path("/root/.comitia/sessions/claude.json")
mode = oct(f.stat().st_mode & 0o777)
results.append(("session credentials mode 0600", mode == "0o600", mode))

d = pathlib.Path("/root/.comitia").stat().st_mode & 0o777
results.append(("~/.comitia dir mode 0700", oct(d) == "0o700", oct(d)))

fails = 0
for name, ok, detail in results:
    print(f"{'PASS' if ok else 'FAIL'}  {name}  -> {detail}")
    fails += 0 if ok else 1
print(f"\n{len(results)-fails}/{len(results)} posture checks passed")
sys.exit(1 if fails else 0)
