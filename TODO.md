# TODO

---

## 🚀 Sprint P0 Veridian — Standard CI (2026-05-13) — cas particulier Notifuse

> Brief complet : [`~/Bureau/SPRINT-GITOPS-VERIDIAN.md`](../SPRINT-GITOPS-VERIDIAN.md) section P0.
> Standard de référence : `../CI-ARCHITECTURE.md` (racine `veridian-platform/`).
>
> **⚠️ Notifuse est un fork upstream open-source.** La règle Veridian s'applique
> uniquement au code custom Veridian, pas au code upstream. Voir subtilité ci-dessous.

### Subtilité Notifuse

Notifuse est écrit en **Go** (pas TypeScript), suit la convention idiomatique Go
(`<file>.go` + `<file>_test.go` côte à côte), et reçoit régulièrement des sync
depuis l'upstream open-source.

**⚠️ Correction structure (2026-05-13)** : la structure réelle des handlers HTTP
est `internal/http/` (PAS `internal/handlers/` comme indiqué dans la rédaction
initiale du TODO). Le script et le hook sont déjà alignés sur la struct réelle.

**Règle adaptée** :

1. **Mapping source↔test obligatoire** sur 4 scopes critiques :
   - `internal/http/**/*.go` (handlers HTTP)
   - `internal/service/**/*.go` (business logic)
   - `internal/repository/**/*.go` (data access)
   - `internal/domain/**/*.go` (domain models avec methods)

   Tout `foo.go` modifié doit avoir son `foo_test.go` modifié dans la même PR
   (convention Go idiomatique, fichiers côte à côte au même niveau).

2. **Règle 1-pour-1 stricte** : chaque nouvelle func exportée (majuscule initiale,
   y compris receivers) doit s'accompagner d'au moins un nouveau `TestXxx` dans le
   test colocalisé. Script compte les `^+func [A-Z]...` vs `^+func Test[A-Z]...`.

3. **Exception upstream sync** : commits dont l'auteur correspond à l'upstream
   (`Notifuse <*@notifuse.com>` ou tout auteur upstream identifié) bypassent le
   check. Le hook vérifie l'auteur du dernier commit avant d'exiger les tests.

4. **Code custom Veridian** : convention **préfixe `veridian_*.go` en flat**
   (PAS de sous-dossier `internal/http/veridian/`). Raisons :
   - Idiomatique Go : packages plats par responsabilité technique, pas par origine.
   - Pas d'import cycle entre sous-packages http/veridian et http.
   - Grep-friendly pour audit : `ls internal/http/veridian_*` ou
     `git diff upstream/main -- internal/http/ ':!internal/http/veridian_*'`.
   - Déjà appliqué : `veridian_handler.go`, `veridian_autologin_handler.go`,
     `veridian_magic_handler.go`.

5. **Pre-push hook Go** : `scripts/ci/check-test-mapping.sh` filtre les diffs
   Go, exécute le mapping 1-pour-1 + check `tests-pending.txt` + check
   `test-coverage-map.yaml` pour les couvertures non-canoniques.

### Statut P0 Notifuse (2026-05-13)

#### ✅ Fait

- [x] `scripts/ci/check-test-mapping.sh` versionné (Go variant, foo.go ↔ foo_test.go).
- [x] `.husky/pre-push` installé, exécute le script avec `BASE_REF=origin/<branch>`.
- [x] `tests-pending.txt` baseline générée — 12 fichiers veridian-custom orphelins :
      - `internal/domain/{auth,blog_feed,contact_segment_queue,ses_client,telemetry,veridian,veridian_token}.go`
      - `internal/http/veridian_{autologin,handler,magic}_handler.go`
      - `internal/repository/workspace_postgres.go`
      - `internal/service/llm_service_openai.go`
- [x] `test-coverage-map.yaml` créé (vide initialement, à peupler au fil des PRs).
- [x] Workflow `.github/workflows/veridian-ci.yml` complet : test-go + build self-hosted
      + deploy-staging Dokploy + e2e-staging Playwright + deploy-prod (workflow_dispatch only)
      + e2e-prod + rollback auto sur fail.
- [x] Convention `veridian_*.go` flat appliquée pour le code custom.
- [x] Bearer Dokploy câblé : secrets `DOKPLOY_API_TOKEN`, `DOKPLOY_URL`,
      `DOKPLOY_NOTIFUSE_PROD_COMPOSE_ID` référencés dans le workflow.
