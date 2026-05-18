#!/usr/bin/env bash
# inject-secrets.sh — injecte les secrets Veridian (Hub, Brevo, etc.) depuis
# ~/credentials/.all-creds.env dans le .env local du dev.
#
# Mapping (.all-creds.env → .env) :
#   NOTIFUSE_HUB_API_SECRET     → HUB_API_SECRET
#   NOTIFUSE_HUB_WEBHOOK_SECRET → HUB_WEBHOOK_SECRET (pour HUB_WEBHOOK_URL côté Hub)

set -euo pipefail

ENV_FILE="${ENV_FILE:-.env}"
CREDS_FILE="${CREDS_FILE:-$HOME/credentials/.all-creds.env}"

if [ ! -f "$ENV_FILE" ]; then
  echo "❌ $ENV_FILE not found"
  exit 1
fi

if [ ! -f "$CREDS_FILE" ]; then
  echo "⚠ $CREDS_FILE not found — skipping Veridian secrets injection."
  echo "  You can set HUB_API_SECRET manually in $ENV_FILE."
  exit 0
fi

# Extraire les valeurs depuis .all-creds.env
HUB_API=$(grep -E "^NOTIFUSE_HUB_API_SECRET=" "$CREDS_FILE" | head -1 | cut -d= -f2- || true)
HUB_WEBHOOK=$(grep -E "^NOTIFUSE_HUB_WEBHOOK_SECRET=" "$CREDS_FILE" | head -1 | cut -d= -f2- || true)

inject_var() {
  local key="$1" value="$2"
  [ -z "$value" ] && return 0
  if grep -qE "^${key}=" "$ENV_FILE"; then
    sed -i "s|^${key}=.*|${key}=${value}|" "$ENV_FILE"
  else
    echo "${key}=${value}" >> "$ENV_FILE"
  fi
  echo "  ✓ ${key}"
}

echo "→ Injecting Veridian secrets..."
inject_var "HUB_API_SECRET" "$HUB_API"
inject_var "HUB_WEBHOOK_SECRET" "$HUB_WEBHOOK"

echo "✓ Secrets injected in $ENV_FILE"
