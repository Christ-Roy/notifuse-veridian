# Migrer le déploiement notifuse en GitOps Nomad (CI)

> **Sévérité** : 🔴 P1 (déploiement prod client — migration Dokploy→Nomad)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-07-11
> **Source** : ticket standard infra `~/nomad-veridian/tickets/TEMPLATE-app-gitops-nomad.md`
> + demande Robert 2026-07-11 ("on a migré sur nomad, revoir le gitops/CI")

## Contexte (fait par l'infra)
Migration Dokploy → **cluster Nomad 3 nœuds** TERMINÉE (2026-07-10). Dokploy
décommissionné. notifuse prod tourne DÉJÀ en job Nomad (`notifuse.nomad.hcl`,
sur contabo-bastion, Model B ingress Traefik) et sert les vrais clients. La CI
notifuse (`veridian-ci.yml`) déployait encore via **Dokploy API** (obsolète).

Contrat gitops (`~/nomad-veridian/GITOPS-NOMAD.md` §1) : chaque app porte son job
dans `deploy/<app>.nomad.hcl` + CI `nomad job run` via secrets NOMAD_ADDR/TOKEN.

## FAIT (cette session, 2026-07-11)
- ✅ **`deploy/notifuse.nomad.hcl` + `deploy/notifuse-staging.nomad.hcl`** rapatriés
  (miroir FIDÈLE du live — `nomad job plan` = "in-place update / all tasks allocated",
  aucune dérive). En-têtes de commentaire nettoyés (le "lab/SMTP coupé" était périmé).
- ✅ **`scripts/ci/nomad-deploy.sh`** (bump tag image → validate → plan → run →
  wait /api/version == tag). Testé bout-en-bout sur staging réel (run OK, version servie).
- ✅ **CI `veridian-ci.yml` réécrite** : `deploy-staging`, `deploy-prod`, `rollback`
  passent de Dokploy à Nomad. deploy-prod/rollback → runner **self-hosted** (tailnet +
  binaire nomad). rollback = `nomad job revert` (version précédente, atomique).
  e2e-staging : `STAGING_DB_CONTAINER` résolu dynamiquement (`db-<alloc_id>`, le nom
  Dokploy `notifuse-staging-db` n'existe plus). YAML validé.
- ✅ **Secrets GitHub** `NOMAD_ADDR` + `NOMAD_TOKEN` posés sur le repo. Token Nomad
  **SCOPÉ** (policy `notifuse-cd` : submit-job/read/logs sur namespace default —
  PAS de mgmt/ACL/node ; vérifié : 403 sur create-token et node status). Pas le
  mgmt token global (moindre privilège en CI).
- ✅ deploy-prod reste **OPT-IN** (marker `[risk:low]` §20 conservé) — pas de bascule
  prod auto tant que non validée.

## RESTE À FAIRE / COORDINATION (remonté à Robert + infra)
1. **Autorité du HCL** : le repo notifuse devient la source de vérité du job (la CI
   déploie depuis `deploy/`). `~/nomad-veridian/jobs/notifuse*.nomad.hcl` doit devenir
   un simple **miroir** (ou être retiré) → sinon divergence si les 2 sont édités.
   À acter avec l'agent infra. Aujourd'hui les 2 sont identiques (aucun risque immédiat).
2. **Activation prod** : au prochain push `[risk:low]` (ou workflow_dispatch
   deploy_prod=true), deploy-prod déploiera prod via Nomad. Valider le 1er passage
   sous surveillance (le script fail→rollback auto si /api/version ne matche pas).
3. **DB Patroni HA** (chantier infra, backlog `nomad-veridian`) : la DB prod notifuse
   est un postgres:17 MONO-INSTANCE co-localisé (reschedule OFF, bind bastion) → si le
   bastion tombe, notifuse prod tombe. Cible = cluster Patroni (failover Consul).
   Migration DB prod = downtime + backup frais + fenêtre coordonnée. NON traité ici.
4. **Cleanup** (non bloquant) : `infra/compose/{staging,prod}.yml` + `bump-compose-image.sh`
   + job `compose-validate` deviennent morts (Dokploy parti). À retirer dans un lot
   dédié (garde la CI verte pour l'instant). Secrets Dokploy (`DOKPLOY_*`) supprimables.
5. **Warning HCL** `shutdown_delay` non set (hérité du live) — amélioration mineure,
   à ajouter côté infra sur le miroir pour cohérence.

## Validation
- `nomad job validate` + `nomad-v plan` verts sur les 2 HCL (dry-run cluster).
- `nomad-deploy.sh staging` exécuté en réel (registration successful, /api/version OK).
- Résolution `db-<alloc>` testée sur dev-pub réel.
- Token scopé testé (deploy OK, escalade ACL/node refusée 403).
- E2E on-premise complet du pipeline prod = au 1er push `[risk:low]` (opt-in).
