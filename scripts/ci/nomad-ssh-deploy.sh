#!/usr/bin/env bash
# nomad-ssh-deploy.sh — déploie notifuse (staging|prod) via les verbes contraints
# du bastion Nomad.
#
# Canon Veridian (décision Robert 2026-07-11) : le NOMAD_TOKEN ne quitte JAMAIS le
# bastion. Constat C4 de l'audit d'exposition (2026-08) : ça ne suffisait pas. La
# clé `notifuse-ci-deploy@github` ouvrait un shell sur un compte NOPASSWD:ALL du
# groupe docker, donc n'importe quel heredoc envoyé par la CI pouvait simplement
# lire ~/credentials/nomad-bastion.env — le jeton management, c'est-à-dire les
# secrets de toutes les applications.
#
# Le correctif est côté serveur : la clé porte désormais une commande forcée
#   command="/usr/local/sbin/veridian-ci-deploy notifuse",no-pty,no-*-forwarding
# L'application est fixée DANS LA LIGNE DE LA CLÉ : ce dépôt ne peut déployer que
# notifuse, jamais le Hub ni le CMS. La CI ne fournit plus qu'un verbe, un tier et
# un tag, tous validés par motif strict côté bastion (refus = code 64).
#
# Ce script tourne donc sur le runner GitHub et ne fait plus que trois appels :
#   put-job <tier>          ← le HCL DU REPO, poussé sur stdin
#   deploy  <tier> <tag>
#   cleanup <tier>
#
# Le pré-pull authentifié de l'image ghcr sur le nœud cible, le `nomad job
# validate`, le `plan`, le `run -detach -check-index` et le suivi du DeploymentID
# exact jusqu'à l'état terminal sont désormais DANS le script serveur. On ne les
# duplique pas ici : aucune garantie n'est perdue, elles sont simplement passées
# côté bastion, là où le jeton vit.
#
# Contrat complet : ~/veridian/secrets-migration/C4-CONTRAT-CI.md
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

# Le job Nomad et le dépôt d'image ne sont plus décidés ici : la table serveur du
# bastion les impose. On ne garde que le chemin du HCL du repo, qui est la seule
# chose que la CI a le droit d'apporter (et que le bastion revalide : il refuse un
# HCL qui ne déclare pas le job attendu pour ce couple app/tier).
case "$ENV_TARGET" in
  staging) HCL="deploy/notifuse-staging.nomad.hcl" ;;
  prod)    HCL="deploy/notifuse.nomad.hcl" ;;
  *) echo "✗ env invalide '$ENV_TARGET' (staging|prod)" >&2; exit 1 ;;
esac
[[ -f "$HCL" ]] || { echo "✗ HCL introuvable : $HCL" >&2; exit 1; }

KEY_FILE=/tmp/nomad_ci_key
# Jamais de -t : les clés CI sont en no-pty côté bastion. BatchMode=yes pour que
# la moindre demande interactive échoue au lieu de faire poireauter le runner.
SSH_OPTS=(-i "$KEY_FILE" -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15)
BASTION="${NOMAD_BASTION_USER}@${NOMAD_BASTION_HOST}"

echo "▶ deploy notifuse ($ENV_TARGET) → tag ${IMAGE_TAG} via les verbes contraints du bastion"

# ── clé SSH dédiée CI + known_hosts ────────────────────────────────────────────
umask 077
printf '%s\n' "$NOMAD_DEPLOY_SSH_KEY" > "$KEY_FILE"
chmod 600 "$KEY_FILE"
# Le trap garantit que la clé privée ne survit pas au step, même si un verbe
# échoue et que `set -e` coupe le script en cours de route.
trap 'rm -f "$KEY_FILE"' EXIT
mkdir -p ~/.ssh
ssh-keyscan -H "$NOMAD_BASTION_HOST" >> ~/.ssh/known_hosts 2>/dev/null || true

# ── put-job : le HCL DU REPO sur stdin ─────────────────────────────────────────
# Jamais la copie ~/nomad-veridian/jobs/ du bastion, et plus de scp vers un chemin
# choisi par le client : le bastion range le HCL lui-même, dans son propre
# répertoire de travail à 0700.
echo "== put-job ${ENV_TARGET} =="
ssh "${SSH_OPTS[@]}" "$BASTION" "put-job ${ENV_TARGET}" < "$HCL"

# ── deploy : pré-pull + validate + plan + run -check-index + suivi, côté bastion ─
# -n : ce ssh ne doit pas consommer le stdin du step.
echo "== deploy ${ENV_TARGET} ${IMAGE_TAG} =="
ssh -n "${SSH_OPTS[@]}" "$BASTION" "deploy ${ENV_TARGET} ${IMAGE_TAG}"

# ── cleanup du matériel temporaire déposé sur le bastion ───────────────────────
ssh -n "${SSH_OPTS[@]}" "$BASTION" "cleanup ${ENV_TARGET}" || true

echo "✓ deploy $ENV_TARGET soumis et vérifié"
