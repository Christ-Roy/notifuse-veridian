#!/usr/bin/env bash
# nomad-ssh-deploy.sh — déploie notifuse (staging|prod) via SSH-bastion.
#
# Canon Veridian (décision Robert 2026-07-11, cf veridian-prospection/deploy/README.md) :
# le NOMAD_TOKEN ne quitte JAMAIS le bastion. Ce script tourne sur le runner GitHub
# (ubuntu-latest), ouvre une session SSH vers le bastion Nomad avec une clé DÉDIÉE CI,
# y dépose le HCL DU REPO (qui déclare `variable image_tag`), pré-pull l'image ghcr
# AVEC auth sur le nœud cible (le plugin docker de Nomad ne pull pas les images privées),
# puis `nomad job run -var image_tag=<tag>` + `deployment status -monitor` (vérif ciblée).
#
# Usage : scripts/ci/nomad-ssh-deploy.sh <staging|prod> <image_tag>
#
# Secrets requis (env, fournis par le workflow depuis les secrets GH partagés cross-app) :
#   NOMAD_DEPLOY_SSH_KEY   clé privée ed25519 dédiée CI (publique dans authorized_keys bastion)
#   NOMAD_BASTION_HOST     IP/hostname public du bastion Contabo
#   NOMAD_BASTION_USER     user SSH sur le bastion (brunon5)
set -euo pipefail

ENV_TARGET="${1:-}"
IMAGE_TAG="${2:-}"
[[ -n "$ENV_TARGET" && -n "$IMAGE_TAG" ]] || { echo "✗ usage: nomad-ssh-deploy.sh <staging|prod> <image_tag>" >&2; exit 1; }
: "${NOMAD_DEPLOY_SSH_KEY:?secret NOMAD_DEPLOY_SSH_KEY manquant}"
: "${NOMAD_BASTION_HOST:?secret NOMAD_BASTION_HOST manquant}"
: "${NOMAD_BASTION_USER:?secret NOMAD_BASTION_USER manquant}"

IMAGE_REPO="ghcr.io/christ-roy/notifuse-veridian"
case "$ENV_TARGET" in
  staging) JOB="notifuse-staging"; HCL="deploy/notifuse-staging.nomad.hcl" ;;
  prod)    JOB="notifuse";         HCL="deploy/notifuse.nomad.hcl" ;;
  *) echo "✗ env invalide '$ENV_TARGET' (staging|prod)" >&2; exit 1 ;;
esac
[[ -f "$HCL" ]] || { echo "✗ HCL introuvable : $HCL" >&2; exit 1; }
REMOTE_HCL="/tmp/notifuse-${ENV_TARGET}-${GITHUB_RUN_ID:-manual}.nomad.hcl"
SSH="ssh -i /tmp/nomad_ci_key -o StrictHostKeyChecking=accept-new ${NOMAD_BASTION_USER}@${NOMAD_BASTION_HOST}"

echo "▶ deploy $JOB ($ENV_TARGET) → ${IMAGE_REPO}:${IMAGE_TAG} via SSH-bastion"

# ── clé SSH dédiée CI + known_hosts ────────────────────────────────────────────
umask 077
printf '%s\n' "$NOMAD_DEPLOY_SSH_KEY" > /tmp/nomad_ci_key
chmod 600 /tmp/nomad_ci_key
mkdir -p ~/.ssh
ssh-keyscan -H "$NOMAD_BASTION_HOST" >> ~/.ssh/known_hosts 2>/dev/null || true

# ── copie du HCL DU REPO (jamais la copie ~/nomad-veridian/jobs/ du bastion) ────
scp -i /tmp/nomad_ci_key -o StrictHostKeyChecking=accept-new "$HCL" "${NOMAD_BASTION_USER}@${NOMAD_BASTION_HOST}:${REMOTE_HCL}"

# ── pré-pull + validate + plan + run -detach + monitoring ciblé, IN SITU ───────
# Tout tourne sur le bastion : le token est sourcé là, ne transite pas par la CI.
# ENV_TARGET pilote le pré-pull : staging = nœud ovh-dev (ssh -n dev-pub) ; prod =
# nœud bastion (docker pull local). `ssh -n` IMPÉRATIF (sinon avale le stdin heredoc).
# shellcheck disable=SC2087
$SSH "IMAGE_TAG='${IMAGE_TAG}' IMAGE_REPO='${IMAGE_REPO}' REMOTE_HCL='${REMOTE_HCL}' ENV_TARGET='${ENV_TARGET}' bash -s" <<'REMOTE'
set -euo pipefail
source ~/credentials/nomad-bastion.env
export NOMAD_ADDR NOMAD_TOKEN="$NOMAD_MGMT_TOKEN"
IMG="${IMAGE_REPO}:${IMAGE_TAG}"

echo "== pré-pull authentifié de l'image sur le nœud cible ($ENV_TARGET) =="
if [ "$ENV_TARGET" = "staging" ]; then
  # -n : sinon ce ssh lit le stdin du heredoc et avale les commandes nomad suivantes.
  ssh -n -o BatchMode=yes -o ConnectTimeout=15 dev-pub "docker pull '$IMG'"
else
  docker pull "$IMG"   # prod = LE bastion (provider=contabo), auth ghcr root local
fi

echo "== nomad job validate =="
/usr/bin/nomad job validate -var "image_tag=${IMAGE_TAG}" "$REMOTE_HCL"

echo "== nomad job plan (diff inoffensif ; exit 1 = allocs à créer, by design) =="
/usr/bin/nomad job plan -var "image_tag=${IMAGE_TAG}" "$REMOTE_HCL" || true

echo "== nomad job run -detach + monitoring du déploiement CIBLÉ =="
# run -detach → Evaluation ID → DeploymentID de cet eval → deployment status -monitor.
# Évite le faux positif du poll `job status | grep successful` (ancien deployment) ET
# le faux négatif du `job run` bloquant (404 transitoire). Incident prospection 2026-07-11.
EVAL=$(/usr/bin/nomad job run -detach -var "image_tag=${IMAGE_TAG}" "$REMOTE_HCL" | grep -oP 'Evaluation ID:\s+\K\S+')
echo "eval: ${EVAL:-none}"
DEP=""
for i in $(seq 1 15); do
  DEP=$(/usr/bin/nomad eval status -json "$EVAL" 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin).get("DeploymentID") or "")' 2>/dev/null || true)
  [ -n "$DEP" ] && break
  sleep 2
done
if [ -z "$DEP" ]; then
  echo "Pas de déploiement créé (job inchangé = no-op) → OK"
else
  echo "deployment: $DEP — monitoring jusqu'à terminal…"
  /usr/bin/nomad deployment status -monitor "$DEP"
fi
REMOTE

# ── cleanup du HCL temporaire sur le bastion ───────────────────────────────────
$SSH "rm -f '${REMOTE_HCL}'" || true
rm -f /tmp/nomad_ci_key
echo "✓ deploy $ENV_TARGET soumis et vérifié"
