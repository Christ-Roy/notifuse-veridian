#!/usr/bin/env bash
# validate-compose.sh — vérifie que les composes prod et staging sont valides.
#
# Pattern : 1 fichier par env, autonome.
#   infra/compose/prod.yml      : compose prod complet (services notifuse-prod*)
#   infra/compose/staging.yml   : compose staging complet (services notifuse-staging*)
#
# Pourquoi pas un base + overrides : Docker Compose merge est imprévisible
# entre versions (v2.24 vs v2.38 vs v5.0 produisent des outputs différents).
# Avec 2 fichiers autonomes : pas de génération, pas de drift, pas de
# dépendance à la version Compose.
#
# Le script CI compose-validate exécute juste `docker compose config --quiet`
# pour vérifier la syntaxe sans rien matérialiser.
#
# Usage :
#   scripts/ci/generate-compose.sh           # valide les deux
#   scripts/ci/generate-compose.sh staging   # un seul env
#   scripts/ci/generate-compose.sh prod
set -euo pipefail

APP_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$APP_ROOT"

COMPOSE_DIR="infra/compose"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
NC=$'\033[0m'

TARGETS=("$@")
[ "${#TARGETS[@]}" -eq 0 ] && TARGETS=("staging" "prod")

# Dummy env values pour passer `config` sans warnings d'env vars manquants.
dummy_env() {
  export POSTGRES_PASSWORD=dummy
  export NOTIFUSE_SECRET_KEY=dummy
  export NOTIFUSE_ROOT_EMAIL=dummy
  export SMTP_HOST=dummy SMTP_PORT=587 SMTP_USER=dummy SMTP_PASS=dummy
  export SMTP_ADMIN_EMAIL=dummy SMTP_SENDER_NAME=dummy
  export NOTIFUSE_HUB_API_SECRET=dummy
  export NOTIFUSE_HUB_WEBHOOK_URL=dummy NOTIFUSE_HUB_WEBHOOK_SECRET=dummy
  export NOTIFUSE_IMAGE_TAG=latest
  export NOTIFUSE_DB_IMAGE_DIGEST=0000000000000000000000000000000000000000000000000000000000000000
  export NOTIFUSE_IMAGE_DIGEST=0000000000000000000000000000000000000000000000000000000000000000
}

validate_env() {
  local env="$1"
  local file="${COMPOSE_DIR}/${env}.yml"

  if [ ! -f "$file" ]; then
    echo "${RED}✗ ${file} absent${NC}" >&2
    return 1
  fi

  if (dummy_env; docker compose -f "$file" --project-name "notifuse-${env}" config --quiet 2>&1); then
    echo "${GREEN}✓ ${env} : compose valide${NC}"
  else
    echo "${RED}✗ ${env} : compose invalide${NC}" >&2
    return 1
  fi
}

FAILED=0
for env in "${TARGETS[@]}"; do
  validate_env "$env" || FAILED=$((FAILED + 1))
done

if [ "$FAILED" -gt 0 ]; then
  echo "${RED}${FAILED} validation(s) en échec${NC}" >&2
  exit 1
fi
