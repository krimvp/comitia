#!/usr/bin/env bash
# Install the Comitia MCP public HTTPS edge.
#
#   COMITIA_PUBLIC_HOST=comitia.example.com deploy-comitia-mcp-edge.sh
#
# The deploy/*.nginx.conf files name the placeholder host comitia.example.com;
# it is replaced with COMITIA_PUBLIC_HOST as they are installed. COMITIA_SRC
# (default: the checkout holding this script) locates deploy/ and scripts/.
#
# Order matters: the ACME-only port-80 block must load before the TLS block,
# because the TLS block references a certificate that does not exist until
# certbot has run. Idempotent. Requires root.
set -euo pipefail

DOMAIN="${COMITIA_PUBLIC_HOST:?set COMITIA_PUBLIC_HOST to the edge hostname}"
[[ "$DOMAIN" =~ ^[A-Za-z0-9][A-Za-z0-9.-]*$ ]] || { echo "invalid hostname '$DOMAIN'" >&2; exit 2; }
SRC="${COMITIA_SRC:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
SRC_DIR="$SRC/deploy"
ACME_SITE=/etc/nginx/sites-available/comitia-mcp-acme
TLS_SITE=/etc/nginx/sites-available/comitia-mcp

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }

# install_site <source> <target>: copy with the placeholder host replaced.
install_site() {
  sed "s/comitia\.example\.com/$DOMAIN/g" "$1" > "$2"
  chmod 0644 "$2"
}

echo "== 1. install the ACME challenge block (and remove any TLS block) =="
rm -f /etc/nginx/sites-enabled/comitia-mcp
install_site "$SRC_DIR/comitia-mcp-acme.nginx.conf" "$ACME_SITE"
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
install_site "$SRC_DIR/comitia-mcp.nginx.conf" "$TLS_SITE"
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
echo "  COMITIA_PUBLIC_HOST=$DOMAIN python3 $SRC/scripts/verify-public-mcp.py"
