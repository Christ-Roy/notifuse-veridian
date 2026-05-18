#!/usr/bin/env bash
# inject-paseto-keys.sh — génère un SECRET_KEY random et l'inject dans .env
#
# Notifuse a basculé de PASETO vers une seule SECRET_KEY (>= 32 bytes random,
# base64-encodée). Cf. config/config.go ligne ~640.

set -euo pipefail

ENV_FILE="${ENV_FILE:-.env}"

if [ ! -f "$ENV_FILE" ]; then
  echo "❌ $ENV_FILE not found. Run 'make dev-bootstrap' first."
  exit 1
fi

# Si SECRET_KEY déjà set (et non vide), skip.
if grep -qE "^SECRET_KEY=.+$" "$ENV_FILE"; then
  echo "✓ SECRET_KEY already set in $ENV_FILE (skip)"
  exit 0
fi

# Génère 32 bytes random → base64 (pas de newline)
SECRET_KEY=$(openssl rand -base64 32 | tr -d '\n')

# Remplace ou ajoute SECRET_KEY=
if grep -q "^SECRET_KEY=" "$ENV_FILE"; then
  sed -i "s|^SECRET_KEY=.*|SECRET_KEY=${SECRET_KEY}|" "$ENV_FILE"
else
  echo "SECRET_KEY=${SECRET_KEY}" >> "$ENV_FILE"
fi

# Idem pour PASETO_PRIVATE_KEY/PASETO_PUBLIC_KEY (backward-compat)
if grep -qE "^PASETO_PRIVATE_KEY=$" "$ENV_FILE"; then
  sed -i "s|^PASETO_PRIVATE_KEY=$|PASETO_PRIVATE_KEY=${SECRET_KEY}|" "$ENV_FILE"
fi

echo "✓ SECRET_KEY generated and injected in $ENV_FILE"
