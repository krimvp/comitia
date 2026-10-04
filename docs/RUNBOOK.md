# Comitia — operations runbook

Deployed 2026-09-19 on the Contabo VPS (45.88.191.59 /
vmi3260467.contaboserver.net). Comitia coordinates harnesses that run
elsewhere; it does not execute them.

For how to *use* Comitia from a harness, see [USING-COMITIA.md](USING-COMITIA.md).
For the ChatGPT-specific analysis, see [chatgpt-integration.md](chatgpt-integration.md).

## Layout

| Path | What |
| --- | --- |
| `/root/workspace/comitia` | source checkout (`go build` / `go test` here) |
| `/opt/comitia/bin/comitia` | installed binary |
| `/opt/comitia/bin/comitia-mcp-session` | MCP stdio wrapper, binds one session by name |
| `/var/lib/comitia/comitia.db` | SQLite database (authoritative state) |
| `/root/.comitia/owner.json` | owner credential, 1 year, mode 0600 |
| `/root/.comitia/sessions/<harness>.json` | per-harness scoped sessions, mode 0600 |
| `/root/.comitia/ids.env` | workspace + context IDs (no secrets) |
| `~/.config/systemd/user/comitia-api.service` | REST API + inspector, `127.0.0.1:8390` |
| `~/.config/systemd/user/comitia-mcp-http.service` | MCP streamable HTTP, `127.0.0.1:8391` |
| `/etc/nginx/sites-available/comitia-mcp` | public TLS edge on `:8443` |

Toolchain: Go 1.24.4 from apt (`/usr/lib/go-1.24/bin`). `go` is not on PATH by
default — `export PATH=/usr/lib/go-1.24/bin:$PATH`.

## Topology

```
                    Internet
                       |  HTTPS :8443, only POST /mcp
                       v
            nginx (TLS, rate limit, route allowlist)
                       |
                       | 127.0.0.1:8391
                       v
        comitia mcp-http (streamable HTTP MCP)
                       |
                       | 127.0.0.1:8390
                       v
                 Comitia daemon  --  SQLite /var/lib/comitia/comitia.db
                       ^
                       | SSH tunnel / loopback
                  Codex CLI, Claude Code, local scripts
```

Comitia has no internet-facing listener of its own. `8390` and `8391` are
loopback-only and verified closed from the internet on IPv4 and IPv6.

## Service management

```sh
export XDG_RUNTIME_DIR=/run/user/0
systemctl --user status  comitia-api.service comitia-mcp-http.service
systemctl --user restart comitia-api.service comitia-mcp-http.service
journalctl --user -u comitia-mcp-http.service -n 50 --no-pager
```

User units are used because writes to `/etc/systemd/system/` are blocked by the
agent's consent gate. Boot persistence comes from `loginctl enable-linger root`
(already on) plus the `default.target.wants` symlink — the same mechanism the
Hermes gateway uses.

nginx is a system unit: `systemctl reload nginx`, validate with `nginx -t`.

## Identities

| Principal | Scope |
| --- | --- |
| owner | admin; mints and revokes sessions |
| chatgpt | read,write,claim,grant in `handoff` |
| codex | read,write,claim,grant in `handoff` |
| claude | read,write,claim,grant in `handoff` |
| hermes | read,write,claim,grant in `handoff` |

Sessions expire ~90 days out (2026-12-18/19); the owner session expires
2027-09-19. There is no account recovery if every admin credential is lost —
mint a replacement before that.

## Common operations

```sh
export COMITIA_DB=/var/lib/comitia/comitia.db
export COMITIA_CREDENTIAL=$(python3 -c "import json;print(json.load(open('/root/.comitia/owner.json'))['credential']['Token'])")

# add a harness: mints an identity and prints its connection block
bash scripts/comitia-add-harness.sh <name> [perms] [hours]

# revoke one session
/opt/comitia/bin/comitia revoke --session-id <SESSION_ID>

# inspect
/opt/comitia/bin/comitia participants --context-id <CTX>
/opt/comitia/bin/comitia task-list    --context-id <CTX>
```

## Rotating credentials

Credentials are read from the session file at launch, so rotation needs no
harness config edit:

1. `comitia revoke --session-id <old>` (owner credential required)
2. `bash scripts/comitia-add-harness.sh <harness>` to mint a fresh one
3. Restart the harness — it picks up the new token on next launch

Revocation is per-identity: verified that revoking one leaves the others working.

## Rebuilding after a code change

```sh
export PATH=/usr/lib/go-1.24/bin:$PATH
cd /root/workspace/comitia
go test ./... && go build -o bin/comitia ./cmd/comitia
install -m 0755 bin/comitia /opt/comitia/bin/comitia
XDG_RUNTIME_DIR=/run/user/0 systemctl --user restart comitia-api.service comitia-mcp-http.service
```

