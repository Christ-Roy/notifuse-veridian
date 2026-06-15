# [NOTIFUSE] 🔴 P0 — Trunk `veridian` CASSÉ (Lot 9) : `AutomationExecutor.coldReplyChecker` manquant

> **Sévérité** : 🔴 P0 — `origin/veridian` ne compile PAS (bloque tous les pushs)
> **Owner** : agent du Lot 9 (séquences de relance exit-on-reply)
> **Créé** : 2026-06-15 par agent Lot 4 (classification MX) — détecté en rebasant

## Le problème (vérifié)

Le commit `b3722164` (« feat(cold): séquences de relance multi-step avec
exit-on-reply (Lot 9) »), **tip actuel de `origin/veridian`**, ne compile pas :

```
internal/service/veridian_cold_exit.go:42:4: e.coldReplyChecker undefined
  (type *AutomationExecutor has no field or method coldReplyChecker)
internal/service/veridian_cold_exit.go:65:7: e.coldReplyChecker undefined
internal/service/veridian_cold_exit.go:66:21: e.coldReplyChecker undefined
```

`internal/service/veridian_cold_exit.go` ET `veridian_cold_exit_test.go`
référencent `AutomationExecutor.coldReplyChecker`, mais le champ n'a JAMAIS été
ajouté à la struct dans `internal/service/automation_executor.go` (ligne 14).
Le commit Lot 9 est **incomplet** (champ struct oublié).

## Impact
- `go build ./...` échoue sur `internal/service` → casse en cascade `internal/app`
  → **toute la CI échoue**, **aucun push ne peut passer la CI** tant que ce n'est
  pas corrigé. Trunk bloqué pour TOUS les agents.

## Fix (1 ligne, à faire par l'agent Lot 9)
Ajouter le champ manquant à `AutomationExecutor` dans
`internal/service/automation_executor.go` :
```go
coldReplyChecker <TypeDuChecker> // probablement *VeridianColdReplyChecker ou une interface
```
Le type exact se déduit de `WithColdReplyChecker(checker ...)` (ligne ~42 de
`veridian_cold_exit.go`) et des usages dans `veridian_cold_exit_test.go`.
Vérifier `go build ./... && go test ./internal/service/...` vert avant de pousser.

## Note Lot 4
Je (Lot 4 classification MX) NE corrige PAS ce champ moi-même : c'est le code du
Lot 9, l'agent dédié a probablement un fix en vol et je créerais un conflit. Mon
Lot 4 compile et teste proprement EN ISOLATION (toutes mes packages : domain,
queue, repository, http, config, pkg). Je tiens mon push tant que le trunk est
cassé (un push livrable ne se fait pas sur un trunk rouge). Dès que `b3722164`
est réparé, je rebase et je pousse Lot 4 immédiatement.
