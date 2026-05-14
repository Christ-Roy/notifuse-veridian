# TODO CI — Notifuse Veridian

> Sprint P0 Standard CI Veridian — spécialisé Notifuse (Go, fork upstream).
> Standard de référence : `../../CI-ARCHITECTURE.md` (racine `veridian-platform/`).
> Constitution CI : `../CLAUDE.md` section "Constitution CI".

---

## ✅ Fait

### Mapping source ↔ test (Constitution §1, §4)

- [x] `scripts/ci/check-test-mapping.sh` versionné — Go variant, convention
      idiomatique `foo.go ↔ foo_test.go` côte à côte.
- [x] 4 scopes critiques couverts :
      `internal/http/`, `internal/service/`, `internal/repository/`,
      `internal/domain/`.
- [x] Règle 1-pour-1 stricte : chaque nouvelle func exportée (majuscule
      initiale, receivers + top-level) exige au moins un nouveau `TestXxx`
      dans le test colocalisé.
- [x] Migrations SQL dans `internal/migrations/` exigent un test
      `repository/` ou `service/` modifié dans la même PR.

### Bypass upstream sync (Constitution §7)

- [x] Détection auteur via `git log --pretty=%ae $BASE_REF...HEAD`.
- [x] Regex upstream : `@notifuse\.com$|^pierre@bazoge\.com$|^pierre@(Air-de-Pierre|Host-[0-9]+)\.lan$`
- [x] Si **tous** les commits du diff sont upstream → skip mapping sur
      fichiers `non-veridian_*`. Les `veridian_*.go` restent sous discipline
      stricte (filet de sécurité même si upstream les touche, ce qui ne
      devrait jamais arriver vu la convention flat).

### Hook local (Constitution §2, §3)

- [x] `.husky/pre-push` installé, exécute `check-test-mapping.sh` avec
      `BASE_REF=origin/<branch>` (fallback `origin/main`).
- [x] `make setup-hooks` : `chmod +x` + `git config core.hooksPath .husky`.
- [x] `make check-test-mapping` : run le check en local sans push.

### Allowlist transitoire (Constitution §5)

- [x] `tests-pending.txt` baseline générée — 12 fichiers veridian-custom
      orphelins listés :
      - `internal/domain/{auth,blog_feed,contact_segment_queue,ses_client,telemetry,veridian,veridian_token}.go`
      - `internal/http/veridian_{autologin,handler,magic}_handler.go`
      - `internal/repository/workspace_postgres.go`
      - `internal/service/llm_service_openai.go`

### Coverage map non-canonique (Constitution §6)

- [x] `test-coverage-map.yaml` créé (vide initialement, à peupler au fil
      des PRs où un fichier critique est légitimement couvert par un test
      ailleurs).

### Convention veridian-override (CLAUDE.md "Veridian customizations")

- [x] Tranchée : **préfixe `veridian_*.go` en flat** (PAS de sous-dossier
      `internal/http/veridian/`). 4 raisons documentées dans CLAUDE.md :
      - Idiomatique Go : packages plats par responsabilité, pas par origine
      - Pas d'import cycle
      - Grep-friendly : `ls internal/http/veridian_*`,
        `git diff upstream/main -- internal/http/ ':!internal/http/veridian_*'`
      - Sync upstream triviale : aucun fichier upstream ne porte ce préfixe
- [x] Déjà appliqué pour les 3 handlers existants
      (`veridian_handler.go`, `veridian_autologin_handler.go`,
      `veridian_magic_handler.go`).

### CI workflow `veridian-ci.yml`

- [x] **Path-based skip docs** sur push + PR :
      `paths-ignore: ['**.md', 'docs/**', 'runbooks/**', 'plans/**', 'TODO.md', 'tests-pending.txt']`
- [x] **Job `test-mapping`** ubuntu-latest (defense in depth §1) :
      rejoue `check-test-mapping.sh` en CI avant `test-go`. Attrape les
      PRs externes / Renovate qui n'auraient pas le hook local.
- [x] **Job `test-go`** : Veridian-specific tests + tests upstream qui
      doivent toujours passer.
- [x] **Job `build`** self-hosted : retag `:rollback` → build → push GHCR
      (`ghcr.io/christ-roy/notifuse-veridian:<tag>`).
