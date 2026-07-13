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

---

## ✅ CANON PROUVÉ — prospection l'a fait le 2026-07-12, COPIE-LE (ne réinvente pas)

prospection a livré **et validé end-to-end** (CI verte, bon SHA déployé, `/api/health`=200)
le **premier** pipeline gitops Nomad SSH-bastion. C'est LE patron de référence.

### Référence à lire/copier (repo `veridian-prospection`)
- **`deploy/README.md`** — runbook complet : schéma de flux, secrets, rollback, migrations, **11 pièges**,
  et une section §10 « comment une AUTRE app copie ce patron ». **Lis-le en premier.**
- `deploy/prospection.nomad.hcl` (prod) + `deploy/prospection-staging.nomad.hcl` (staging) — jobs
  versionnés avec `variable "image_tag"` + stanza `update` (cf piège 1).
- `.github/workflows/prospection-deploy-staging.yml` (job `deploy`) et `prospection-ci.yml`
  (job `deploy-prod`) — les 2 jobs CI à copier-adapter.

### Secrets GH PARTAGÉS (déjà posés, cross-app — RÉUTILISE, ne recrée pas)
`NOMAD_DEPLOY_SSH_KEY` (clé CI dédiée, sa publique est dans `~brunon5/.ssh/authorized_keys` du bastion),
`NOMAD_BASTION_HOST=75.119.158.217`, `NOMAD_BASTION_USER=brunon5`. (`CR_PAT` reste requis pour le
submodule privé `veridian-infra` sur les checkouts `submodules: recursive` + login GHCR.)

### Prérequis NODE one-shot : `docker login ghcr` pour ROOT sur chaque nœud
Nomad **ne pull pas les images privées ghcr** (son plugin docker n'a pas de bloc `auth`) → sans ça,
l'alloc reste `pending` → 502. Vérifié posé sur **ovh-dev + bastion**. Pour un autre nœud :
`sudo cp ~<user>/.docker/config.json /root/.docker/config.json` (le user SSH y a déjà l'auth).

### Les 4 PIÈGES qui ont coûté 6 itérations CI à prospection — évite-les d'emblée
1. **`update { healthy_deadline = "15m", progress_deadline = "20m", auto_revert = true }`** sur le
   group. Le 1er pull de l'image sur un nœud sans cache dépasse les **5min par défaut** → deployment
   marqué `failed` alors que l'app finit de démarrer (incident 502). `auto_revert` = filet de sécurité.
2. **Pré-pull authentifié de l'image sur le nœud cible AVANT `nomad job run`** (staging via
   `ssh -n dev-pub 'docker pull …'`, prod via `docker pull` local sur le bastion). Sinon Nomad lève
   un `401 unauthorized` sur l'image privée.
3. **`ssh -n` OBLIGATOIRE** pour tout `ssh` dans un heredoc (pré-pull, rm/scp du migrate) : sans `-n`,
   le ssh **lit le stdin du heredoc et AVALE les commandes `nomad job run` suivantes** → elles ne
   s'exécutent jamais (CI verte mais ancienne version live = **faux vert silencieux**).
4. **Vérif du deploy = `nomad deployment status -monitor <DeploymentID>` CIBLÉ** — PAS le poll
   `nomad job status | grep successful` (faux **positif** : lit le "successful" de l'ANCIEN
   déploiement) NI `nomad job run` bloquant (faux **négatif** : `404 deployment not found` transitoire).
   Résous le `DeploymentID` via l'`Evaluation ID` renvoyé par `nomad job run -detach` (petit retry).

### Ce que TU adaptes pour ton app
- `constraint ${meta.provider}` : `contabo` (bastion = prod/ingress) ou `ovh-dev` (staging). Vérifie
  où ton alloc tourne (`nomad job status <ton-job>`).
- Tag image : staging = `staging-<sha7>`, prod = `<sha7>` **sans** préfixe (cf `docker/metadata-action`).
- Migrate : container DB `db-<alloc>` (co-localisé) ou externe/Patroni selon ton cas ; `ssh -n` !
- Smoke : via **tailnet** si privé (`internal-only@nomad`), **direct** si public.
- DB co-localisée mono-instance → cible **Patroni HA** à terme (`nomad-veridian/tickets/TICKET-001`).

> Tout le détail (rollback par tag/`job revert`, deploy manuel, cleanup post-Dokploy) est dans
> **`veridian-prospection/deploy/README.md`**. En cas de doute prod / fenêtre → remonte à Robert.
