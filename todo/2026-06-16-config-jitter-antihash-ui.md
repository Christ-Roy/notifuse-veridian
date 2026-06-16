# Exposer jitter temporel + anti-hash identique dans l'UI Cold outreach

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-16
> **Type** : UI PURE (backend 100 % câblé, allowlist incluse — zéro code Go)

## Contexte

Deux leviers de politique d'envoi cold sont livrés en backend, persistables
workspace ET par infra, ET déjà propagés par l'allowlist `UpdateWorkspace`, mais
**totalement absents de l'UI** : un admin ne peut ni les régler ni les voir.

- **Jitter temporel** (`internal/service/queue/veridian_jitter.go` +
  `email_provider.go:VeridianJitterPct` + `workspace.go:VeridianJitterPct`) :
  disperse le délai de re-planification du throttle minute (±pct, défaut cold
  0.30) pour casser le rythme métronomique (tell de machine cold). `nil` = défaut
  cold 0.30 ; `*0` = désactivé explicite.
- **Anti-hash identique** (`internal/domain/veridian_content_hash.go` +
  `email_provider.go:VeridianAntiHashEnabled/WindowHours` + idem workspace) :
  empêche deux mails au rendu identique de partir vers la même classe dans une
  fenêtre glissante (défaut 72h). `enabled` `*bool` nil = défaut cold ON, `*false`
  = OFF ; window int (≤0 = 72h).

## État réel constaté (code à l'appui)

- Backend allowlist PRÊTE : `internal/service/workspace_service.go:408-410`
  ```go
  existingWorkspace.Settings.VeridianJitterPct = settings.VeridianJitterPct
  existingWorkspace.Settings.VeridianAntiHashEnabled = settings.VeridianAntiHashEnabled
  existingWorkspace.Settings.VeridianAntiHashWindowHours = settings.VeridianAntiHashWindowHours
  ```
  → tout champ envoyé par l'UI sera persisté. Par infra : `EmailProvider` est un
  JSON blob, les 3 champs `omitempty` passent automatiquement (aucune allowlist).
- UI ABSENTE :
  `grep -rniE "jitter|anti.?hash|rotation" console/src/components/settings/ console/src/services/api/workspace.ts`
  → **zéro résultat**. Ni champ, ni type, ni libellé.
- Le type front `EmailProvider` (`console/src/services/api/workspace.ts:185-191`)
  et `WorkspaceSettings` (`:56-73`) n'ont AUCUN des champs jitter/anti-hash.

Conséquence : la dispersion temporelle (anti-détection cold #1) et l'anti-empreinte
de hash tournent sur leurs **défauts implicites** sans qu'on puisse les ajuster
par campagne sensible — alors que le backend les expose à 3 niveaux.

## Demande précise (UI seulement)

1. `console/src/services/api/workspace.ts` :
   - `WorkspaceSettings` += `veridian_jitter_pct?: number`,
     `veridian_anti_hash_enabled?: boolean`, `veridian_anti_hash_window_hours?: number`.
   - `EmailProvider` += les mêmes 3 champs (`snake_case` miroir du Go).
   - ⚠️ `veridian_jitter_pct` et `veridian_anti_hash_enabled` sont des **pointeurs**
     côté Go (`*float64` / `*bool`) avec une sémantique tri-état :
     `undefined`/absent = défaut cold, `0`/`false` = désactivé explicite. Le front
     doit DISTINGUER « non configuré » de « 0 » : ne PAS omettre un `0`/`false`
     explicitement choisi par l'admin (sinon il retombe sur le défaut cold ON). Un
     `Switch` 3-états (héritage défaut / forcé ON / forcé OFF) ou un `Switch
     "override"` + valeur est plus sûr qu'un `InputNumber` qui collapse vide↔0.
2. `console/src/components/settings/veridian_cold_outreach_settings.tsx` :
   - **Bloc workspace** (carte dédiée, à côté du Sending window) :
     - Jitter : `Switch "Override the default ±30% jitter"` + `InputNumber`
       (pct 0–0.9, désactivé si pas d'override). Aide : « casse le rythme
       métronomique d'envoi (anti-détection). Vide = défaut cold ±30%. »
     - Anti-hash : `Switch` tri-état (défaut cold / forcé ON / forcé OFF) +
       `InputNumber` fenêtre en heures (défaut 72). Aide : « empêche deux mails
       identiques vers le même provider dans la fenêtre. »
   - **Par infra** (étendre `InfraLimitsCard`) : mêmes 3 contrôles par
     intégration, persistés sur `EmailProvider` (renvoyer le provider COMPLET
     comme les rates/caps/tracking — sinon on perd les senders).
3. Tests colocalisés `veridian_cold_outreach_settings.test.tsx` : un override
   jitter sauvé est renvoyé dans le payload `workspaces.update` ; anti-hash forcé
   OFF envoie bien `false` (pas omis) ; par infra le provider complet est conservé.

## Impact

- Permet de durcir/relâcher l'anti-détection campagne par campagne sensible sans
  curl (ces deux leviers sont précisément ceux qui distinguent un envoi cold
  « propre » d'un envoi détectable). Aujourd'hui un admin non-dev ne peut pas y
  toucher alors que la mécanique est en prod (v52.0).
- Zéro risque backend (aucune ligne Go), tier 🟡 MOYEN (UI dashboard).

## Fichiers exacts

| Fichier | Action |
|---|---|
| `console/src/services/api/workspace.ts` | +3 champs sur `WorkspaceSettings` + `EmailProvider` |
| `console/src/components/settings/veridian_cold_outreach_settings.tsx` | carte Jitter/Anti-hash workspace + contrôles dans `InfraLimitsCard` |
| `console/src/components/settings/veridian_cold_outreach_settings.test.tsx` | +tests payload/non-régression |

⚠️ Piège SW cache (memory `project_notifuse_console_sw_cache`) : valider le rendu
staging avec `?cachebust=`. Tier promo 🟡 → push sans `[risk:low]` (UI à valider en
rendu réel Chrome avant promo, cf. memory `feedback_skip_prod_pour_valider_UI`).
