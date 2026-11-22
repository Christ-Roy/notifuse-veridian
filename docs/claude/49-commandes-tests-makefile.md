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
