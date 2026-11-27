# Commandes tests (Makefile)

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

## Modules `internal/veridian/` sur un vrai PostgreSQL (22/11/2026)

Leurs tests SQL passent par `testpg.DSN(t, "<variable propre>")` (paquet `internal/veridian/testpg`) :
variable propre au module, sinon `VERIDIAN_TEST_POSTGRES_DSN`. Sans base, le test est ignoré sur un
poste, et **échoue** en CI (`VERIDIAN_TEST_REQUIRE_PG=1`, job `test-go`). Avant cette date, la boîte
unifiée et la politique lisaient des variables que personne ne posait : leurs tests étaient ignorés
partout. Tout nouveau test PostgreSQL passe par `testpg.DSN`, jamais par un `os.Getenv` + `t.Skip`.

```bash
# base jetable sur le bastion : le nom doit matcher notifuse-go-* (conteneurs déclarés), sinon
# control-plane-audit l'arrête en silence sous 5 minutes
docker run --rm -d --name notifuse-go-pg-$USER -e POSTGRES_PASSWORD=jetable -p 127.0.0.1:55439:5432 postgres:17-alpine
VERIDIAN_TEST_REQUIRE_PG=1 VERIDIAN_TEST_POSTGRES_DSN="postgres://postgres:jetable@127.0.0.1:55439/postgres?sslmode=disable" \
  nice -n 19 go test -count=1 -p 1 ./internal/veridian/...
docker stop notifuse-go-pg-$USER
```

---

## Pre-push (26/11/2026)

Le pre-push lance les tests Go (vet + test) des paquets que le push modifie, et le typecheck E2E si `tests/e2e-veridian`
bouge, par `scripts/ci/lourd-ou-local.sh` : `lourd-si-bastion` sur le bastion (rise ou node02, nice 19), sur place ailleurs.
Si lourd n'a rien fait tourner (NON ABOUTI), l'étape est relancée ici en `nice -n 19` : un « non abouti » ne laisse jamais
passer. i18n reste local (il réécrit les `.po` à commiter). Prouvé : un test cassé exprès dans `mxgate` fait refuser le push
(FAIL lu sur rise), le vert passe. Les suites complètes à la main : `lourd --go go test ./...`, jamais en direct sur le bastion.

Depuis le 27/11 : `scripts/ci/prepush-console-tests.sh` joue par lourd (`npm ci` puis `vitest run`) les tests console du push et
ceux qui importent un fichier `console/src` modifié (choix par `grep` des imports ; `vitest related` bute sur les `.po` faute du
plugin Lingui dans `vitest.config.ts`). Prouvé : `installLinksLang` cassé exprès dans la source fait rouge (`&lang=en` attendu),
la source saine passe (9 tests, ~30 s avec `npm ci`). Un fichier sans test lié passe ici : le mapping source↔test en juge.

i18n au pre-push (27/11) : `check-i18n-extracted.sh` sans `console/node_modules` (le bastion) extrait par lourd et applique
le diff des `.po` ici ; il exige aussi un fr.po sans `msgstr` vide. Avant, il sortait toujours « non abouti » sur le bastion.
