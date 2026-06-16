# Fenêtre d'envoi PAR INFRA dans l'UI (actuellement workspace-only)

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-16
> **Type** : UI PURE (backend câblé — zéro code Go)

## Contexte

La fenêtre d'envoi (horaires ouvrables) existe en backend à DEUX niveaux dans la
cascade `broadcast → infra → workspace` :
- `WorkspaceSettings.VeridianSendingWindow` (workspace) — **exposée dans l'UI** via
  `SendingWindowCard`.
- `EmailProvider.VeridianSendingWindow` (par infra) — **PAS exposée dans l'UI**.

Or l'intérêt cold premier est justement par infra : une IP en warm-up cantonnée
10h-16h pendant que le reste du workspace envoie sur une fenêtre plus large. Le
backend le permet, l'UI ne le règle pas.

## État réel constaté (code à l'appui)

- Backend par infra présent : `internal/domain/email_provider.go:107`
  ```go
  VeridianSendingWindow *VeridianSendingWindow `json:"veridian_sending_window,omitempty"`
  ```
  cascade résolue dans `internal/service/queue/veridian_sending_window_gate.go`
  (`veridianResolveSendingWindow` : broadcast → infra → workspace). JSON blob, pas
  d'allowlist (comme R2).
- UI workspace présente : `veridian_cold_outreach_settings.tsx:837` `SendingWindowCard`
  édite `workspace.settings.veridian_sending_window`.
- UI par infra ABSENTE : `InfraLimitsCard` (`:517`) ne traite que rates/caps/
  per-recipient. Le type front `EmailProvider`
  (`console/src/services/api/workspace.ts:185-191`) n'a PAS `veridian_sending_window`.
  `grep "veridian_sending_window" console/src/services/api/workspace.ts` →
  uniquement sur `WorkspaceSettings`, jamais sur `EmailProvider`.

## Demande précise (UI seulement)

1. `console/src/services/api/workspace.ts` : `EmailProvider` +=
   `veridian_sending_window?: VeridianSendingWindow` (le type
   `VeridianSendingWindow` existe déjà, défini pour le workspace).
2. `veridian_cold_outreach_settings.tsx` : ajouter par intégration dans
   `InfraLimitsCard` (ou une nouvelle carte « Fenêtre d'envoi par infra ») un
   éditeur de fenêtre RÉUTILISANT la logique de `SendingWindowCard` (jours,
   créneaux 30 min, timezone, validation end > start). Extraire le corps éditable
   de `SendingWindowCard` en sous-composant réutilisable
   (`<SendingWindowEditor value onChange />`) pour ne pas dupliquer les
   constantes `WEEKDAY_OPTIONS`/`START_SLOTS`/`END_SLOTS`. Persister sur
   `EmailProvider.veridian_sending_window` via `updateIntegration` (provider
   COMPLET conservé). Vide/désactivé = pas de fenêtre infra → héritage workspace.
3. Indiquer dans l'aide la cascade : « cette fenêtre prime sur la fenêtre du
   workspace pour cette infra ; vide = on hérite du workspace ».
4. Tests colocalisés : fenêtre infra sauvée renvoyée dans le payload, provider
   complet conservé, désactivation = champ absent (héritage).

## Impact

- Débloque le pattern warm-up réel : fenêtre serrée sur l'IP fraîche, large sur
  les IP matures, sans curl. Sans ça, on ne peut imposer qu'une fenêtre globale
  workspace (tout ou rien).
- Zéro risque backend, tier 🟡 MOYEN (UI).

## Fichiers exacts

| Fichier | Action |
|---|---|
| `console/src/services/api/workspace.ts` | +`veridian_sending_window?` sur `EmailProvider` |
| `console/src/components/settings/veridian_cold_outreach_settings.tsx` | extraire `SendingWindowEditor` + l'utiliser par infra dans `InfraLimitsCard` |
| `console/src/components/settings/veridian_cold_outreach_settings.test.tsx` | +tests |

⚠️ Piège SW cache : valider staging `?cachebust=`. Tier 🟡 → push sans `[risk:low]`.
