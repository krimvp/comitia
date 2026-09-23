#!/usr/bin/env bash
# Install the Comitia MCP public HTTPS edge.
#
#   deploy-comitia-mcp-edge.sh
#
# Order matters: the ACME-only port-80 block must load before the TLS block,
# because the TLS block references a certificate that does not exist until
# certbot has run. Idempotent. Requires root.
set -euo pipefail

DOMAIN="vmi3260467.contaboserver.net"
SRC_DIR=/root/workspace/comitia/deploy
ACME_SITE=/etc/nginx/sites-available/comitia-mcp-acme
TLS_SITE=/etc/nginx/sites-available/comitia-mcp

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }

echo "== 1. install the ACME challenge block (and remove any TLS block) =="
rm -f /etc/nginx/sites-enabled/comitia-mcp
install -m 0644 "$SRC_DIR/comitia-mcp-acme.nginx.conf" "$ACME_SITE"
ln -sfn "$ACME_SITE" /etc/nginx/sites-enabled/comitia-mcp-acme
mkdir -p /var/www/html
nginx -t 2>&1 | sed 's/^/   /'
systemctl reload nginx
echo "   nginx reloaded with challenge block"

echo "== 2. obtain the certificate =="
if [ -d "/etc/letsencrypt/live/$DOMAIN" ]; then
  echo "   certificate already present"
else
  certbot certonly --webroot -w /var/www/html -d "$DOMAIN" \
    --non-interactive --agree-tos --register-unsafely-without-email \
    --keep-until-expiring 2>&1 | sed 's/^/   /'
fi
[ -f "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" ] || {
  echo "   ERROR: no certificate issued; leaving the TLS block disabled" >&2; exit 1; }

echo "== 3. install the TLS block and drop the standalone ACME block =="
install -m 0644 "$SRC_DIR/comitia-mcp.nginx.conf" "$TLS_SITE"
ln -sfn "$TLS_SITE" /etc/nginx/sites-enabled/comitia-mcp
# The TLS block carries its own ACME location, so the phase-1 block is retired.
rm -f /etc/nginx/sites-enabled/comitia-mcp-acme
nginx -t 2>&1 | sed 's/^/   /'
systemctl reload nginx
echo "   nginx reloaded with TLS edge"

echo "== 4. ensure renewal reloads nginx =="
if [ -d /etc/letsencrypt/renewal ]; then
  install -d -m 0755 /etc/letsencrypt/renewal-hooks/deploy
  cat > /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh <<'HOOK'
#!/bin/sh
systemctl reload nginx
HOOK
  chmod 0755 /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh
  echo "   deploy hook installed"
fi

echo "== 5. open 443 (the port ChatGPT dials) =="
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
  ufw allow 443/tcp 2>&1 | sed 's/^/   /'
else
  echo "   ufw inactive; ensure 443/tcp is open in the provider firewall"
fi

echo
echo "done. Verify with:"
echo "  python3 /root/workspace/comitia/scripts/verify-public-mcp.py"
