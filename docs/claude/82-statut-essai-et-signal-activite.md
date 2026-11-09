# Statut d'essai du Hub et signal d'activité (08/11/2026)

`hub reconcile` (notWired) relevait deux trous côté Notifuse. Contrat : CONTRAT-BILLING v2 §3 et CONTRAT-HUB §7.

## 1. Statut d'essai : stocké et rendu, jamais appliqué
- `POST /api/tenants/update-plan` reçoit `trial_status` (`none|trial_active|expired|paid`) et `trial_ends_at`
  (ISO 8601 ou null) avec le plan. Hors enum ou date illisible : 400 `invalid_payload`. Colonnes V72
  `veridian_plan.trial_status` / `trial_ends_at` (nullables, aussi dans le schéma de base). Écrit AVANT la garde
  d'immunité : un plan offert reste protégé (409 `plan_locked`) mais le statut d'essai est enregistré.
- `GET|POST /api/users/by-email` rend par workspace `trial_status` et `trial_ends_at`, clés TOUJOURS présentes
  (null si jamais reçu) : le Hub s'en sert pour savoir que l'app rend le statut. Un tenant jamais repoussé par le
  Hub rend null : `trial_mismatch` dans reconcile jusqu'à une passe update-plan du Hub.
- Lecture seule : drapeau `VERIDIAN_TRIAL_READONLY_ENFORCED`, FAUX par défaut, décision de Robert. Aucun code ne
  l'applique : lever le drapeau ne fait rien tant que l'application n'est pas écrite.
- Code : `internal/veridian/trial` (stockage), `veridianService.UpdatePlan` et `LookupByEmail`.

## Preuve en prod (09/11/2026)

Workspace jetable provisionné par le Hub (e2essoessai), 5 mails réels transactionnels (relais Gmail : la garde réseau refuse le relais interne pour un workspace client) : `emails_sent_lifetime`=5, `tenant.activity_threshold_reached` livré 200, entrée eligible au Hub, essai démarré, plan pro posé par update-plan. Nettoyé ensuite. Aucun rejeu pour les 4 workspaces déjà au-delà du seuil. La ligne « signal réel » du reconcile attend un vrai premier tenant.
