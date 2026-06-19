# Audit cold — gate fenêtre d'envoi placé APRÈS 2-3 COUNT DB (gaspillage hors-fenêtre + doc contradictoire)

> **Sévérité** : 🟢 P2 (perf/cohérence — pas de bug fonctionnel, gaspillage DB hors fenêtre)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-19
> **Source** : audit de cohérence cold (axe 2 ordre des gates)

## Problème

Ordre réel des gates dans `worker.go:processEntry` :

```
282  circuit breaker     (in-memory, cheap)
308  exclusion classe    (classify, MX caché, cheap)
338  throttle minute     (token bucket RAM, cheap)
354  daily cap           (DB COUNT × 1-2 : per-recipient + per-class)   ← EXPENSIVE
372  per-sender cap      (DB COUNT × 1)                                  ← EXPENSIVE
389  sending window      (IsWithinWindow = test de temps PUR)           ← CHEAP, mais APRÈS les COUNT
408  pre-filter          (syntaxe/disposable pur + DNS best-effort)
442  content-hash        (DB EXISTS, log-only)
```

Le gate **sending window** est un check `O(1)` pur (comparaison heure/jour/tz,
`veridian_sending_window_gate.go:54-68`). Il est pourtant exécuté **APRÈS** le
daily cap (1-2 COUNT DB) et le per-sender cap (1 COUNT DB).

Conséquence : pour toute entrée picked-up **hors fenêtre** (ex. window
lun-ven 9h-18h = ~75 % de la semaine en heures), le worker exécute jusqu'à
**3 COUNT DB** (`CountSentSinceForContact`, `CountSentSinceForDomainsAndSenderDomain`,
`CountSentSinceForSender`) AVANT de découvrir que l'entrée doit juste être
reschedulée à la prochaine ouverture. Ces COUNT sont du pur gaspillage : le
verdict « hors fenêtre » est indépendant de l'état des compteurs.

En warm-up cold avec fenêtre serrée + gros volume en queue, ça fait tourner la
DB pour rien la nuit / le week-end (le worker re-pick les mêmes entrées à
chaque tick tant qu'elles ne sont pas reschedulées au-delà).

## La doc se contredit elle-même

`CLAUDE.md:861-862` (section « Fenêtre d'envoi ») :

> - **Câblage worker** : gate dans `processEntry` APRÈS le daily cap, AVANT le
>   pré-filtre (**pas de classification/COUNT si on est hors fenêtre**).

La justification entre parenthèses (« pas de COUNT si hors fenêtre ») est
EXACTEMENT ce que le placement actuel défait : le daily-cap COUNT s'exécute
AVANT la fenêtre. L'intention de design n'a pas été réalisée — le code fait
l'inverse de ce que la doc prétend qu'il évite.

Idem `veridian_sending_window_gate.go:9-14` parle de « troisième frère des gates
throttle/cap » mais l'ordre logique optimal (cheap-first) voudrait la fenêtre
juste après le throttle minute (lui aussi cheap, RAM), AVANT tout COUNT DB.

## Fix proposé

Déplacer l'appel `veridianSendingWindowGate` (worker.go:389) **juste après le
throttle minute** (après worker.go:347, avant le daily cap worker.go:354).

Ordre cible :
```
circuit breaker → exclusion → throttle minute → SENDING WINDOW → daily cap →
per-sender cap → pre-filter → content-hash
```

Tous ces gates en amont du daily-cap sont cheap (RAM/pur) ; la fenêtre rejoint
sa place naturelle dans la zone cheap. Aucun impact fonctionnel (l'ordre entre
gates indépendants ne change pas le verdict d'un envoi qui passe ; il ne change
que l'ordre de SHORT-CIRCUIT) — sauf le gain : zéro COUNT DB hors fenêtre.

Mettre à jour la doc CLAUDE.md (la parenthèse « pas de COUNT si hors fenêtre »
redevient vraie) + le commentaire d'en-tête de `veridian_sending_window_gate.go`.

## Impact / risque

- Tier 🟡 (réordonnancement de gates, surface envoi). Pas de nouveau code, juste
  un déplacement d'appel + maj tests d'ordre s'il en existe.
- **Réversible**, déterministe. Vérifier qu'aucun test ne dépend de l'ordre
  daily-cap-avant-fenêtre (peu probable, les gates sont indépendants).
- Smoke staging : confirmer qu'une entrée hors fenêtre est bien reschedulée sans
  toucher message_history (déjà couvert par cold-simulate `sending_window_decision`).

## Note

Pas un bug fonctionnel : aucune entrée n'est mal traitée, c'est purement de
l'efficience + une incohérence doc/code. Classé P2. À grouper avec le ticket
warmup (même fichier `veridian_daily_cap.go` / `worker.go` touché) si une passe
cold est lancée.
