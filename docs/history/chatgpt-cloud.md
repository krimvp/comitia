# Using Comitia from ChatGPT (cloud) (deployment record)

> Historical record, archived 2026-10-05. Recorded deployment/plan by: 2026-10-04.
> Commands, paths, client restrictions and conclusions below describe that record,
> not current product guarantees. Start with [Connecting](../CONNECTING.md)
> and [local service operations](../local-service.md) for current guidance.

Historical VPS connector notes, archived 2026-10-04, not a generic connection
recipe. The named identity, paths and external client restrictions below have
not been revalidated. Start with [Connecting](../CONNECTING.md) for current local,
demo and configurable remote setup; verify your cloud client's current
requirements separately.

This is the one harness that cannot use the SSH tunnel, because ChatGPT's tools
execute on OpenAI's infrastructure rather than on your machine. It needs a
public HTTPS endpoint.

## The two hard requirements

**1. The MCP endpoint must be on port 443.**

ChatGPT's connector broker only dispatches to the standard HTTPS port. A server
on a non-standard port receives **no traffic at all** — the request is simply
never made. It is not a TLS, DNS, certificate or firewall fault, and it cannot
be reproduced with curl from anywhere else, which is what makes it expensive to
diagnose. The failure surfaces as a generic "couldn't reach the server" with
zero requests in your nginx log.

This is documented behaviour, not a local misconfiguration. Reverse-proxy the
MCP path onto 443.

**2. Authentication is OAuth, "No authentication", or Mixed — but never a
custom header.**

The connector UI has no field for an `Authorization` header. A token-based
server therefore has exactly one usable option: put the credential in the URL,
`https://<host>/mcp/<credential>`, and select **No authentication**.

A URL credential is a genuine secret and leaks more easily than a header — into
browser history, proxy logs, screenshots and chat transcripts. Treat the full
URL as the credential: don't paste it into shared documents, and use a
dedicated identity that you rotate, so revocation is cheap.

## Setup

Prerequisites: a ChatGPT plan that includes developer mode (Plus, Pro, Business,
Enterprise or Education — not Free), and on Business/Enterprise a workspace
admin must allow it. It is a web-only feature: the desktop app and Deep Research
mode do not support MCP connectors.

1. Get a credential for ChatGPT. Comitia already has an identity named
   `chatgpt`; use it rather than reusing `codex` or `claude`, so a leaked URL can
   be revoked without disturbing the other harnesses.

   ```sh
   bash scripts/comitia-add-harness.sh chatgpt      # mint, if starting fresh
   python3 -c "import json;print(json.load(open('/root/.comitia/sessions/chatgpt.json'))['credential']['Token'])"
   ```

2. In ChatGPT (web): **Settings → Apps & Connectors → Advanced settings**, and
   switch **Developer mode** on. If the toggle is not under Connectors, look
   under Settings → Security and login; OpenAI has moved it. If it is missing
   entirely, the plan or a workspace policy is the reason.

3. **Settings → Apps & Connectors → Create** (a custom connector).

4. Fill in:
   - **Name**: `Comitia`
   - **Description**: something action-oriented, because the model reads it when
     choosing tools. For example: *"Coordinate with other coding agents: list
     participants, send and read messages, and create, claim and complete
     tasks."*
   - **MCP Server URL**: `https://comitia.example.com/mcp/<credential>`
     — standard port 443, **no `:8443`**, no trailing slash.
   - **Authentication**: **No authentication**. The credential is in the URL.
     Do not choose OAuth — ChatGPT will try a discovery flow that this endpoint
     does not serve, and fail.

5. Save. ChatGPT should list **11 tools**:
   `send_message`, `list_participants`, `replay_messages`, `ack_messages`,
   `message_cursor`, `create_task`, `claim_task`, `renew_task`, `complete_task`,
   `get_task`, `list_tasks`.

6. In a conversation, open the **+** menu, choose **Developer Mode**, and tick
   Comitia. Developer-mode apps are selected **per conversation** — adding the
   connector does not make it available everywhere, and it is not active until
   you tick it in that chat.

7. Ask it something concrete to confirm: *"List the Comitia participants."*

## Verifying from outside

The endpoint is credential-gated, so a liveness probe needs a real call. Note
that `tools/list` is deliberately **credential-free** — it is tool metadata and
needs no identity — so a bad credential there proves nothing. Only a tool call
authenticates:

```sh
curl -sS "https://comitia.example.com/mcp/$CRED" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call",
       "params":{"name":"list_participants",
                 "arguments":{"context_id":"'"$CTX"'"}}}'
```

A good credential returns participant records. A bad one returns HTTP **200**
with `"isError": true` — not a 4xx. Treating that 200 as success is a common
integrator mistake.

## What ChatGPT can do once connected

Everything any other harness can: discover peers, send and read messages, and
run the full task lifecycle. Two behaviours it must follow, which are worth
putting in its instructions:

- **Reading is poll plus ack.** There is no push. Call `replay_messages` with
  `after=<cursor>`, handle what comes back, then `ack_messages(sequence)`. Its
  cursor is stored server-side, so a new conversation does not lose its place.
- **Delivery is at-least-once.** Comitia will not duplicate the log, but it
  cannot undo a retry on the other side. Use a `dedupe_key` and make side
  effects idempotent.

Tasks are **leased, not locked**: `complete_task` needs both the `Token` and the
`lease_version` from the claim, and a long task must call `renew_task` before the
lease expires or another harness can legitimately take it.

## Troubleshooting

- **"Couldn't reach the MCP server", and the nginx log shows nothing.** The port
  is the overwhelmingly likely cause. It must be 443. Check for a `:8443` in the
  URL.
- **Connection fails at TLS.** Make sure the URL has no port, so it defaults to
  443, and that the hostname matches the certificate.
- **Connected, but ChatGPT never calls the tools.** The connector is not enabled
  for that conversation — tick it in the **+** menu. This is per-chat.
- **Tools list is empty.** The endpoint returned an HTML error page instead of
  JSON, so the route is not reaching the gateway. Check `location /mcp` is
  matching and `:8391` is up.
- **`429`.** Rate limiting, not an auth failure. Respect `Retry-After`.
