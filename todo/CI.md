# TODO CI — Notifuse Veridian

> Sprint P0 Standard CI Veridian — spécialisé Notifuse (Go, fork upstream).
> Standard de référence : `../../CI-ARCHITECTURE.md` (racine `veridian-platform/`).
> Constitution CI : `../CLAUDE.md` section "Constitution CI".
>
> **Dernière mise à jour** : 2026-05-17 (déblocage push : faux positifs script + CVE npm)

## ✅ FAIT — 2026-05-17 (déblocage push normal)

- [x] **Script `check-test-mapping.sh` débogué** : reconnaissance pattern `httptest.NewServer + Sprintf`, filtre lignes commentées, scan récursif `internal/http/**`, pipes safe sous `set -euo pipefail`. Voir détails dans la sous-section "Routes API orphelines résolues" plus bas.
- [x] **CVE npm HIGH résolues (2026-05-17)** :
  - `liquidjs <10.25.7` (HIGH 7.5 DoS via circular block) → `10.25.5` est compatible avec lock bumped → `10.27.0`
  - `fast-xml-builder <=1.1.6` (HIGH 6.1) → fix via update transitif
  - `postcss <8.5.10` (MODERATE 6.1 XSS) → fix patch en passant
  - `npm audit fix` sans `--force` (pas de breaking, juste patch lock-file). 866 packages auditées → **0 vulnérabilités**.
- [x] **`tests-pending.txt`** : 12 → 8 fichiers (4 retirés, leur test colocalisé existe déjà).

---

## ✅ FAIT — État actuel du flow CI

### Husky pre-push (mode Nuclear, 0 dette autorisée)

`scripts/ci/check-test-mapping.sh` bloque le push si :

- [x] **Fichier critique modifié sans test colocalisé** (`internal/{http,service,repository,domain}/*.go` ↔ `*_test.go` au même niveau)
- [x] **Règle 1-pour-1 stricte** : nouvelle func exportée (majuscule initiale, top-level + receivers) sans nouveau `TestXxx` correspondant
- [x] **Migration SQL** dans `internal/migrations/` sans test repo/service modifié
- [x] **Route API déclarée** (`mux.Handle("/api/...")`) sans test l'exerçant (`httptest.NewRequest(..., "/api/...")`)
- [x] **Handler API modifié** sans test exerçant ses routes modifié dans le même push
- [x] **Bypass upstream** : commits 100% upstream Notifuse (`@notifuse.com`, `pierre@bazoge.com`, etc.) skip le mapping sur fichiers non-`veridian_*`
- [x] Path-based skip docs : `paths-ignore` actif sur `**.md`, `docs/`, `runbooks/`, `plans/`, `TODO.md`, `tests-pending.txt`

Installé via :
```bash
make setup-hooks    # chmod + git config core.hooksPath .husky
```

**État dette à fin de session** : 15 routes API orphelines (cf. ❌ ci-dessous), bloquent tout push normal.

### Workflow `.github/workflows/veridian-ci.yml` (11 jobs)

```
test-mapping    ─┐
compose-validate─┤    (ubuntu-latest, parallèle)
cve-scan        ─┤
                 ↓
              test-go (ubuntu-latest + service Postgres)
                 ↓
              build (self-hosted dev-pub, push GHCR)
                 ↓
              deploy-staging (self-hosted, compose pur via Traefik staging-edge)
                 ↓
              e2e-staging (self-hosted, Playwright headful)
                 ↓
        ┌────────┼────────────────────────────┐
        ↓        ↓                            ↓
   deploy-prod  promote-prod-compose      (skipped sur push normal)
   (workflow_  (workflow_dispatch
    dispatch,   manual, opt-in)
    image only)
        ↓        ↓ → push notifuse-deploy → webhook Dokploy → redeploy
   e2e-prod
        ↓
   rollback (auto si e2e-prod fail)
```

| Job | État dernier run (25852461033) |
|---|---|
| test-mapping | ✅ success |
| compose-validate | ✅ success |
| cve-scan | ✅ success |
| test-go | ✅ success |
| build | ✅ success |
| deploy-staging | ✅ success |
| e2e-staging | ❌ failure (à investiguer) |
| deploy-prod | ⏭️ skipped (normal) |
| e2e-prod | ⏭️ skipped |
| rollback | ⏭️ skipped |
| promote-prod-compose | ⏭️ skipped (opt-in) |

### Pattern compose (1 fichier autonome par env)

