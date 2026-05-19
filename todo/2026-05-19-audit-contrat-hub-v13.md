# 2026-05-19 — Audit conformité CONTRAT-HUB v1.3

> **Source** : audit complet `../CONTRAT-HUB.md` (2682 lignes, v1.3) vs code Notifuse actuel (SHA `7a18b16d`).
> **Priorité globale** : 🟡 P1 (le Hub est fonctionnel via les endpoints existants, mais plusieurs blocs v1.1/v1.2/v1.3 ne sont pas implémentés).
> **Demandeur** : Robert (revue contrat 2026-05-19)

## Vue d'ensemble

Le contrat a évolué massivement : v1.1 (lifecycle endpoints), v1.2 (rotation/transfer/quotas/lookup user), v1.3 (multi-membre cross-app). Notifuse implémente le noyau v1.0 (provision/update-plan/suspend/resume/delete/status/health/attach-owner/magic-link/version/mode) mais zéro des extensions v1.1-v1.3.

**Décision Robert demandée** : on shipe en bloc ou on priorise ? Reco : priorité par sévérité (voir tickets séparés par chantier).

## Tickets créés (chantiers séparés)

| Ticket | Sévérité | Effort | Spec contrat |
|---|---|---|---|
| `2026-05-19-lifecycle-soft-delete-restore-purge.md` | P1 | L (1-2j) | §5.7, §5.8 |
| `2026-05-19-idempotency-key-header.md` | P1 sécu | L (1-2j) | §5.11 |
| `2026-05-19-error-format-standardise.md` | P2 | M (2-8h) | §5.10 |
| `2026-05-19-webhooks-manquants.md` | P1 | M (2-8h) | §7.1, §5.8.4 |
| `2026-05-19-plan-source-immunity.md` | P1 | M (2-8h) | §3.3, §5.2 |
| `2026-05-19-paywall-obfuscation-degrade.md` | P1 | L (1-2j) | §5.9 |
| `2026-05-19-rotate-transfer-owner-endpoints.md` | P2 | M (2-8h) | §5.15, §5.16 |
| `2026-05-19-multi-membre-cross-app.md` | P3 | L (1-2j) | §5.18-5.21 |
| `2026-05-19-quotas-au-provision.md` | P3 | S (<2h) | §5.17 |

## Points DEJA OK (faux positifs agent audit)

- `api_key_email` dans `ProvisionResponse` : présent (`domain/veridian.go:123`)
- `idempotency_key` dans payload webhook : présent (`domain/veridian.go:318`) — c'est **émis** par Notifuse, distinct du header **reçu** côté provision (§5.11)
- Branche `main` : existe (`origin/main` tracké)
- `runs-on: self-hosted` jobs ont tous `if: always()` cleanup (workflow ligne 239, 267, 421, 457, 636, 672, 877, 936)

## Coordination Hub

Avant d'attaquer les chantiers P1, vérifier côté Hub :
- Est-ce que le Hub envoie déjà l'`Idempotency-Key` header ? Si non, P1 sécu downgradé en P2.
- Est-ce que le Hub appelle déjà `/api/tenants/{id}/soft-delete` (échec 404 actuel) ou utilise le DELETE existant ? Si DELETE, P1 lifecycle downgradé en P2.
- Quelles features v1.3 (multi-membre) le Hub prévoit d'activer concrètement ?

Si Hub n'envoie rien de v1.1+ aujourd'hui, on a le luxe de prioriser proprement plutôt que de tout shipper en urgence.