- [x] **Job `deploy-staging`** : force pull GHCR + recreate container
      Dokploy + wait healthy + cleanup test tenants HMAC.
- [x] **Job `e2e-staging`** Playwright bloquant (Constitution §11).
- [x] **Job `deploy-prod`** : `workflow_dispatch + deploy_prod=true`
      uniquement (jamais auto).
- [x] **Garde pixel-parfait avant prod** : `deploy-prod` exige qu'un run
      automatique sur le SHA exact ait passé `e2e-staging` avec
      `conclusion == success`. Refuse sinon. Vérification via
      `gh api /repos/.../actions/workflows/veridian-ci.yml/runs?head_sha=...`.
- [x] **Job `e2e-prod`** Playwright tag `@prod-safe` après deploy.
- [x] **Job `rollback`** auto si `e2e-prod` fail : retag broken-<sha>,
      restore `:rollback` → `:latest`, redeploy via Dokploy API,
      notification Telegram.

### Flags test manuel staging via Chrome MCP

- [x] `workflow_dispatch.skip_wipe=true` → skip step
      "Cleanup test tenants" (préserve les tenants créés pendant test
      manuel).
- [x] `workflow_dispatch.skip_e2e=true` → skip job `e2e-staging` complet
      (libère staging du Playwright auto, pas de concurrence pendant
      test manuel).

### Renovate (CI-ARCHITECTURE §10, Constitution §14)

- [x] `.github/renovate.json` créé — auto-merge total :
      - Patch : auto-merge branch
      - Minor : auto-merge PR
      - Major : auto-merge PR
      - CVE (`vulnerabilityAlerts`) : auto-merge
      - `lockFileMaintenance` hebdo auto-merge
- [x] Zéro `needs-human-review` (Constitution §A1).
- [x] Groupings : gomod patch+minor, github-actions, frontend deps.

### GitHub Environments

- [x] Environment `staging` créé sur `Christ-Roy/notifuse-veridian`
      (id 15309935895) — pas de protection rule.
- [x] Environment `production` créé (id 15309933105) — branch policy
      restrictive : `veridian` + tag `v*-veridian.*` uniquement.
- [x] Workflow câblé : `deploy-staging.environment: { name: staging,
      url: https://notifuse.staging.veridian.site }` et
      `deploy-prod.environment: { name: production,
      url: https://notifuse.app.veridian.site }`.

### Documentation

- [x] Section "Veridian customizations" ajoutée à `CLAUDE.md` racine
      (branche, convention `veridian_*.go`, sync upstream, Dokploy, E2E).
- [x] Section "Constitution CI" ajoutée — 15 règles non négociables
      adaptées Go.
- [x] Commandes utiles documentées :
      `BASE_REF=HEAD scripts/ci/check-test-mapping.sh`,
      `make setup-hooks`, `make check-test-mapping`,
      `git ls-files | grep -E 'veridian_|veridian\.go|veridian_token'`.

### Secrets GitHub (référencés par workflow)

- [x] `DOKPLOY_API_TOKEN` — Bearer pour `POST /api/compose.redeploy`
- [x] `DOKPLOY_URL` — base URL Dokploy
- [x] `DOKPLOY_NOTIFUSE_PROD_COMPOSE_ID` — composeId prod
- [x] `NOTIFUSE_HUB_API_SECRET_STAGING` — HMAC pour wipe-test-tenants
- [x] `NOTIFUSE_HUB_API_SECRET_PROD` — HMAC pour e2e prod
- [x] `TELEGRAM_BOT_TOKEN` + `TELEGRAM_CHAT_ID` — alertes rollback

---

## ❌ Reste à faire

### 🚨 Bloquant validation 7 jours

- [ ] **App Renovate installée sur le repo** : `.github/renovate.json`
      existe mais le GitHub App Renovate n'est pas configuré sur
      `Christ-Roy/notifuse-veridian`. Action :
      https://github.com/apps/renovate → Install → sélectionner le repo.
      Sans ça, le fichier de config est ignoré.
- [ ] **Vérifier que le pre-push hook bloque vraiment** : faire un test
      réel — touche un fichier critique sans son `_test.go`, tenter
      `git push`, vérifier que c'est refusé. Document le résultat ici.