- [x] Rollback prod auto : retag `:rollback` avant push, redeploy auto si e2e-prod fail.

#### ✅ Fait (suite, session 2026-05-13)

- [x] **Bypass upstream dans `check-test-mapping.sh`** : détection auteur via
      `git log --pretty=%ae $BASE_REF...HEAD`, regex
      `@notifuse\.com$|^pierre@bazoge\.com$|^pierre@(Air-de-Pierre|Host-[0-9]+)\.lan$`.
      Si **tous** les commits du diff sont upstream → skip mapping sur fichiers
      non-`veridian_*`. Les `veridian_*.go` restent sous discipline.
- [x] **`make setup-hooks`** : target ajoutée + `make check-test-mapping`.
      Setup local : `chmod +x .husky/pre-push scripts/ci/check-test-mapping.sh` +
      `git config core.hooksPath .husky`.
- [x] **Path-based skip docs** : `paths-ignore: ['**.md', 'docs/**',
      'runbooks/**', 'plans/**', 'TODO.md', 'tests-pending.txt']` sur push + PR.
- [x] **Job CI `test-mapping`** : rejoue `check-test-mapping.sh` sur ubuntu-latest
      avant `test-go` (defense in depth §1, attrape Renovate/PRs externes).
- [x] **Section "Veridian customizations" + "Constitution CI" dans CLAUDE.md
      racine** : convention `veridian_*.go` flat documentée (4 raisons), sync
      upstream documenté, 15 règles non négociables.
- [x] **`.github/renovate.json`** (Renovate, pas Dependabot — cohérent avec
      CI-ARCHITECTURE §10) : auto-merge total patch+minor+major+CVE, group gomod,
      group github-actions, group frontend deps, lockFileMaintenance auto-merged.
      Zéro `needs-human-review` (cf. Constitution §A1 : pas de review humaine).
- [x] **GitHub Environments créés** sur `Christ-Roy/notifuse-veridian` :
      - `staging` (id 15309935895) : pas de protection rule
      - `production` (id 15309933105) : branch policy = `veridian` + tag
        `v*-veridian.*`
- [x] **Workflow `veridian-ci.yml` câblé sur Environments** :
      `deploy-staging.environment: { name: staging, url: ... }` et
      `deploy-prod.environment: { name: production, url: ... }`.

#### ❌ Reste à faire

- [ ] **Path-based staging gate strict 24h** : actuellement `deploy-prod` est
      `workflow_dispatch` manuel uniquement (équivalent fonctionnel). Pour
      formaliser la règle 24h : ajouter un step qui check via API GitHub que
      le commit a été déployé sur staging il y a > 24h. Pas urgent vu que prod
      est déjà 100% manuel.
- [ ] **Trivy multi-scan étage 1** (Constitution CI §13) : Trivy fs + image
      + SARIF upload vers GitHub Security tab. Pas câblé dans `veridian-ci.yml`.
- [ ] **Validation : 7 jours consécutifs de pushes sans `--no-verify`** —
      compte démarre maintenant (2026-05-13). Cible : 2026-05-20.
- [ ] **App Renovate installée sur le repo** : créer/installer le GitHub App
      Renovate (https://github.com/apps/renovate) avec accès à
      `Christ-Roy/notifuse-veridian` pour que `.github/renovate.json` soit
      effectivement exécuté.

### Pré-requis externes (hors Notifuse, bloquent la validation)

- [x] Standard officiel rédigé : `../CI-ARCHITECTURE.md` (1206 lignes)
- [x] Route `/api/compose.deploy` exposée Traefik avec Bearer token (déjà actif :
      le workflow l'appelle pour `deploy-staging` ET `rollback`)
- [ ] Pas de workflow réutilisable partagé : Notifuse garde son propre
      `veridian-ci.yml` (Go ≠ Next.js, le `_app-ci.yml` ne s'applique pas).

---

## Marketing

- create notifuse blog
- pages on homepage for:

  - rich contact profiles with custom events + segments
  - newsletter campaigns
  - transactional API
  - blog posts

- send newsletter to all contacts+previous customers
- post supabase on twitter and facebook
- page vs mailerlite
- page vs mautic

- https://docs.dify.ai/en/develop-plugin/getting-started/getting-started-dify-plugin
- n8N

## Eventual features

- server settings panel for root user
- better design for system email (use MJML for template)
- add contact_list reason string

## Roadmap

- check for updates + newsletter box
