# Warmup progressif — montée de charge automatique du cap (rampe jour/jour)

> **Sévérité** : 🟡 P1 — itération du preset warmup (le V1 statique suffit à démarrer)
> **Owner** : agent notifuse
> **Créé** : 2026-06-16
> **Type** : audit de cohérence — manque BACKEND identifié sous le preset warmup
> **Dépend de** : `2026-06-16-preset-mode-warmup.md` (V1 statique livré d'abord)

## Le trou

Le preset « Mode warmup » V1 pose un cap STATIQUE (1/jour/classe). Le vrai warmup au
sens délivrabilité est PROGRESSIF : on démarre une IP/domaine frais à très peu de
volume puis on monte par paliers (ex. J1=1, J2=2, J4=5, J7=10, J14=25, J21=50…/jour
par classe) sur 2-4 semaines. C'est ce que font Lemlist/Instantly automatiquement.

**Vérifié (grep)** : rien n'incrémente le cap au fil des jours. Tous les gates
(`veridian_daily_cap.go`, `veridian_provider_throttle.go`, `veridian_sending_window_gate.go`)
ne font que LIRE la config statique posée. `ColdSequenceStep.IntegrationID`
(`veridian_cold_sequence.go`) est un override d'infra PAR ÉTAPE de séquence, pas une
rampe temporelle. Aucun cron, aucune table de progression, aucune date de début de
warmup n'existe.

## Demande (spec backend)

Un mécanisme qui fait croître le cap effectif d'une infra (ou d'un workspace) en
fonction du nombre de jours écoulés depuis le DÉBUT de son warmup, selon une courbe
de paliers configurable.

### Modèle de données (proposition, à valider)

Sur `EmailProvider` (JSON blob, donc **PAS de migration** — pattern R2/jitter/tracking
déjà éprouvé : 3 champs `omitempty` passent automatiquement, aucune allowlist à
étendre dans `UpdateIntegration`) :

```go
// EmailProvider (internal/domain/email_provider.go) — nouveaux champs omitempty
VeridianWarmupStartedAt   *time.Time `json:"veridian_warmup_started_at,omitempty"`
VeridianWarmupSchedule    []int      `json:"veridian_warmup_schedule,omitempty"` // cap/jour/classe par palier
VeridianWarmupStepDays    int        `json:"veridian_warmup_step_days,omitempty"` // durée d'un palier (défaut 1)
```

- `VeridianWarmupStartedAt` = la date où le warmup a commencé pour cette infra.
- `VeridianWarmupSchedule` = la courbe `[1, 2, 5, 10, 25, 50, 100]` : la valeur du
  cap/jour/classe au palier N.
- `VeridianWarmupStepDays` = combien de jours dure chaque palier (défaut 1 = un palier
  par jour ; 2 = on reste 2 jours à chaque valeur).

### Résolution (où ça vit)

Le cap effectif warmup se calcule À LA LECTURE dans la cascade `veridianResolveDailyCaps`
(`internal/service/queue/veridian_daily_cap.go`) : si l'infra a un
`VeridianWarmupStartedAt` non nil ET un `VeridianWarmupSchedule` non vide, le cap par
classe devient `schedule[min(elapsedDays/stepDays, len(schedule)-1)]` — qui PRIME sur
le cap statique de l'infra. Fonction PURE testable
(`veridianWarmupCapForDay(startedAt, schedule, stepDays, now) int`), zéro I/O, zéro
cron : la date fait tout le travail (même principe que le daily-cap qui dérive « le
jour » de `now()` plutôt qu'un compteur).

**Pas de cron d'incrément** (cohérent règle d'or « pas de cron bricolé ») : le palier
se déduit de `now - startedAt` à chaque tick worker. Survit aux redémarrages.

### Front (preset + UI)

- Le preset « Mode warmup » V1 pose `VeridianWarmupStartedAt = now`,
  `VeridianWarmupSchedule = [1,2,5,10,25,50,100]`, `VeridianWarmupStepDays = 2` (ou
  ce que Robert préfère) → la rampe démarre automatiquement à l'application du preset.
- Carte UI dans `veridian_cold_outreach_settings.tsx` (sous `InfraLimitsCard`) :
  affiche « Warmup jour N/total — cap actuel X/jour », bouton « Démarrer/Réinitialiser
  le warmup », édition de la courbe (avancé).

### Décision à trancher (Robert)

- Granularité : warmup par INFRA (recommandé — une IP/domaine se warm individuellement)
  ou par workspace ? → reco : par infra.
- Courbe par défaut + durée de palier. Reco : `[1,2,5,10,25,50,100,200]` à 2 jours/palier
  (~16 jours pour atteindre le plein régime). Conservateur.

## Fichiers concernés

- `internal/domain/email_provider.go` — 3 champs warmup omitempty (JSON blob, pas de migration).
- `internal/domain/veridian_warmup.go` (NOUVEAU, flat veridian) — `veridianWarmupCapForDay` pur + parsing.
- `internal/service/queue/veridian_daily_cap.go` — `veridianResolveDailyCaps` applique
  le cap warmup de l'infra par-dessus le cap statique.
- `console/src/services/api/workspace.ts` + `veridian_cold_outreach_settings.tsx` — UI warmup.
- Tests colocalisés.

## Impact business

Vrai warmup IP/domaine automatique (standard Lemlist/Instantly) : démarrer un nouveau
domaine d'envoi sans le griller, sans réglage manuel quotidien. Le V1 statique
débloque le démarrage ; cette rampe est ce qui rend le warmup « pro » sur la durée.
