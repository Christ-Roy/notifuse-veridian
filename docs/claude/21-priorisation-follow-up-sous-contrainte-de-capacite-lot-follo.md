# Priorisation follow-up sous contrainte de capacité (Lot FOLLOWUP cold outreach, 2026-06-15)

Quand la capacité quotidienne d'envoi est CONTRAINTE (caps par provider destinataire
`veridian_daily_cap.go`, sending windows, nombre de senders/IP limité en warm-up), le
batch de scheduling (`AutomationExecutor.ProcessBatch`, taille `batchSize`) ne vide pas
tout le dû à chaque tick. Il faut donc PRIORISER : les follow-up dont la fenêtre se ferme
passent AVANT les envois qui peuvent attendre. Roadmap Robert 2026-06-15 (*"contraintes de
date"*). Spec : ticket `todo/2026-06-14-tunnel-vente-ui-controle-et-roadmap.md`.

- **Critère = RETARD** (`overdue = now - ScheduledAt`, décroissant) : le plus en retard sur
  son échéance d'abord. Un J+7 dû depuis 2 jours (prospect qui refroidit, sens de la relance
  qui se périme) prime un J+0 dû à l'instant (peut attendre demain sans perte). L'étape de
  séquence (J+7 > J+3 > J+0) n'est PAS un critère premier : elle est captée par le retard,
  et ça évite de matérialiser le n° d'étape sur `ContactAutomation` (non stocké) — `now -
  ScheduledAt` est dérivable à la lecture, **zéro nouvelle colonne, zéro migration**.
- **Départage stable** : à retard égal → `ScheduledAt` le plus ancien (FIFO, aligné sur le
  `scheduled_at ASC` du repo) → `EnteredAt` le plus ancien → `ID` (déterminisme total).
- **Non-régression hors contrainte** : si le batch absorbe tout le dû (cas nominal faible
  volume), le tri ne change PAS le set traité, seulement l'ordre — et "le plus en retard
  d'abord" reste un FIFO sain sur l'échéance. Le tri ne tranche QUE quand on doit couper au
  `limit`, et alors il coupe les moins urgents.
- **Couche scheduling, pas persistance** : tri PUR appliqué dans `ProcessBatch` APRÈS le
  fetch round-robin (qui garde l'anti-starvation inter-workspace), AVANT la boucle `Execute`.
  Le repo `GetScheduledContactAutomationsGlobal` (upstream-pur) n'est PAS touché.
- **Fichier veridian** : `internal/service/veridian_followup_prioritizer.go`
  (`veridianPrioritizeFollowups(items, now)` tri en place + `veridianOverdue` helper, tous
  deux nil-safe). Test colocalisé `veridian_followup_prioritizer_test.go` (J+7 dû avant J+0
  dû, retard avant à-l'heure, départages, troncature au limit garde les plus urgents,
  non-régression set inchangé, stabilité).

⚠️ **Diffs INLINE supplémentaires** (priorisation follow-up) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/automation_executor.go` | `ProcessBatch` : hisse `now` hors du call repo + appel `veridianPrioritizeFollowups(contacts, now)` après le fetch, avant la boucle `Execute` (fichier déjà étendu Veridian : gate cold exit + `coldReplyChecker`). |