- [x] `infra/compose/staging.yml` — services `notifuse-staging` + `notifuse-staging-db`, network `staging-edge` (Traefik standalone dev-pub), volumes locaux nommés, image `:latest` mutable, Host `notifuse.staging.veridian.site`
- [x] `infra/compose/prod.yml` — services `notifuse-prod` + `notifuse-prod-db` alignés avec containers Dokploy existants (zéro diff sémantique vs `notifuse-deploy/docker-compose.yml` actuel → promotion sans downtime attendue), volumes externes `infra_notifuse-*`, network `dokploy-network`, Host `notifuse.app.veridian.site`
- [x] `scripts/ci/generate-compose.sh` — valide via `docker compose config --quiet`, pas de génération côté repo (compose merge non reproductible cross-version)

### Staging déployé en réel sur dev-pub (validé bout en bout)

- [x] Containers `notifuse-staging` + `notifuse-staging-db` healthy
- [x] Endpoint `https://notifuse.staging.veridian.site/api/setup.status` → HTTP 200 / 137ms (HTTP/2, cert Let's Encrypt OK, Traefik route)
- [x] `/opt/veridian/staging/notifuse/.env` créé sur dev-pub (chmod 600, ubuntu:ubuntu)
- [x] GHCR login sur dev-pub via `gh auth token | docker login ghcr.io -u Christ-Roy --password-stdin`
- [x] Cycle pull → up -d → force-recreate testé manuellement

### Promote-prod-compose (job opt-in)

- [x] Job `promote-prod-compose` ajouté, `workflow_dispatch + promote_prod_compose=true` uniquement
- [x] Clone `notifuse-deploy`, copie `infra/compose/prod.yml` → `docker-compose.yml`, commit, push → webhook Dokploy → redeploy
- [x] Wait healthy `/api/setup.status` + Telegram notif succès/échec
- [x] Procédure documentée dans `CLAUDE.md` section "Promotion vers prod"

### GitHub Environments + secrets

- [x] Environment `staging` créé (id 15309935895)
- [x] Environment `production` créé (id 15309933105) avec branch policy `veridian` + `v*-veridian.*`
- [x] Secret `TELEGRAM_BOT_TOKEN` ajouté
- [x] Secrets existants OK : `DOKPLOY_*`, `NOTIFUSE_HUB_API_SECRET_{STAGING,PROD}`

### Renovate (auto-merge total)

- [x] `.github/renovate.json` créé : patch + minor + major + CVE auto-mergés, zéro `needs-human-review`

### Audit prod final (2026-05-14)

| Check | État |
|---|---|
| Container `notifuse-prod` | ✅ Up 21h, 0 restart, healthy |
| Endpoint `/api/setup.status` | ✅ HTTP 200 / 100-148ms |
| Logs erreurs 24h | ✅ Aucune |
| RAM/CPU app | ✅ 49 MiB / 1.89 % |
| Cert SSL | ✅ Valide jusqu'au 21 juin 2026 |
| GitOps Dokploy | ✅ `notifuse-deploy/main` → webhook actif |
| Image active | ✅ `saas-v1.0.3@sha256:b07226feb...` |

---

## ❌ RESTE À FAIRE

### 🚨 Bloquant pour push normal (mode Nuclear)

- [x] **Routes API orphelines résolues (2026-05-17)** :
  - Script `check-test-mapping.sh` corrigé : reconnaît désormais le pattern `httptest.NewServer(mux) + fmt.Sprintf("%s/api/...", serverURL)` (en plus de `httptest.NewRequest(..., "/api/...")` direct), filtre les lignes commentées (`//` en début de ligne), grepe tout `internal/http/**` (pas juste racine).
  - **12 faux positifs éliminés** (toutes les routes `templates.*`, `templateBlocks.*`, `contacts.getByEmail` étaient en fait couvertes via `NewServer + Sprintf`).
  - **2 vrais bugs corrigés** :
    - `transactional_handler_test.go:1540` : `httptest.NewRequest("/api/email.testTemplate")` → `/api/transactional.testTemplate` (route renommée upstream, test pas mis à jour).
    - `task_handler_test.go:267` : tableau de routes `TestTaskHandler_RegisterRoutes` mis à jour : remplacement `tasks.executePending` (n'existe plus) par `tasks.reset, tasks.trigger, cron, cron.status` (routes réelles).
  - **1 faux positif (commentaire de doc) résolu par filtre `^\s*//`** : `auth.go:112` `//	mux.Handle("/api/sensitive.operation", ...)` n'est plus compté.
  - Mode Nuclear : 0 dette de routes, tous les tests `internal/http/` verts.

### 🐛 Bugs/incidents à investiguer

- [ ] **e2e-staging fail sur run 25852461033** : Playwright a échoué après migration staging compose pur. Probablement tests écrits pour l'ancien staging Dokploy (URLs `saas-notifuse.staging.veridian.site` durcies dans les specs ?). À regarder via `gh run view 25852461033 --log-failed`.

### 🔐 Secrets manquants (non bloquants)

- [ ] **`TELEGRAM_CHAT_ID`** — utilisé par rollback + promote-prod-compose pour notifs. `getUpdates` actuel est vide (`{"ok":true,"result":[]}`), il faut **envoyer un message au bot `@<bot_username>` ou dans le groupe Telegram dédié** pour qu'il apparaisse. Robert : envoie "test" au bot puis je relance.
- [ ] **`NOTIFUSE_DEPLOY_PUSH_PAT`** — PAT scope `repo` sur `Christ-Roy/notifuse-deploy`. Sans, le job promote-prod-compose explose. À créer avant le premier passage prod.

### 🎯 Premier promote-prod réel (validation finale)

- [ ] Une fois `NOTIFUSE_DEPLOY_PUSH_PAT` en place, lancer `workflow_dispatch` avec `promote_prod_compose=true` sur un commit dont l'e2e-staging est vert. Diff sémantique vs compose live est vide → **aucun changement comportemental attendu**, juste promotion de notre `infra/compose/prod.yml` comme nouvelle source de vérité de `notifuse-deploy`.

### 🚦 App Renovate à installer

- [ ] Le fichier `.github/renovate.json` est en place mais l'**App Renovate doit être installée** sur le repo (https://github.com/apps/renovate → Install → select `Christ-Roy/notifuse-veridian`). Sans ça, le fichier est ignoré.

### 🛡️ Standard pas encore atteint (Constitution §13)

- [ ] **Trivy multi-scan étage 1** : `trivy fs --scanners vuln,secret,misconfig,license --severity CRITICAL,HIGH` + SARIF upload GitHub Security tab
- [ ] **Trivy image scan étage 2** : sur image GHCR fraîchement buildée + `--exit-on-eol 1` + SBOM CycloneDX artifact
- [ ] **gitleaks** dans le diff Git
- [ ] **govulncheck** dans test-go (équivalent Go de `npm audit`)
- [ ] **Cron hebdo Trivy** sur images prod running

### 📦 Migrations DB (Constitution §12)

- [ ] **Script `check-migration-safety.sh`** adapté Go pour Notifuse : bloque DROP COLUMN, DROP TABLE, ALTER NOT NULL sur table peuplée, RENAME, CREATE INDEX sans CONCURRENTLY. Migrations Notifuse sont dans `internal/migrations/v*.go`.
- [ ] **Test backward-compat étage 2** : pull image `:rollback`, apply migration PR, lance `:rollback` contre schéma N, smoke test.

### 🧹 Path-based gates strictes

- [ ] **Gate structurel 24h** : si `Dockerfile`, `go.mod`, `internal/migrations/**` modifiés → exiger commit déployé sur staging > 24h avant `deploy_prod=true`. Aujourd'hui `deploy-prod` est `workflow_dispatch` manuel uniquement (équivalent fonctionnel pour clients réels).

### 🏗️ Staging avancé

- [ ] **Ephemeral staging per PR** (CI-ARCHITECTURE §9) : workflow `staging-ephemeral.yml` qui spawn une stack par branche feature (URL `notifuse-<branch-slug>.staging.veridian.site`), teardown au merge/close.
- [ ] **GC hebdo stacks orphelines** : cron `staging-gc.sh` sur dev-pub.
- [ ] **Snapshot prod anonymisé** restauré à chaque deploy staging (cron nightly + script anonymizer dans `veridian-infra`).

### 🔔 Annotations Grafana (Constitution §9)

- [ ] `obs annotate deploy` câblé dans deploy-staging + deploy-prod (après wait healthy)
- [ ] `obs annotate rollback` dans le job rollback
- [ ] `obs annotate migrate` avant + après migration DB

### 🚦 Defense in depth (CI-ARCHITECTURE §17)

- [ ] **Workflow `emergency-revert.yml`** : tout auto-rollback Docker déclenche un revert Git auto + freeze main jusqu'à merge revert
- [ ] **Webhook Grafana → repository_dispatch** : alertes oom_killed, memory_creep, synthetic_failed_3x câblées sur emergency-rollback
- [ ] **Lint workflow YAML custom** : rejette les jobs self-hosted sans step cleanup `always()` (déjà appliqué manuellement sur build/deploy-staging/e2e-staging)

### 📊 Dette baseline

- [ ] **`tests-pending.txt`** = **8 fichiers** veridian-custom à résorber sous 90 jours (cible : 0)
  - 4 retirés le 2026-05-17 (avaient déjà leur test : `veridian_token.go`, `veridian_handler.go`, `veridian_autologin_handler.go`, `veridian_magic_handler.go`)
  - Restants : `auth.go`, `blog_feed.go`, `contact_segment_queue.go`, `ses_client.go`, `telemetry.go`, `veridian.go` (domain), `workspace_postgres.go` (repository), `llm_service_openai.go` (service)
- [ ] **`test-coverage-map.yaml`** vide — peupler quand un fichier critique est légitimement couvert par un test d'intégration ailleurs

### 📅 Validation 7 jours

- [ ] **7 jours consécutifs de pushes sans `--no-verify`** — compte démarre quand les 15 tests routes orphelines auront été ajoutés. Cible : J+7 après ce moment.

---

## 🧪 Procédures opérationnelles

### Tester staging à la main via Chrome MCP

GitHub UI → Actions → "Notifuse Veridian CI/CD" → Run workflow :
- Cocher `skip_wipe=true` + `skip_e2e=true`
- Le build pousse l'image, deploy-staging redéploie staging, **mais** :
  - Aucun wipe des tenants de test
  - Aucun Playwright auto qui mange les routes en parallèle
- Tester via Chrome MCP sur `https://notifuse.staging.veridian.site`
- Pour reprendre la CI normale : push un commit sur `veridian` (sans inputs)

### Promouvoir une nouvelle image en prod (image only)

GitHub UI → Run workflow → cocher `deploy_prod=true`. Trigger `POST /api/compose.redeploy` Dokploy avec l'API token. Ne touche pas au compose.

### Promouvoir un changement de compose en prod (structurel)

1. Modifier `infra/compose/prod.yml`
2. Commit + push sur `veridian` (CI complète tourne, e2e-staging doit être vert)
3. GitHub UI → Run workflow → cocher `promote_prod_compose=true` sur ce SHA exact
4. Le job clone `notifuse-deploy`, remplace `docker-compose.yml` par notre version (avec en-tête de traçabilité), commit, push → webhook Dokploy → redeploy

### Rollback prod

- **Auto** : si `e2e-prod` fail après `deploy-prod`, le job `rollback` retag `:rollback` → `:latest` et redéploie
- **Manuel** : retag GHCR + trigger redeploy Dokploy (steps du job `rollback` dans le workflow)

### Bypass urgence (interdit Constitution §3)

```bash
git push --no-verify        # contourne Husky
git config core.hooksPath '' # désactive complètement les hooks locaux
git config core.hooksPath .husky  # réactive
```

Le job CI `test-mapping` rejouera quand même le script côté GitHub, donc bypass local ne contourne pas la CI distante (defense in depth §1).

---

## 📍 Repères fichiers

- **Workflow** : `.github/workflows/veridian-ci.yml`
- **Script mapping** : `scripts/ci/check-test-mapping.sh` (mode Nuclear)
- **Script compose** : `scripts/ci/generate-compose.sh` (validate-only)
- **Hook** : `.husky/pre-push`
- **Compose** : `infra/compose/{prod,staging}.yml`
- **Dette tests** : `tests-pending.txt`
- **Couverture non-canonique** : `test-coverage-map.yaml`
- **Renovate** : `.github/renovate.json`
- **Constitution** : `CLAUDE.md` section "Constitution CI"
- **Standard global** : `../../CI-ARCHITECTURE.md`

## 📍 Repères serveurs

- **dev-pub** (37.187.199.185) : `/opt/veridian/staging/notifuse/` (compose + .env), `~/traefik-staging/` (Traefik standalone)
- **prod-pub** (OVH VPS) : Dokploy `compose-transmit-open-source-microchip-k9lvap` tire depuis `Christ-Roy/notifuse-deploy/main`
- **runner self-hosted** : `veridian-dev-server-notifuse` (agentId 21) sur dev-pub
- **GHCR** : `ghcr.io/christ-roy/notifuse-veridian:{latest,rollback,saas-v1.0.3,v30.1-veridian.*}`
