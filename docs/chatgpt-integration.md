# ChatGPT integration — findings and plan

Historical integration investigation, archived 2026-10-04. Deployment observations and
external client restrictions below are a dated record, not current setup
requirements. The current adapter implements stateless HTTP MCP; see
[Connecting](CONNECTING.md#remote-http-and-ssh-forwarding). OAuth work discussed
below remains a separate proposal.

Status: **Phase 1 complete and verified. Phase 2 blocked pending a decision.**

## Phase 1 — ChatGPT identity: DONE

`~/.comitia/sessions/chatgpt.json` exists as a first-class Comitia identity,
separate from claude/codex/hermes, verified 10/10 by
`scripts/verify-chatgpt-identity.sh`:

| Capability | Result |
| --- | --- |
| discover peers | 5 participants (owner, claude, codex, hermes, chatgpt) |
| send message | ok |
| replay messages | ok |
| ack messages | ok |
| cursor read | ok |
| create task | ok |
| claim task | ok (lease v1) |
| inspect task state | ok |
| complete task | ok |
| addressed envelope to another session | ok |

Revocation isolation verified 7/7 by `scripts/verify-revocation-isolation.sh`:
revoking one identity denies that identity and leaves claude, codex, chatgpt and
hermes working.

## Phase 2 — the blocker

The plan assumes a `/mcp` HTTPS endpoint exists that a tunnel or proxy can front:

> expose only `/mcp` through Cloudflare Tunnel or nginx

**It does not exist, and cannot be proxied.** Verified against the code:

- `mcp/server.go` exposes exactly one entrypoint:
  `func (s *Server) Serve(ctx, in io.Reader, out io.Writer) error` — **stdio only**.
  There is no `net/http`, no SSE, no `text/event-stream` anywhere in the package.
- `cmd/comitia/main.go` wires it as `Serve(ctx, os.Stdin, os.Stdout)`.
- `httpapi/httpapi.go` serves only `/ui/` and paths under `/v1/`; anything else
  404s. `grep -rn '"/mcp"' --include=*.go` returns nothing.
- Live probe: `POST /mcp` with a valid token → **404**. The 401 seen without a
  token is only because auth is evaluated before the path check.

So the "MCP Gateway / reverse tunnel" box is not optional scaffolding — it must
implement the MCP **streamable HTTP transport** itself. Nothing can be pointed at
`/mcp` today, because no listener answers there.

## Phase 2b — ChatGPT's auth model (second blocker)

Per OpenAI's Apps SDK authentication docs:

- ChatGPT expects **OAuth 2.1** conforming to the MCP authorization spec, with
  dynamic client registration, **PKCE S256**, a `401` +
  `WWW-Authenticate: Bearer resource_metadata=...` challenge, and
  `/.well-known/oauth-protected-resource` metadata.
- ChatGPT **"does not support machine-to-machine OAuth grants such as client
  credentials, service accounts, or JWT bearer assertions, nor can it present
  custom API keys or mTLS certificates."**

That contradicts the plan's `Authorization: Bearer <chatgpt-token>` design for
the ChatGPT **web app**. Third-party write-ups conflict on whether the connector
UI now offers a static "Token" option; OpenAI's own docs describe OAuth.

Bearer tokens **are** accepted by the **Responses API / Agents SDK** MCP tool
(`authorization` field), which is a different client than ChatGPT web.

## Route surface if exposed naively

`scripts/audit-surface.py` against the live daemon:

- `/ui/` and its assets (`app.js`, `style.css`, `graph.mjs`, `model.mjs`,
  `sequence.mjs`) are served with **no authentication**. Conversation data still
  requires a token, but the shell is public.
- Everything else requires a bearer token; non-`/v1/` paths 404 even with one.
- Any non-POST method returns 405 before anything else.

This confirms the plan's instinct to allowlist `/mcp` only. A gateway must be the
sole public route; `/ui/` must stay private.

## Options

**A. ChatGPT via Codex CLI (works today, zero new code).**
Anton's ChatGPT subscription already includes Codex, which is a verified Comitia
participant over SSH. `codex` appears in `participants` and completes tasks.
This is ChatGPT's *agent* participating in Comitia, not ChatGPT web.

**B. Bearer-token MCP gateway (no OAuth).**
Build `comitia mcp-http` — a small Go adapter that serves the existing
`mcp.Server` over streamable HTTP per request (stateless: credential from the
`Authorization` header, one JSON-RPC message per POST, Comitia keeps cursors
server-side). Front it with Cloudflare Tunnel or nginx + TLS + allowlist + rate
limit. Works for the Responses API, Agents SDK, Cursor, Claude app, and ChatGPT
*if* its UI accepts a static token. Does **not** satisfy ChatGPT web's OAuth
requirement. ~150 lines of Go plus proxy config.

**C. B (transport) + OAuth 2.1 authorization server.**
The only route that makes **ChatGPT web** a direct Comitia client. Requires
implementing authorization server metadata, `/authorize` (consent screen),
`/token`, dynamic client registration, PKCE S256 verification, protected-resource
metadata, and audience/scope validation — then mapping issued tokens onto Comitia
sessions. Substantially more work, and the token lifetime vs. Comitia's
90-day session/grant model needs designing.

## Decisions needed

1. **Auth mode:** accept bearer-only (option B, covers API clients), or invest in
   full OAuth 2.1 (option C, required for ChatGPT web)?
2. **Hostname:** there is no `comitia.example.com`. The machine's public DNS name
   is `vmi3260467.contaboserver.net` (an A record to 45.88.191.59, verified via
   public DoH), so a Let's Encrypt cert is obtainable without owning a domain —
   but an OpenAI app review allowlists a specific redirect URI, so this needs a
   deliberate choice.
3. **Tunnel vs. proxy:** Cloudflare Tunnel (no public listener, needs a CF account
   token) or nginx + Let's Encrypt on a public port (already installed, adds a
   public service)?

## What must not change

- Comitia stays bound to `127.0.0.1:8390`. Verified: closed from the internet on
  IPv4 and IPv6.
- SSH path stays for Codex/Claude/other harnesses.
- `/ui/` stays private; only the MCP route becomes public.
- ChatGPT keeps its own identity; never reuse `codex.json` or `claude.json`.