- [ ] **7 jours consécutifs de pushes sans `--no-verify`** — compte
      démarre 2026-05-13. Cible : 2026-05-20. Critère : zéro contournement
      dans `git log --all --pretty=%H | xargs -I{} git show {} --stat`.

### 🎯 Standard pas encore atteint (Constitution §13)

- [ ] **Trivy multi-scan étage 1** :
      `trivy fs --scanners vuln,secret,misconfig,license --severity CRITICAL,HIGH`
      avec SARIF upload vers GitHub Security tab.
- [ ] **Trivy image scan étage 2** : sur l'image GHCR fraîchement
      buildée, avec `--exit-on-eol 1` et SBOM CycloneDX en artifact.
- [ ] **gitleaks** dans le diff Git (complète Trivy secret scan binaire).
- [ ] **govulncheck** ajouté au job `test-go` (équivalent Go de
      `npm audit` mentionné dans CI-ARCHITECTURE §3 étage 1).
- [ ] **Cron hebdo Trivy** sur les images prod running (CI-ARCHITECTURE
      §5 capacité 9).

### 📦 Migrations DB (Constitution §12)

- [ ] **Script `check-migration-safety.sh`** adapté Go/SQL pour Notifuse :
      bloque `DROP COLUMN`, `DROP TABLE`, `ALTER COLUMN ... SET NOT NULL`
      sur table peuplée, `RENAME COLUMN`, `CREATE INDEX` sans
      `CONCURRENTLY`. Migrations Notifuse sont dans
      `internal/migrations/v*.go` (pas Prisma) — adapter le pattern.
- [ ] **Test backward-compat étage 2** : pull image `:rollback`, apply
      la migration de la PR, lance le container `:rollback` contre le
      schéma N, vérifier que le smoke test passe.

### 🧹 Path-based gates strictes

- [ ] **Gate structurel 24h** : si `Dockerfile`, `go.mod`,
      `docker-compose.yml`, `internal/migrations/**` modifiés → exiger
      que le commit soit déployé sur staging depuis > 24h avant
      `deploy_prod=true`. Actuellement la garde "CI staging verte sur
      ce SHA" couvre 90 % du besoin, manque juste le délai 24h.

### 🏗️ Migration staging vers dev server — ✅ FAIT (session 2026-05-14)

#### Pattern base + overrides docker-compose

- [x] **Architecture source unique** :
      - `infra/compose/base.yml` — services communs (Notifuse + Postgres,
        env partagées, healthchecks, ports internes)
      - `infra/compose/prod.yml` — overrides prod (image digest pinned,
        volumes externes `infra_*`, network `dokploy-network`, labels
        Host `notifuse.app.veridian.site`, ressources 6.5G/3.6 CPU)
      - `infra/compose/staging.yml` — overrides staging (image `:latest`,
        volumes locaux nommés, network `staging-edge`, labels Host
        `notifuse.staging.veridian.site`, ressources 1.5G/1.5 CPU)
      - `infra/compose/docker-compose.staging.yml` — généré, commité
- [x] **Script `scripts/ci/generate-compose.sh`** :
      - Mode normal : génère les consolidés depuis base+override avec
        `docker compose config --no-interpolate --no-path-resolution`
      - Mode `CHECK_ONLY=1` : drift check qui compare le consolidé
        fraîchement généré au fichier commité ; si différent → exit 1 +
        diff précis
      - Bandeau d'avertissement automatique en tête des consolidés
        ("FICHIER GÉNÉRÉ — NE PAS ÉDITER À LA MAIN")
- [x] **Job CI `compose-drift-check`** ubuntu-latest, exécuté avant
      `test-go`. Empêche toute édition manuelle du consolidé.

#### Traefik staging-edge sur dev-pub

- [x] **Traefik standalone déjà installé** sur dev-pub
      (`~/traefik-staging/`, service systemd `traefik-staging.service`,
      network externe `staging-edge`, DNS wildcard
      `*.staging.veridian.site` → 37.187.199.185, Let's Encrypt DNS-01
      Cloudflare).
- [x] **Convention labels** : `traefik.docker.network=staging-edge` +
      ``Host(`notifuse.staging.veridian.site`)`` (cf.
      `~/traefik-staging/README.md` sur dev-pub).

