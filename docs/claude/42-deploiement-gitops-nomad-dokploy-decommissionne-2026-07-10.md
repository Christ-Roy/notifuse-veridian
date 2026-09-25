# Déploiement — GitOps Nomad (⚠️ Dokploy DÉCOMMISSIONNÉ 2026-07-10)

Migration Dokploy → **cluster Nomad 3 nœuds** terminée. Le déploiement passe
désormais par des **jobs Nomad versionnés dans CE repo** (`deploy/*.nomad.hcl`),
poussés par la CI via `nomad job run` (plus de Dokploy, plus de `infra/compose/*.yml`).

- **Image** : `ghcr.io/christ-roy/notifuse-veridian:<tag>`
- **Jobs** : `deploy/notifuse.nomad.hcl` (prod, contabo-bastion) +
  `deploy/notifuse-staging.nomad.hcl` (ovh-dev, privé Tailscale + internal-only).
  Source de vérité gitops de l'app ; miroir infra : `~/nomad-veridian/jobs/`.
- **Deploy — canon SSH-bastion** (décision Robert, cf `veridian-prospection/deploy/README.md`) :
  CI `veridian-ci.yml` → `scripts/ci/nomad-ssh-deploy.sh <env> <tag>` → SSH vers le
  bastion (clé dédiée CI), pré-pull image ghcr (auth du nœud), scp le HCL (qui déclare
  `variable image_tag`), `nomad job run -var image_tag=<tag>` + `deployment status
  -monitor`. **Le NOMAD_TOKEN ne quitte JAMAIS le bastion** (lu in situ). deploy-staging
  = runner self-hosted (steps post-deploy tailnet) ; deploy-prod/rollback = ubuntu-latest.
- **Rollback** : `nomad job revert notifuse <version-1>` via SSH-bastion (job `rollback`,
  auto sur e2e-prod fail). Stanza `update{auto_revert=true}` = filet Nomad si deployment KO.
- **Secrets CI** : `NOMAD_DEPLOY_SSH_KEY` (clé ed25519 dédiée notifuse, publique dans
  authorized_keys bastion) + `NOMAD_BASTION_HOST` + `NOMAD_BASTION_USER`. Secrets
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

