#!/usr/bin/env bash
# bump-compose-image.sh — GitOps image bump pour infra/compose/{staging,prod}.yml
#
# Remplace dans le compose la ligne `image: ghcr.io/christ-roy/notifuse-veridian:...`
# par une référence immutable `:<tag>@sha256:<digest>`. Le compose passe ainsi
# de paramétré (${VAR}) à pinné en dur, traçable via git log.
#
# Usage :
#   scripts/ci/bump-compose-image.sh <env> <tag> <digest>
#
# env    : staging | prod
# tag    : ex. v32.0-veridian.eb7a88e2  (sans préfixe registry)
# digest : ex. dfcc65665a24...  (sha256 sans préfixe "sha256:")
#
# Idempotent : si la ligne contient déjà `:<tag>@sha256:<digest>`, no-op.
# Exit codes : 0 = bump effectué ou no-op, 1 = erreur (fichier introuvable,
# pattern image: pas trouvé, args manquants).

set -euo pipefail

ENV_NAME="${1:?usage: bump-compose-image.sh <env> <tag> <digest>}"
TAG="${2:?usage: bump-compose-image.sh <env> <tag> <digest>}"
DIGEST="${3:?usage: bump-compose-image.sh <env> <tag> <digest>}"

case "$ENV_NAME" in
  staging|prod) ;;
  *) echo "❌ env must be 'staging' or 'prod', got '$ENV_NAME'" >&2; exit 1 ;;
esac

COMPOSE_FILE="infra/compose/${ENV_NAME}.yml"
if [ ! -f "$COMPOSE_FILE" ]; then
  echo "❌ compose file not found: $COMPOSE_FILE" >&2
  exit 1
fi

# Strip "sha256:" prefix si présent (build action sort des fois avec, des fois sans).
DIGEST="${DIGEST#sha256:}"

# Le pattern de ligne à remplacer matche les 3 formes possibles :
#   image: ghcr.io/christ-roy/notifuse-veridian:latest
#   image: ghcr.io/christ-roy/notifuse-veridian:${NOTIFUSE_IMAGE_TAG:-latest}
#   image: ghcr.io/christ-roy/notifuse-veridian:v31.0-...@sha256:abc...
NEW_REF="ghcr.io/christ-roy/notifuse-veridian:${TAG}@sha256:${DIGEST}"
NEW_LINE="    image: ${NEW_REF}"

# Idempotence : si la nouvelle ligne est déjà là, on sort vert.
if grep -qF "$NEW_REF" "$COMPOSE_FILE"; then
  echo "✓ $COMPOSE_FILE déjà pinné sur $NEW_REF — no-op"
  exit 0
fi

# Sanity : il doit exister une ligne `image: ghcr.io/christ-roy/notifuse-veridian:`
if ! grep -qE '^[[:space:]]+image:[[:space:]]+ghcr\.io/christ-roy/notifuse-veridian:' "$COMPOSE_FILE"; then
  echo "❌ aucune ligne 'image: ghcr.io/christ-roy/notifuse-veridian:' trouvée dans $COMPOSE_FILE" >&2
  exit 1
fi

# In-place sed. On capture l'ancien pour log/debug.
OLD_LINE=$(grep -E '^[[:space:]]+image:[[:space:]]+ghcr\.io/christ-roy/notifuse-veridian:' "$COMPOSE_FILE" | head -1)
sed -i -E "s|^([[:space:]]+)image:[[:space:]]+ghcr\.io/christ-roy/notifuse-veridian:.*|\\1image: ${NEW_REF}|" "$COMPOSE_FILE"

# Vérif post-sed
if ! grep -qF "$NEW_REF" "$COMPOSE_FILE"; then
  echo "❌ sed a échoué — $NEW_REF absent de $COMPOSE_FILE après bump" >&2
  exit 1
fi

echo "✓ Bumped $COMPOSE_FILE"
echo "  - $OLD_LINE"
echo "  + ${NEW_LINE}"
