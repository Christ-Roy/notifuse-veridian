# Conventions clés (résumé exécutable)

- **Architecture** : Clean Architecture (domain → service → repository → http). DI par constructor.
- **DB** : Postgres 17, query builder Squirrel, migrations versionnées (`config.VERSION`, `internal/migrations/vN.go` implémentant `MajorMigrationInterface`). Idempotent (`IF NOT EXISTS`), transactionnel, additif.
- **API** : RPC-style `POST /api/<resource>.<verb>`. JWT auth, middleware permissions.
- **Erreurs HTTP** : un handler rend ses erreurs par `WriteAuthAwareError` (ou `WriteAuthAwareErrorCode`), jamais un 500 nu. Ce chemin unique classe 401/403 (`classifyAuthError`) et, depuis le 26/11/2026, l'id déjà pris (PostgreSQL 23505) en **409 `already_exists`** avec `details.resource` = table (`veridian_conflict_error.go`). Une erreur de dépôt s'enveloppe en `%w`, sinon la classification ne la voit pas.
- **Tests Go** : table-driven, Testify (`assert`/`require`/`mock`), GoMock v1.6.0 (`github.com/golang/mock`, **pas** `go.uber.org/mock`), `go-sqlmock` pour la DB.
- **Front console** : React 18 + TS strict + Vite + Ant Design + TanStack Query/Router + Lingui i18n (`useLingui()` + `` t`...` ``).
- **Plans** : si AI-assisted plan, écrire dans `plans/` (kebab-case), inclure stratégie de test + commande `make test-*` à lancer.

Pour le tech stack complet (versions précises, libs front, observabilité, etc.) : lire le `README.md` ou la doc upstream Notifuse.

---

