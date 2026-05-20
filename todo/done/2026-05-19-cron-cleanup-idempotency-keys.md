# 2026-05-19 — Brancher cron cleanup `veridian_idempotency_keys.DeleteExpired`

> **Sévérité** : 🟢 P3 (dette légère — pas urgent court terme)
> **Effort** : S (<2h)
> **Découvert pendant** : ship ticket idempotency-key-header (sec. 5.11)

## Contexte

Le commit `20689427` a ajouté le middleware Idempotency-Key et la table `veridian_idempotency_keys` (migration V35). La méthode `DeleteExpired(ctx) (int64, error)` est implémentée et testée (`internal/repository/veridian_idempotency_postgres.go:96`) mais **aucun cron ne l'appelle**.

## Conséquence

La table grossit sans limite. Aujourd'hui c'est acceptable car le Hub n'envoie pas encore le header `Idempotency-Key` (le middleware est passthrough quand pas de header), donc 0 lignes insérées en prod. Mais dès que le Hub branchera le header, la table grossira ~1MB/jour si ~10k requêtes/jour, soit ~30MB/mois. Sans purge, atteindra 1GB en 3 ans.

Pas critique court terme mais à brancher avant le rollout massif du header côté Hub.

## Travail

1. Identifier où Notifuse expose un système de cron (probablement `internal/service/cron_*.go` ou un job runner upstream).
2. Brancher un job quotidien (4h UTC, hors pic de charge) qui appelle `idempotencyRepo.DeleteExpired(ctx)` et log le count supprimé.
3. Ajouter à la liste des cron-jobs documentée dans `CLAUDE.md` ou ailleurs.

Alternative simple : un trigger PostgreSQL `BEFORE INSERT` qui delete les expired au passage. Moins propre mais zéro nouveau code Go.

## Tests

Test unitaire du job (mock du repo) + smoke test qu'il tourne 1×/jour.

## Lien

- Implémentation idempotency : commit `20689427`
- Memory : [[project_contrat_hub_v13_audit]] mentionne ce ticket dans "Dette détectée"

---

## Update — 2026-05-20 — Livré (commit 9fd0379b)

`VeridianIdempotencyCleanupService` créé dans `internal/service/
veridian_idempotency_cleanup.go` + 5 tests colocalisés. Câblé depuis
`internal/app/app.go` (Start après telemetryService).

Pattern goroutine + 24h ticker (identique TelemetryService). Best-effort
sur erreur (log et continue). Stop propre via ctx shutdown.

À déplacer vers `todo/done/` après confirmation log "VeridianIdempotency
Cleanup: purged expired entries" visible en prod (24h après deploy).
