#!/usr/bin/env bash
# comitia-tunnel — keep an SSH tunnel to the Comitia daemon alive,
# then run a command with COMITIA_SERVER_URL pointed at it.
#
#   comitia-tunnel                 # just hold the tunnel open (foreground)
#   comitia-tunnel -- codex        # hold tunnel, run codex in this shell
#
# Override with env vars:
#   COMITIA_SSH_HOST   (required, e.g. root@203.0.113.10)
#   COMITIA_LOCAL_PORT (default 8390)
#   COMITIA_REMOTE_PORT(default 8390)
set -euo pipefail

SSH_HOST="${COMITIA_SSH_HOST:?set COMITIA_SSH_HOST, e.g. root@203.0.113.10}"
LOCAL_PORT="${COMITIA_LOCAL_PORT:-8390}"
REMOTE_PORT="${COMITIA_REMOTE_PORT:-8390}"

cleanup() { [ -n "${TUNNEL_PID:-}" ] && kill "$TUNNEL_PID" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

echo "opening tunnel 127.0.0.1:${LOCAL_PORT} -> ${SSH_HOST}:${REMOTE_PORT}" >&2
ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
    -L "${LOCAL_PORT}:127.0.0.1:${REMOTE_PORT}" "$SSH_HOST" &
TUNNEL_PID=$!

# Wait for the local listener to come up.
for _ in $(seq 1 20); do
  if (exec 3<>/dev/tcp/127.0.0.1/"$LOCAL_PORT") 2>/dev/null; then break; fi
  sleep 0.5
done
echo "tunnel ready (pid $TUNNEL_PID)" >&2

export COMITIA_SERVER_URL="http://127.0.0.1:${LOCAL_PORT}"
unset COMITIA_DB COMITIA_BOOTSTRAP_CREDENTIAL

if [ "$#" -gt 0 ]; then
  [ "$1" = "--" ] && shift
  exec "$@"
fi
wait "$TUNNEL_PID"
