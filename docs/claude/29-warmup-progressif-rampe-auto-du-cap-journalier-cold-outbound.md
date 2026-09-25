# Warmup progressif — rampe AUTO du cap journalier (cold outbound, 2026-06-17)

Itération de la rampe automatique sous le preset warmup statique (V53) : au lieu d'un
cap fixe 1/jour/classe, le cap monte par paliers (`[1,2,5,10,25,50,100]`) au fil des
jours écoulés depuis le DÉBUT du warmup de l'infra — standard Lemlist/Instantly. Spec :
ticket `todo/2026-06-16-warmup-progressif-rampe-auto.md`.

- **Granularité = PAR INFRA** (`EmailProvider`, JSON blob) : une IP/domaine se warm
  individuellement. **PAS de migration, PAS de cron** (cohérent règle d'or) : le palier
  se DÉRIVE à la lecture de `now - startedAt` à chaque tick worker, comme le daily-cap
  dérive « le jour » de `now()`. Survit aux redémarrages par construction.
- **3 champs `omitempty`** sur `EmailProvider` (pattern R2/jitter, aucune allowlist) :
  `VeridianWarmupStartedAt *time.Time`, `VeridianWarmupSchedule []int` (cap/jour TOTAL de
  l'infra par palier), `VeridianWarmupStepDays int` (jours par palier, défaut 1). Vides =
  pas de warmup → héritage du cap statique (non-régression stricte).
- **Fichier veridian** : `internal/domain/veridian_warmup.go` — `VeridianWarmupCapForDay
  (startedAt, schedule, stepDays, now) int` PUR (`schedule[min(elapsed/step, len-1)]`,
  clamp dernier palier, futur/0 = palier 0, valeur négative = 0 best-effort) +
  `VeridianWarmupActive` + `VeridianWarmupStep` (UI « jour N/total »). Test colocalisé.
- **Résolution = cap TOTAL par infra (corrigé 2026-06-19, cf. ci-dessous)** :
  `veridian_daily_cap.go` — `veridianWarmupCap(provider, now)` appelé dans
  `veridianDailyCapGate`. Si actif, le cap warmup PRIME sur (et COURT-CIRCUITE) le
  cap-classe statique : c'est un plafond **HOLISTIQUE du VOLUME TOTAL** émis par l'infra
  sur la journée, **toutes classes destinataires confondues** (« J1 = N max, point »).
  L'enforcement compte le TOTAL par DOMAINE émetteur via
  `CountSentSinceForSenderDomain` (aucun filtre de classe destinataire) → **robuste aux
  classes MX** (le COUNT-par-classe les bypassait). 0 = pas de warmup actif ; sender
  vide (legacy) = pas d'attribution infra → warmup non enforçable (best-effort, pass).
- Le preset « Mode warmup » (UI) posera `StartedAt=now` + `Schedule=[1,2,5,10,25,50,100]`
  + `StepDays=2` → rampe auto à l'application (volet UI = agent ui-cold, hors scope ici).

⚠️ **Diffs INLINE supplémentaires** (warmup rampe auto) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +3 champs `EmailProvider` : `VeridianWarmupStartedAt` (*time.Time), `VeridianWarmupSchedule` ([]int), `VeridianWarmupStepDays` (int), tous omitempty — JSON blob, pas de migration ; +import `time` |

