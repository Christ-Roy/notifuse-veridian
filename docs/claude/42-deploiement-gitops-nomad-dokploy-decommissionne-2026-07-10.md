# Déploiement — GitOps Nomad (⚠️ Dokploy DÉCOMMISSIONNÉ 2026-07-10)

Migration Dokploy → **cluster Nomad 3 nœuds** terminée. Le déploiement passe
désormais par des **jobs Nomad versionnés dans CE repo** (`deploy/*.nomad.hcl`),
poussés par la CI via `nomad job run` (plus de Dokploy, plus de `infra/compose/*.yml`).

- **Image** : `ghcr.io/christ-roy/notifuse-veridian:<tag>`
- **Jobs** : `deploy/notifuse.nomad.hcl` (prod, contabo-bastion) +
  `deploy/notifuse-staging.nomad.hcl` (ovh-dev, privé Tailscale + internal-only).
  Source de vérité gitops de l'app ; miroir infra : `~/nomad-veridian/jobs/`.
- **Deploy — verbes contraints du bastion** (constat C4 ; contrat dans
  `~/veridian/secrets-migration/C4-CONTRAT-CI.md`) : CI `veridian-ci.yml` →
  `scripts/ci/nomad-ssh-deploy.sh <env> <tag>` → trois appels SSH : `put-job <tier>`
  (le HCL du repo sur stdin), `deploy <tier> <tag>`, `cleanup <tier>`. La clé porte une
  **commande forcée** `command="/usr/local/sbin/veridian-ci-deploy notifuse"` : plus de
  shell, plus de heredoc, l'application est fixée côté serveur. Pré-pull ghcr
  authentifié, `validate`, `plan`, `run -check-index` et suivi du DeploymentID sont
  **dans le script serveur** : ne jamais les redupliquer côté CI. **Le NOMAD_TOKEN ne
  quitte JAMAIS le bastion**. deploy-staging = runner self-hosted (steps post-deploy
  tailnet) ; deploy-prod/rollback = ubuntu-latest.
- **Rollback** : verbe `revert prod` (job `rollback`, auto sur e2e-prod fail) : dernière
  version **stable** antérieure, pas `version-1`. Stanza `update{auto_revert=true}` =
  filet Nomad si deployment KO.
- **Secrets CI** : `NOMAD_DEPLOY_SSH_KEY_V2` (clé ed25519 `notifuse-ci-deploy-v2@github`,
  à commande forcée ; exposée au script sous le nom d'env `NOMAD_DEPLOY_SSH_KEY`) +
  `NOMAD_BASTION_HOST` + `NOMAD_BASTION_USER`. Secrets
  applicatifs = Nomad Variables `nomad/jobs/notifuse{,-staging}` (`template{env=true}`).
  ⚠️ Piège n°1 : Nomad ne pull pas les images privées ghcr → pré-pull authentifié +
  auth ghcr root sur les nœuds (bastion + ovh-dev, déjà posé).
- **Endpoints** : staging `notifuse.staging.veridian.site`, prod `notifuse.app.veridian.site`
- **Pilotage cluster** : skill `/nomad` (`nomad-v state`/`doctor`/`plan`/`deploy`).
  Control-plane = bastion Contabo. Détail migration : ticket
  `todo/2026-07-11-migration-ci-gitops-nomad.md`.
- ⚠️ **Legacy à nettoyer** (non bloquant) : `infra/compose/{staging,prod}.yml`,
  `scripts/ci/bump-compose-image.sh`, job `compose-validate`, secrets `DOKPLOY_*` —
  morts depuis la décommission Dokploy.