#### Deploy staging via SSH dev-pub (compose pur)

- [x] **Job `deploy-staging` refondu** :
      - `runs-on: [self-hosted, linux, x64]` (runner Notifuse
        `veridian-dev-server-notifuse` sur dev-pub, agentId 21)
      - Checkout repo → copie `base.yml`+`staging.yml` vers
        `/opt/veridian/staging/notifuse/infra/compose/` (sans sudo,
        dossier chown ubuntu:ubuntu)
      - `docker compose -f base.yml -f staging.yml -p notifuse-staging
        --env-file .env pull && up -d --force-recreate notifuse`
      - Wait healthy via `/api/setup.status` (HTTP 200)
      - Wipe test tenants HMAC sur
        `notifuse.staging.veridian.site/api/veridian/admin/wipe-test-tenants`
      - Cleanup `always()` final : prune containers/images/volumes + cache
        BuildKit > 7j + `/tmp/runner-*` + alerte si disk > 80%
- [x] **`/opt/veridian/staging/notifuse/.env` créé** sur dev-pub
      (chmod 600, ubuntu:ubuntu) avec POSTGRES_PASSWORD propre staging,
      NOTIFUSE_SECRET_KEY propre, SMTP Brevo, hub secrets prod (à splitter
      si staging hub différent un jour).
- [x] **GHCR login sur dev-pub** : `gh auth token | docker login ghcr.io
      -u Christ-Roy --password-stdin` — `gh` CLI déjà authentifié.

#### Premier deploy validé en réel (2026-05-14)

- [x] **Containers staging UP & healthy** :
      - `notifuse-staging` (image `ghcr.io/christ-roy/notifuse-veridian:latest`)
      - `notifuse-staging-db` (postgres:17-alpine)
- [x] **App démarrée correctement** — logs :
      `"Server starting on 0.0.0.0:8081 with API endpoint:
      https://notifuse.staging.veridian.site"` +
      `"Application successfully initialized"`
- [x] **Endpoint public répond** :
      `curl https://notifuse.staging.veridian.site/api/setup.status` →
      **HTTP 200 en 163ms** (HTTP/2, cert Let's Encrypt valide,
      Traefik route correct).
- [x] **URLs mises à jour** dans workflow + CLAUDE.md + todo/CI.md.

#### Cleanup runner self-hosted (Constitution §20)

- [x] Step `Cleanup runner (always)` ajouté sur les 3 jobs self-hosted :
      `build`, `deploy-staging`, `e2e-staging`.
- [x] Prune containers + images > 1h + volumes (sauf `label=keep=true`) +
      cache BuildKit > 7j (`--keep-storage 10GB`) +
      `/tmp/runner-${run_id}-*` + alerte disk > 80%.

### 🏗️ Reste à faire côté staging

- [ ] **Ephemeral staging per PR** (CI-ARCHITECTURE §9) : workflow
      `staging-ephemeral.yml` qui spawn une stack par branche feature
      à l'open PR (URL `notifuse-<branch-slug>.staging.veridian.site`),
      teardown au merge/close. Aujourd'hui staging = 1 seule stack qui
      suit la branche `veridian`.
- [ ] **GC hebdo stacks orphelines** : cron `staging-gc.sh` sur dev-pub
      (peu de risque tant qu'on n'a pas les éphémères par PR).
- [ ] **Snapshot prod anonymisé restauré** à chaque deploy staging
      branche `veridian` (cron nightly sur prod + script anonymizer
      dans veridian-infra).
### ✅ Flow prod complet (session 2026-05-14)

- [x] **Audit fin prod réalisé** (`todo/CI.md` ne contient pas les valeurs,
      cf. logs session) : container Up 21h sans restart, RAM 49 MiB /
      6.5 GiB, latence 142-148ms (1 pic isolé 1.1s), 0 erreur 24h,
      setup.status ok, 12 tenants actifs, DB 8.6 MB système.
- [x] **`generate-compose.sh` étendu prod** : génère
      `docker-compose.prod.yml` consolidé depuis `base.yml + prod.yml`,
      avec drift check côté CI sur `compose-drift-check`.
