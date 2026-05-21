# notifuse-veridian — fork Veridian de Notifuse

> Fork du projet [Notifuse](https://github.com/Notifuse/notifuse). Sync régulière
> via `git pull upstream main` sur la branche `veridian`. Tech stack upstream
> (Go 1.25 + Postgres 17 + React 18 + Vite + Ant Design) : voir le `README.md`
> et la doc Notifuse amont — pas reprise ici pour pas polluer le contexte agent.

---

## Customizations Veridian

### Branche et workflow

- **Branche de travail** : `veridian` (push direct OK, CI obligatoire)
- **Branche upstream tracking** : `main` (sync depuis `upstream/main`, jamais de commit Veridian direct)
- **Tags** : `v<upstream>-veridian.<sha8>` générés par CI (`veridian-ci.yml`)

### Convention `veridian_*.go` flat

Tout code custom dans `internal/{http,service,repository,domain}/` est préfixé
`veridian_*.go` au **même niveau** que les fichiers upstream — pas de
sous-dossier `internal/http/veridian/`.

Raisons : idiomatique Go (packages plats par responsabilité), pas d'import
cycle, grep-friendly (`ls internal/http/veridian_*`), sync upstream triviale
(aucun fichier upstream ne porte ce préfixe).

Règle stricte : **ne jamais patcher un fichier upstream** pour les besoins
Veridian. Si un handler upstream doit être étendu, créer
`veridian_<nom>_handler.go` qui wraps/remplace, et router dessus depuis le mux
Veridian.

Exemples existants : `internal/http/veridian_handler.go`,
`veridian_autologin_handler.go`, `veridian_magic_handler.go`,
`internal/domain/veridian.go`, `veridian_token.go`.

### Sync upstream

```bash
git checkout main && git pull upstream main
git checkout veridian && git merge main
```

Pre-push hook détecte les commits dont l'auteur est `@notifuse.com` et bypasse
le mapping 1-pour-1 sur les fichiers **non-veridian_***. Les fichiers
`veridian_*.go` restent sous discipline stricte même dans un sync upstream.

### Dokploy + GHCR

- **Image** : `ghcr.io/christ-roy/notifuse-veridian:<tag>`
- **Compose staging** : `compose-bypass-bluetooth-feed-tbayqr`
- **Compose prod** : secret `DOKPLOY_NOTIFUSE_PROD_COMPOSE_ID`
- **Endpoints** : staging `notifuse.staging.veridian.site`, prod `notifuse.app.veridian.site`

### Tests E2E Veridian

`tests/e2e-veridian/` (Playwright). Tag `@prod-safe` pour les tests
read-only autorisés à tourner sur prod.

---

## Pricing — source de vérité

> **Source unique cross-app** : `../veridian-hub/docs/PRICING-VERIDIAN.md`.
> Lire ce doc avant tout travail pricing/trial/paywall/feature gate.

**Philosophie figée par Robert 2026-05-21** : générosité maximale, **tout
illimité partout y compris Free** (emails, contacts, OAuth BYO, automation,
seats, custom domains, A/B testing, historique). L'app ne doit **jamais** être
défigurée par des limites visibles ou murs béton.

**Seules différenciations** :
- Free → durée 15j visibles (révélée à J+2 après 5 mails envoyés) puis paywall
- Business 99€ vs Pro 29€ → white-label custom (footer client custom)

**Flow trial** : signup silencieux → 5 mails déclenchent timer 2j serveur
invisible → J+2 bandeau trial 15j → ajout CB = cadeau **inconditionnel** de
30j → débit auto Pro à expiration si CB présente, sinon paywall lecture seule.
Détails complets : doc Hub.

### Interdits côté code

- Mur béton `402 Payment Required` sur une feature
- Compteur visible "il vous reste X mails / contacts / domaines"
- Menu grisé "🔒 Pro", pop-up "passez Pro pour faire ça"
- Branding obligatoire qui dégrade les emails du client
- Toute limite enforced sur contacts / OAuth / seats / automation / historique / custom domains / A/B
- Affichage du timer trial **avant J+2** (le timer 2j post-5mails reste invisible UI)

### Acceptable côté code

- Bandeau trial visible uniquement en phase 4+ (J+2)
- Compte à rebours pendant les 15j (puis 30j si CB)
- Lien Upgrade, paywall lecture seule à expiration
- White-label custom = différenciation Business+ uniquement

### État côté Notifuse (post-pivot 2026-05-21)

- **V37 lots 4b/4c/4d et 5** : tous annulés (aucun enforcement de dimensions)
- **Lot 4a A/B feature gate** : reverté (A/B gratuit pour tous, `featureGatedPaths` vide)
- **DefaultPlanLimits** : tout à `-1` / `true` sauf `FeatureWhiteLabel` (Business+ uniquement)

### Compteurs invisibles (télémétrie interne)

- `emails_sent_lifetime` : signal d'activité
- `activity_threshold_reached_at` (post-5e mail) : timestamp serveur consommé par Hub
- **Jamais exposés UI client** tant que phase 3 (J+2) n'est pas atteinte

### Tickets actifs reliés

- `todo/2026-05-21-trial-eligible-signal.md` (signal 5 mails Notifuse→Hub)
- `todo/2026-05-21-paywall-degraded-mode-soft-deleted.md` (UX dégradée)
- `todo/2026-05-20-pricing-plans-implementation.md` (V37)
- Hub : `2026-05-21-trial-state-machine.md`, `2026-05-21-stripe-webhook-orchestrator.md`

---

## Constitution CI — règles non négociables

Standard de référence : `../CI-ARCHITECTURE.md` (racine `veridian-platform/`).
Adaptations Go pour ce repo :

1. **1 fichier critique = 1 test colocalisé.** Scopes :
   `internal/{http,service,repository,domain}/**/*.go`. Convention Go :
   `foo.go` ↔ `foo_test.go` au même niveau. Zéro exception.
2. **Pre-push hook bloquant** via `.husky/pre-push` →
   `scripts/ci/check-test-mapping.sh`. Setup : `make setup-hooks`.
3. **Jamais `--no-verify`.** Si le hook bloque : fix le test ou ajoute à
   `tests-pending.txt` (dette tracée).
4. **Règle 1-pour-1 stricte** : chaque nouvelle func exportée dans un fichier
   critique = au moins un nouveau `TestXxx` dans le test colocalisé.
5. **`tests-pending.txt`** : baseline transitoire, cible 0 sous 90 jours. Toute
   ligne retirée doit s'accompagner du test.
6. **`test-coverage-map.yaml`** : si un fichier est légitimement couvert
   ailleurs, déclarer avec `reason:` explicite + `covered_by`.
7. **Exception upstream sync** : auteur `@notifuse.com` bypasse le mapping
   pour les fichiers **non-veridian_***. Les `veridian_*.go` restent sous
   discipline stricte.
8. **Skip docs** : `paths-ignore: ['**.md', 'docs/**', 'runbooks/**', 'plans/**']`.
9. **Staging gate path-based** : si `Dockerfile`, `go.mod`, `docker-compose.yml`,
   `internal/migrations/**` modifiés → staging vert + e2e-staging vert exigés
   avant prod. Prod = `workflow_dispatch` manuel uniquement (clients réels).
10. **Deploy via Dokploy API** : `POST /api/compose.redeploy` (Bearer).
    Secrets : `DOKPLOY_URL`, `DOKPLOY_NOTIFUSE_PROD_COMPOSE_ID`. Rotation 6 mois.
11. **Rollback prod auto** sur e2e-prod fail : `:rollback` retagé avant chaque
    `:latest`. Fail → retag broken-<sha> + restore `:rollback` → redeploy →
    Telegram alert.
12. **Migrations Expand & Contract obligatoire**. Le tag Docker précédent doit
    tourner sur le schéma actuel. Versions majeures (V6, V7…) additives ;
    DROP COLUMN / NOT NULL sur table peuplée = 2 PRs sur 2 deploys.
13. **Findings GitHub Security tab** : Trivy + gitleaks SARIF via
    `upload-sarif@v3`. Pas de `.trivyignore` sans VEX écrit.
14. **Renovate auto-merge** (à activer) : patch + minor + CVE auto-mergés si
    CI verte. Major → review humaine.

### Commandes CI utiles

```bash
make setup-hooks                                 # one-time, installe pre-push
BASE_REF=HEAD scripts/ci/check-test-mapping.sh   # test local working tree
BASE_REF=origin/main scripts/ci/check-test-mapping.sh  # comme pre-push
wc -l tests-pending.txt                          # voir la dette
git ls-files | grep -E 'veridian_|veridian\.go'  # lister fichiers custom
```

---

## Commandes tests (Makefile)

```bash
make test-unit          # tous les tests unit
make test-domain        # domain layer
make test-service       # service layer
make test-repo          # repository
make test-http          # HTTP handlers
make test-migrations    # migrations
make test-integration   # intégration full stack
make coverage           # rapport HTML
cd console && npm test  # frontend
```

---

## Conventions clés (résumé exécutable)

- **Architecture** : Clean Architecture (domain → service → repository → http). DI par constructor.
- **DB** : Postgres 17, query builder Squirrel, migrations versionnées (`config.VERSION`, `internal/migrations/vN.go` implémentant `MajorMigrationInterface`). Idempotent (`IF NOT EXISTS`), transactionnel, additif.
- **API** : RPC-style `POST /api/<resource>.<verb>`. JWT auth, middleware permissions.
- **Tests Go** : table-driven, Testify (`assert`/`require`/`mock`), GoMock v1.6.0 (`github.com/golang/mock`, **pas** `go.uber.org/mock`), `go-sqlmock` pour la DB.
- **Front console** : React 18 + TS strict + Vite + Ant Design + TanStack Query/Router + Lingui i18n (`useLingui()` + `` t`...` ``).
- **Plans** : si AI-assisted plan, écrire dans `plans/` (kebab-case), inclure stratégie de test + commande `make test-*` à lancer.

Pour le tech stack complet (versions précises, libs front, observabilité, etc.) : lire le `README.md` ou la doc upstream Notifuse.

---

## Claude Agent Rules

- Pas d'auto-attribution : aucune mention "Generated with Claude", "Co-Authored-By: Claude", AI signatures dans commits / releases / PRs / code.
