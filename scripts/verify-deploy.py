#!/usr/bin/env python3
"""Post-deploy checks: inspector UI, public exposure, restart resilience."""
import http.client, json, os, pathlib, socket, subprocess, time, sys

PUBLIC_IP = os.environ.get("COMITIA_PUBLIC_IP") or sys.exit("set COMITIA_PUBLIC_IP to the host's public address")
results = []

# 1. Inspector UI is served.
try:
    conn = http.client.HTTPConnection("127.0.0.1", 8390, timeout=10)
    conn.request("GET", "/ui/")
    r = conn.getresponse()
    body = r.read().decode("utf-8", "replace")
    results.append(("inspector UI served at /ui/", r.status == 200 and "html" in body.lower(), r.status))
    conn.close()
except Exception as e:
    results.append(("inspector UI served at /ui/", False, repr(e)))

# 2. Port 8390 must NOT be reachable from the public IP.
try:
    s = socket.socket(); s.settimeout(5)
    rc = s.connect_ex((PUBLIC_IP, 8390))
    s.close()
    results.append(("port 8390 closed to internet", rc != 0, "closed" if rc != 0 else "OPEN"))
except Exception as e:
    results.append(("port 8390 closed to internet", False, repr(e)))

# 3. Port should be loopback-bound only.
out = subprocess.run(["ss", "-tln"], capture_output=True, text=True).stdout
line = [l for l in out.splitlines() if ":8390" in l]
bound_lo = bool(line) and "127.0.0.1:8390" in line[0]
results.append(("listener bound to 127.0.0.1 only", bound_lo, line[0].split()[3] if line else "not listening"))

# 4. Restart resilience: restart the unit, confirm API returns and data persisted.
subprocess.run(["systemctl", "--user", "restart", "comitia-api.service"],
               env={"XDG_RUNTIME_DIR": "/run/user/0", "PATH": "/usr/bin:/bin"},
               capture_output=True, text=True)
time.sleep(3)
cred = json.loads(pathlib.Path("/root/.comitia/owner.json").read_text())["credential"]["Token"]
ctx = [l.split("=", 1)[1].strip() for l in
       pathlib.Path("/root/.comitia/ids.env").read_text().splitlines()
       if l.startswith("COMITIA_CONTEXT_ID=")][0]
try:
    conn = http.client.HTTPConnection("127.0.0.1", 8390, timeout=10)
    conn.request("POST", "/v1/messages/replay",
                 body=json.dumps({"context_id": ctx, "after": 0, "limit": 100}),
                 headers={"Content-Type": "application/json",
                          "Authorization": "Bearer " + cred})
    r = conn.getresponse()
    body = r.read().decode()
    conn.close()
    survived = r.status == 200 and "deploy verification" in body
    results.append(("data survives service restart", survived, r.status))
except Exception as e:
    results.append(("data survives service restart", False, repr(e)))

# 5. Boot persistence flags.
en = subprocess.run(["systemctl", "--user", "is-enabled", "comitia-api.service"],
                    env={"XDG_RUNTIME_DIR": "/run/user/0", "PATH": "/usr/bin:/bin"},
                    capture_output=True, text=True).stdout.strip()
results.append(("unit enabled for boot", en == "enabled", en))
li = subprocess.run(["loginctl", "show-user", "root", "--property=Linger"],
                    capture_output=True, text=True).stdout.strip()
results.append(("linger on (starts without login)", "yes" in li.lower(), li))

fails = 0
for name, ok, detail in results:
    print(f"{'PASS' if ok else 'FAIL'}  {name}  -> {detail}")
    fails += 0 if ok else 1
print(f"\n{len(results)-fails}/{len(results)} checks passed")
sys.exit(1 if fails else 0)
