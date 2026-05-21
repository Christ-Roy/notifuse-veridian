# 2026-05-19 — Audit conformité CI-ARCHITECTURE.md

> **Source** : audit complet `../CI-ARCHITECTURE.md` (1206 lignes, Constitution v15+).
> **Priorité globale** : 🟡 P2 (la CI marche, le pipeline GitOps est stable — ce sont des durcissements)

## Conformité globale

Conformité actuelle : **~80%**. Le noyau dur Constitution (§1 mapping tests, §3 no `--no-verify`, §7 Renovate, §8 path-based skip docs, §10 Dokploy API, §11 rollback auto, §12 migrations, §13 Trivy SARIF, §15 upstream-bypass) est respecté. Les écarts sont sur le périmètre étendu (monitoring synthétique, flaky detection, ephemeral staging).

## Tickets séparés

| Ticket | Sévérité | Effort |
|---|---|---|
| `2026-05-19-ci-migration-safety-non-cable.md` | P1 | S |
| `2026-05-19-ci-paths-ignore-docs.md` | P2 | S |
| `2026-05-19-ci-trivy-allowlist-yaml.md` | P2 | S |
| `2026-05-19-ci-playwright-retries-flaky.md` | P2 | M |
| `2026-05-19-ci-freeze-main-rollback.md` | P2 | M |
| `2026-05-19-ci-synthetic-monitoring.md` | P3 | M |
| `2026-05-19-ci-ephemeral-staging.md` | P3 | L |
| `2026-05-19-ci-tests-pending-debt-tracker.md` | P3 | S |

## Faux positifs filtrés (déjà OK)

- **Branche `main` existe** (`origin/main` tracké) — agent disait absent
- **Cleanup `if: always()` partout** : ligne 239, 267, 421, 457, 636, 672, 877, 936 du workflow — agent disait manquant sur e2e-staging/e2e-prod
- **`check-test-mapping.sh` + pre-push hook + upstream bypass** : OK
- **Renovate auto-merge total** : OK (.github/renovate.json)
- **Path-based staging gate 24h** : OK (workflow ligne 712-746)
- **Trivy SARIF upload** : OK
- **Migrations Expand & Contract** : pattern respecté côté Notifuse (`internal/migrations/v*.go` additif)

## Notes

Notifuse est **100% Go backend**. La Constitution CI §2 (Pyramide tests Next.js) ne s'applique pas — le repo n'a pas de Next.js routing. La console et le widget React sont upstream avec leurs propres tests Vitest.
