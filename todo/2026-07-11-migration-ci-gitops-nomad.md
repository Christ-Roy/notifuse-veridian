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

## Validation CI RÉELLE (run 29166515686, 2026-07-11) — pipeline prouvé end-to-end
- ✅ **`deploy-staging` (GitOps Nomad — nomad job run) VERT** (1m33s) : la CI a
  réellement déployé staging via Nomad. C'est la preuve end-to-end de la migration.
- ✅ workflow-lint (§20), Go tests, build, résolution `db-<alloc>` : tous verts.
- ✅ Débloqué en passant : **govulncheck** (bump Go 1.25.11→1.25.12, CVE GO-2026-5856
  crypto/tls + GO-2026-4970 os) — la CI était rouge depuis ~17j pour ça (orthogonal
  à Nomad mais bloquait tout le pipeline → build/deploy jamais atteints).
- ⚠️ **e2e-staging : 285 passed / 1 failed** — le seul rouge = `chaos-provisioning.spec.ts:91`
  (1 des 5 provisions concurrentes du même tenant → 500 transitoire). **NON reproductible** :
  5 provisions concurrentes rejouées à la main → convergent proprement (0×500). = **flaky**
  de charge CI documenté (cf `flaky-ci-staging-postgres-saturation`), PAS une régression de
  la migration (le déploiement est vert, l'app saine). N'impacte pas la prod (opt-in).
  Piste si récurrent : DB staging Nomad à 256MB (vs 512 en prod) — à confirmer avant de bumper.
- Preuves unitaires (pré-CI) : `nomad job validate`+`plan` verts, `nomad-deploy.sh staging`
  exécuté en réel, token scopé testé (deploy OK, escalade ACL/node → 403).
- **Prod** : deploy-prod reste opt-in `[risk:low]` — 1er déploiement prod Nomad au prochain
  push marqué, sous surveillance (rollback auto `nomad job revert` si /api/version ≠ tag).
