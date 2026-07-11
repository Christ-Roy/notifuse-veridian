#!/usr/bin/env bash
# nomad-deploy.sh — déploie un job Nomad Veridian (staging|prod) depuis deploy/*.nomad.hcl.
#
# Remplace l'ancien GitOps Dokploy (bump infra/compose/*.yml + compose.redeploy).
# Nouveau modèle (cf ~/nomad-veridian/GITOPS-NOMAD.md §1) : le job vit dans CE repo,
# la CI bump le tag image dans le HCL puis `nomad job run`. `nomad job plan` avant tout run.
#
# Usage :
#   scripts/ci/nomad-deploy.sh <staging|prod> <image_tag> [--no-wait]
#     <image_tag>  ex: v54.0-veridian.7ff43498  (le tag GHCR fraîchement buildé)
#     --no-wait    ne poll pas /api/version après le run (déploiement fire-and-forget)
#
# Auth Nomad (dans l'ordre) :
#   1. $NOMAD_ADDR + $NOMAD_TOKEN de l'environnement (mode CI, secrets GitHub)
#   2. sinon ~/credentials/nomad-bastion.env (mode local/bastion)
#
# Le binaire `nomad` doit être dans le PATH (présent sur le bastion + les nœuds dev-pub/prod-pub).
# Sortie : logs lisibles, exit != 0 si validate/run échoue ou si /api/version ne matche pas le tag.
set -euo pipefail

# ---------------------------------------------------------------- args
ENV_TARGET="${1:-}"
IMAGE_TAG="${2:-}"
WAIT=1
[[ "${3:-}" == "--no-wait" ]] && WAIT=0

die() { echo "✗ $*" >&2; exit 1; }

[[ -n "$ENV_TARGET" && -n "$IMAGE_TAG" ]] || die "usage: nomad-deploy.sh <staging|prod> <image_tag> [--no-wait]"

case "$ENV_TARGET" in
  prod)    JOB="notifuse";          HCL="deploy/notifuse.nomad.hcl";         HEALTH_URL="https://notifuse.app.veridian.site/api/version" ;;
  staging) JOB="notifuse-staging";  HCL="deploy/notifuse-staging.nomad.hcl"; HEALTH_URL="https://notifuse.staging.veridian.site/api/version" ;;
  *) die "env invalide '$ENV_TARGET' (attendu: staging|prod)" ;;
esac

# Résout le chemin du HCL depuis la racine du repo (le script peut être appelé de n'importe où).
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HCL_PATH="${REPO_ROOT}/${HCL}"
[[ -f "$HCL_PATH" ]] || die "job HCL introuvable : $HCL_PATH"

IMAGE_REPO="ghcr.io/christ-roy/notifuse-veridian"
FULL_IMAGE="${IMAGE_REPO}:${IMAGE_TAG}"

# ---------------------------------------------------------------- auth Nomad
if [[ -z "${NOMAD_ADDR:-}" || -z "${NOMAD_TOKEN:-}" ]]; then
  CREDS="${HOME}/credentials/nomad-bastion.env"
  if [[ -f "$CREDS" ]]; then
    # shellcheck disable=SC1090
    set -a; . "$CREDS"; set +a
    # nomad-bastion.env expose NOMAD_MGMT_TOKEN — le mapper sur NOMAD_TOKEN si besoin.
    export NOMAD_TOKEN="${NOMAD_TOKEN:-${NOMAD_MGMT_TOKEN:-}}"
  fi
fi
[[ -n "${NOMAD_ADDR:-}" ]]  || die "NOMAD_ADDR absent (env CI ou ~/credentials/nomad-bastion.env)"
[[ -n "${NOMAD_TOKEN:-}" ]] || die "NOMAD_TOKEN absent (env CI ou ~/credentials/nomad-bastion.env)"
command -v nomad >/dev/null || die "binaire 'nomad' absent du PATH"

echo "▶ deploy $JOB ($ENV_TARGET) → $FULL_IMAGE"
echo "  NOMAD_ADDR=$NOMAD_ADDR  HCL=$HCL"

# ---------------------------------------------------------------- bump tag image
# Remplace la ligne `image = "ghcr.io/christ-roy/notifuse-veridian:<...>"` par le nouveau tag.
# Ne touche PAS l'image postgres (préfixe différent).
CUR="$(grep -oE "${IMAGE_REPO}:[^\"]+" "$HCL_PATH" | head -1 || true)"
if [[ "$CUR" == "$FULL_IMAGE" ]]; then
  echo "  tag déjà à jour ($IMAGE_TAG) — re-run idempotent"
else
  echo "  bump image : ${CUR:-<absent>} → $FULL_IMAGE"
  # séparateur '|' pour éviter d'échapper les '/'
  sed -i "s|${IMAGE_REPO}:[^\"]*|${FULL_IMAGE}|g" "$HCL_PATH"
  grep -q "$FULL_IMAGE" "$HCL_PATH" || die "bump du tag a échoué (image non trouvée après sed)"
fi

# ---------------------------------------------------------------- validate + plan + run
echo "▶ nomad job validate"
nomad job validate "$HCL_PATH" || die "validate a échoué"

echo "▶ nomad job plan (dry-run diff)"
# plan renvoie 1 quand il y a des changements à appliquer (comportement normal, PAS une erreur),
# 255 sur vraie erreur. On tolère 0 et 1.
set +e
nomad job plan "$HCL_PATH"
PLAN_RC=$?
set -e
[[ "$PLAN_RC" == "0" || "$PLAN_RC" == "1" ]] || die "plan a échoué (rc=$PLAN_RC)"

echo "▶ nomad job run"
nomad job run -detach "$HCL_PATH" || die "run a échoué"

# ---------------------------------------------------------------- wait deployment
if [[ "$WAIT" == "0" ]]; then
  echo "✓ run soumis (--no-wait, pas de vérification /api/version)"
  exit 0
fi

echo "▶ attente déploiement sain (poll ${HEALTH_URL})"
DEADLINE=$(( $(date +%s) + 300 ))   # 5 min
SHA_EXPECTED="${IMAGE_TAG##*.}"     # le sha8 après le dernier '.'
while :; do
  BODY="$(curl -fsS --max-time 8 "$HEALTH_URL" 2>/dev/null || true)"
  if echo "$BODY" | grep -q "$IMAGE_TAG" || echo "$BODY" | grep -q "$SHA_EXPECTED"; then
    echo "✓ $ENV_TARGET sert $IMAGE_TAG — deploy OK"
    echo "  $BODY"
    exit 0
  fi
  if (( $(date +%s) >= DEADLINE )); then
    echo "✗ timeout 5min — /api/version ne renvoie pas $IMAGE_TAG" >&2
    echo "  dernière réponse : ${BODY:-<vide>}" >&2
    echo "  → vérifier : nomad job status $JOB ; nomad alloc logs <alloc>" >&2
    exit 3
  fi
  sleep 10
done
