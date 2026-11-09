#!/usr/bin/env python3
"""Bounded, reproducible subprocess stress test for the Comitia CLI."""
import argparse, json, os, secrets, subprocess, tempfile, time
from threading import Barrier
from concurrent.futures import ThreadPoolExecutor, as_completed

# Above the CLI's 15 s SQLite write-lock wait, so a slow lock reports the CLI error.
TIMEOUT = 30

def call(binary, db, credential, command, *args, timeout=TIMEOUT):
    env = dict(os.environ); env["COMITIA_CREDENTIAL"] = credential
    env.pop("COMITIA_SERVER_URL", None)
    try:
        p = subprocess.run([binary, "--db", db, command, *args], text=True,
                           capture_output=True, env=env, timeout=timeout)
    except subprocess.TimeoutExpired:
        key = args[args.index("--dedupe-key") + 1] if "--dedupe-key" in args else ""
        raise RuntimeError(" ".join(filter(None, (command, key))) + " timed out") from None
    try: value = json.loads(p.stdout) if p.stdout.strip() else None
    except Exception: value = None
    return p.returncode, value, p.stderr.strip()

def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--binary", required=True)
    ap.add_argument("--workers", type=int, default=12); ap.add_argument("--messages", type=int, default=100)
    a = ap.parse_args(); started = time.monotonic()
    if not 1 <= a.workers <= 64 or not 1 <= a.messages <= 10000:
        ap.error("workers must be 1..64 and messages per worker must be 1..10000")
    if not __debug__:
        ap.error("run without -O: assertions must remain enabled")
    workers, messages = a.workers, a.messages
    counts = {"writes": 0, "dedupe_retries": 0, "claims": 0, "claim_wins": 0, "replays": 0, "acks": 0}
    assertions = []
    with tempfile.TemporaryDirectory(prefix="comitia-stress-") as td:
        db = os.path.join(td, "stress.db")
        env = dict(os.environ, COMITIA_BOOTSTRAP_CREDENTIAL=secrets.token_urlsafe(32))
        env.pop("COMITIA_SERVER_URL", None)
        boot = subprocess.run([a.binary, "--db", db, "bootstrap", "--name", "stress"], text=True, capture_output=True, timeout=TIMEOUT, env=env)
        if boot.returncode != 0: raise RuntimeError("bootstrap: " + boot.stderr)
        b = json.loads(boot.stdout); cred = b["credential"]["Token"]
        rc, ws, err = call(a.binary, db, cred, "workspace-create", "--name", "stress-ws"); assert rc == 0, "workspace-create: " + err
        rc, ctx, err = call(a.binary, db, cred, "context-create", "--workspace-id", ws["ID"], "--name", "stress-context"); assert rc == 0, "context-create: " + err
        cid = ctx["ID"]
        def worker(w):
            n = retries = 0
            for i in range(messages):
                key = "w{}-m{}".format(w, i)
                rc, first, err = call(a.binary, db, cred, "send", "--context-id", cid, "--dedupe-key", key, "--body", key)
                if rc != 0: raise RuntimeError("send {}: {}".format(key, err))
                n += 1
                # durable retry of every tenth operation
                if i % 10 == 0:
                    rc2, second, err2 = call(a.binary, db, cred, "send", "--context-id", cid, "--dedupe-key", key, "--body", key)
                    if rc2 != 0 or second != first: raise RuntimeError("send retry {}: dedupe retry mismatch: {}".format(key, err2))
                    retries += 1
            return n, retries
        with ThreadPoolExecutor(max_workers=workers) as ex:
            results = [f.result() for f in as_completed([ex.submit(worker, w) for w in range(workers)])]
            counts["writes"] = sum(x[0] for x in results); counts["dedupe_retries"] = sum(x[1] for x in results)
        assert counts["writes"] == workers * messages and counts["dedupe_retries"] == workers * ((messages + 9)//10)
        rc, task, err = call(a.binary, db, cred, "task-create", "--context-id", cid, "--title", "contended", "--body", "stress")
        assert rc == 0, "task-create: " + err
        barrier = Barrier(workers, timeout=15)
        def claim():
            barrier.wait()
            return call(a.binary, db, cred, "task-claim", "--task-id", task["ID"], "--lease", "60s")
        with ThreadPoolExecutor(max_workers=workers) as ex: results = [f.result() for f in as_completed([ex.submit(claim) for _ in range(workers)])]
        counts["claims"], counts["claim_wins"] = len(results), sum(x[0] == 0 for x in results)
        assert counts["claim_wins"] == 1, "expected exactly one lease owner"
        bad = [err for rc, _, err in results if rc != 0 and json.loads(err).get("error") != "conflict"]
        assert not bad, "task-claim: unexpected claim failure: " + "; ".join(bad)
        winner = next(value for rc, value, _ in results if rc == 0)
        def finish(claim):
            return call(a.binary, db, cred, "task-complete", "--task-id", claim["Task"]["ID"],
                        "--claim-token", claim["Token"], "--lease-version", str(claim["Task"]["LeaseVersion"]))
        assert finish(winner)[0] == 0, "winning claim could not complete"
        rc, probe, err = call(a.binary, db, cred, "task-create", "--context-id", cid, "--title", "expiry-probe")
        assert rc == 0, "task-create probe: " + err
        rc, stale, err = call(a.binary, db, cred, "task-claim", "--task-id", probe["ID"], "--lease", "1s")
        assert rc == 0, "task-claim probe: " + err
        time.sleep(1.05)  # leases last at least 1s
        rc, fresh, err = call(a.binary, db, cred, "task-claim", "--task-id", probe["ID"], "--lease", "60s")
        assert rc == 0 and fresh["Task"]["LeaseVersion"] > stale["Task"]["LeaseVersion"], "reclaim failed"
        rc, _, err = finish(stale)
        assert rc != 0 and json.loads(err).get("error") == "stale task claim", "stale completion accepted"
        assert finish(fresh)[0] == 0, "fresh claim failed"
        counts["expiry_reclaims"] = 1
        counts["stale_completions_rejected"] = 1
        replay=[]; after=0
        while True:
            rc, page, err = call(a.binary, db, cred, "replay", "--context-id", cid, "--after", str(after), "--limit", "1000"); assert rc == 0, "replay: " + err
            if not page: break
            replay.extend(page); after=page[-1]["Sequence"]
        counts["replays"] = len(replay); last = replay[-1]["Sequence"] if replay else 0
        assert [x["Sequence"] for x in replay] == list(range(1,last+1)), "replay sequence gap"
        # Task operations append server-authored task events; the worker writes are the rest.
        writes = [x for x in replay if (x.get("envelope") or {}).get("schema") != "comitia.task-event.v1"]
        counts["task_events"] = len(replay) - len(writes)
        assert len(writes) == workers * messages, "unexpected write count"
        expected = {f"w{w}-m{i}" for w in range(workers) for i in range(messages)}
        assert {x["DedupeKey"] for x in writes} == expected, "lost or unexpected logical writes"
        assert len({x["ID"] for x in replay}) == len(replay), "duplicate message IDs"
        assert all(x["Body"] == x["DedupeKey"] for x in writes), "message body corruption"
        if last: assert call(a.binary, db, cred, "ack", "--context-id", cid, "--sequence", str(last))[0] == 0; counts["acks"] = 1
        rc, cur, _ = call(a.binary, db, cred, "cursor", "--context-id", cid)
        assert rc == 0 and cur["sequence"] == last, "cursor mismatch after reopen"
        assertions = ["exact writes and retry identities", "full ordered replay and bodies", "single task claim winner", "expired lease reclaimed", "stale completion rejected", "cursor survives reopen"]
    summary = {"operations": counts, "assertions": assertions, "runtime_seconds": round(time.monotonic()-started, 3), "workers": workers, "messages_per_worker": messages}
    print(json.dumps(summary, sort_keys=True))
if __name__ == "__main__": main()