- [x] **Job `promote-prod-compose`** ajouté en fin de workflow,
      `workflow_dispatch + promote_prod_compose=true` uniquement. Garde
      e2e-staging green sur SHA → régénère + drift check → git clone
      `notifuse-deploy`, copie `docker-compose.yml`, commit, push → webhook
      Dokploy → wait healthy → Telegram succès/échec.
- [x] **Procédure pré-prod documentée** dans `CLAUDE.md` section
      "Promotion vers prod" (2 flows : image only, ou image + compose).
- [x] **Diff sémantique consolidé vs compose live identifié** :
      changement de service name `notifuse-prod-db` → `notifuse-db` (notre
      version) → impacte `DB_HOST` interne. Container_name préservé.
      Downtime court attendu lors du premier promote-prod-compose.

#### Pré-requis à activer avant premier promote-prod-compose

- [ ] Créer PAT GitHub scope `repo` sur `Christ-Roy/notifuse-deploy`
- [ ] Ajouter en secret `NOTIFUSE_DEPLOY_PUSH_PAT` sur ce repo
- [ ] Choisir une fenêtre calme pour le premier passage (downtime
      attendu ~30-60s recreate des 2 containers)
- [ ] Vérifier que la connectivité hub→notifuse reprend bien après
      recreate (les hub.app.veridian.site routes doivent toujours répondre)

### 🔔 Annotations Grafana (Constitution §9)

- [ ] **`obs annotate deploy`** câblé dans `deploy-staging` (après
      smoke OK) et `deploy-prod` (après wait healthy).
- [ ] **`obs annotate rollback`** câblé dans le job `rollback`.
- [ ] **`obs annotate migrate`** avant + après migration DB.

### 🚦 Defense in depth (CI-ARCHITECTURE §17)

- [ ] **Workflow `emergency-revert.yml`** : tout auto-rollback Docker
      déclenche un revert Git auto + freeze main jusqu'à merge revert.
- [ ] **Webhook Grafana → `repository_dispatch`** : alertes
      `oom_killed`, `memory_creep`, `synthetic_failed_3x` câblées sur
      `emergency-rollback.yml`.
- [ ] **Cleanup runner self-hosted obligatoire** : step `always()` qui
      prune containers, images, volumes, cache BuildKit > 7j, dossiers
      `/tmp`. Lint workflow YAML rejette les jobs self-hosted sans ce
      step.

### 🐛 Bugs / dettes connus

- [ ] **Dokploy API `compose.all` / `compose.one` renvoient 404**
      (testé 2026-05-14 avec `DOKPLOY_API_KEY` de `.all-creds.env`).
      Soit l'API key n'a pas le scope `compose:read`, soit l'endpoint
      a changé. À investiguer côté Dokploy avant de pouvoir scripter
      des inspect compose côté CI.
- [ ] **`tests-pending.txt` baseline = 12 fichiers** — réduire à 0
      sous 90 jours. Audit hebdo par cron + issue GitHub auto
      (à câbler).
- [ ] **`test-coverage-map.yaml` vide** — peupler au fil des PRs où
      un fichier critique est légitimement couvert par un test
      d'intégration ailleurs.

---

## 🧪 Comment tester la CI staging via Chrome MCP

Pour tester staging à la main sans qu'un push parallèle wipe tes données
ou qu'un Playwright auto bloque l'UI :

1. GitHub UI → Actions → "Notifuse Veridian CI/CD" → Run workflow
2. Cocher `skip_wipe=true` ET `skip_e2e=true`
3. Lancer → build + push image GHCR + redeploy staging, mais aucun wipe,
   aucun Playwright
4. Tester via Chrome MCP sur `https://notifuse.staging.veridian.site`
   à ton rythme
5. Pour reprendre la CI normale : push un commit sur `veridian` (sans
   inputs) → tout repart en mode auto

---

## 📍 Repères

- **Workflow** : `.github/workflows/veridian-ci.yml`
- **Script mapping** : `scripts/ci/check-test-mapping.sh`
- **Hook** : `.husky/pre-push`
- **Dette** : `tests-pending.txt`
- **Couverture non-canonique** : `test-coverage-map.yaml`
- **Renovate** : `.github/renovate.json`
- **Constitution** : `CLAUDE.md` section "Constitution CI"
- **Standard global** : `../../CI-ARCHITECTURE.md`