## Verification

Run all of these after any change. Every one is expected to pass.

```sh
export PATH=/usr/lib/go-1.24/bin:$PATH
go test ./...                                          # unit + transport tests

python3 scripts/verify-http.py 127.0.0.1 8390          # REST API + auth       (7)
python3 scripts/verify-mcp.py                          # stdio MCP adapter     (5)
python3 scripts/verify-mcp-http.py                     # HTTP MCP gateway     (19)
python3 scripts/verify-sdk-client.py                   # official MCP SDK
python3 scripts/verify-public-mcp.py                   # public TLS edge      (15)
python3 scripts/verify-e2e-chatgpt.py                  # chatgpt<->codex e2e  (11)
python3 scripts/verify-harness-configs.py              # Claude + Codex config (2)
python3 scripts/verify-deploy.py                       # service+persistence   (6)
python3 scripts/verify-posture.py                      # nothing public        (8)
bash    scripts/verify-chatgpt-identity.sh             # identity capabilities (10)
bash    scripts/verify-revocation-isolation.sh         # isolation             (7)
bash    scripts/verify-ssh-tunnel.sh                   # SSH tunnel path
```

`verify-public-mcp.py` deliberately exhausts the edge rate limit, so leave a gap
before running `verify-e2e-chatgpt.py` (or run e2e first). The SDK check needs
`/tmp/mcpsdkvenv`; recreate with `python3 -m venv /tmp/mcpsdkvenv &&
/tmp/mcpsdkvenv/bin/pip install mcp`.

`verify-ssh-tunnel.sh` temporarily adds a key to `authorized_keys` and restores
it byte-for-byte; run it deliberately.

## HTTPS edge details

- Public route: `POST https://vmi3260467.contaboserver.net:8443/mcp` **only**.
- Everything else returns 404 — `/ui/`, `/v1/*`, `/admin`, `/debug`, `/metrics`.
  `GET /mcp` is refused (this adapter is stateless; it has no SSE stream).
- Certificate: Let's Encrypt, auto-renewing, with a deploy hook that reloads nginx.
- Origin allowlist is enforced in the adapter (`--allowed-origins`). It is
  currently empty, which rejects any request carrying an `Origin` (i.e. browser
  callers) while allowing non-browser clients. Set it if a browser-based MCP
  client is ever added.
- Rate limits: nginx `5r/s` burst 20 per IP, plus a per-credential token bucket
  inside the adapter (20/s, burst 60). Responses are `429` + `Retry-After`.

## Auth model (important)

The public endpoint accepts a **static bearer credential**. That covers the
Responses API, Agents SDK, Cursor, the Claude app, and any MCP client that lets
you supply a header token.

It does **not** satisfy ChatGPT web's connector requirement: per OpenAI's Apps SDK
auth docs, ChatGPT expects OAuth 2.1 with dynamic client registration, PKCE S256
and protected-resource metadata, and "does not support ... custom API keys".
ChatGPT web also cannot be pointed at a non-standard port. Making ChatGPT web a
direct client would need an OAuth 2.1 authorization server (option C in
`chatgpt-integration.md`) plus ingress on 443.

## Notes and gotchas

- **Envelope schemas are strictly validated** and unknown fields are rejected.
  `review-request` needs `artifact_refs` **and** `checks`; `assignment` needs
  `instructions` and `acceptance_criteria`; `assignment`, `review-request` and
  `blocker` require `task_id`, while `work-submitted` and `verification-result`
  may name `artifact_refs` instead. There is no `review-response` schema — use
  `work-submitted`, or `approval` for a reviewer's verdict.
- **CLI `send` uses `--envelope-file`**, not `--envelope`.
- **CLI `task-claim` uses `--lease 10m`** (a Go duration), not seconds. Add
  `--agent-instance ID` to record which process holds the claim, and
  `--open-ended` for a lease that lasts until `task-release`/`task-complete`
  (capped at the session's expiry).
- **Claim response wraps the task:** `{"Task": {...}, "Token": "..."}`.
- **HTTP ack field is `after`**; the CLI/MCP name is `sequence`. `message_cursor`
  returns `{"sequence": N}`, not a bare number.
- **`comitia serve` refuses `--server-url`;** `comitia mcp-http` requires it and
  refuses a process-level `COMITIA_CREDENTIAL`.
- **MCP is polling-based.** No SSE or long polling. Delivery is at-least-once —
  use `dedupe_key` and make side effects idempotent.
- **Bootstrap runs once per database** and is unavailable over HTTP by design.
