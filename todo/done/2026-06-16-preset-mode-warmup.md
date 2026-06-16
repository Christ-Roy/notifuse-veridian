# Preset « Mode warmup » — composition des briques cold en un usage métier nommé

> **Sévérité** : 🔴 P0 — demande business #1 (Robert, verbatim ci-dessous)
> **Owner** : agent notifuse
> **Créé** : 2026-06-16
> **Type** : audit de cohérence — axe PRESETS / WORKFLOWS PRODUIT
> **Statut** : SPEC prête à exécuter (lecture-seule, aucun code écrit par l'audit)

## Demande Robert (verbatim)

> « il me faut pour commencer le tunnel un preset "mode warmup" avec genre
> 1 envoi par jour par provider et faire un round-robin entre chaque adresse
> mail »

## Diagnostic : le trou exact

Toutes les **briques d'enforcement** de la politique d'envoi cold existent et sont
testées (daily-cap, sender-rotation, sending-window, throttle par classe). Elles
sont même déjà **configurables une par une** dans la console (Settings → Cold
outreach, `veridian_cold_outreach_settings.tsx`).

Ce qui MANQUE = la **composition produit** : aucun « preset » ne pose en un clic
l'ensemble cohérent de valeurs qui constitue le « mode warmup ». Aujourd'hui, pour
obtenir le comportement demandé par Robert, un admin doit :
- aller dans la carte « Per-recipient daily cap » et la carte de chaque classe,
- saisir à la main `cap/day = 1` sur 5 à 11 classes,
- activer la fenêtre d'envoi (jours + heures + TZ),
- s'assurer que l'infra a ≥ 2 senders (le round-robin s'active alors tout seul),
- recommencer pour chaque infra en warm-up.

C'est ~20 champs à remplir sans se tromper, sans nom métier, sans garantie de
cohérence. Le preset transforme ça en **un bouton « Appliquer le mode warmup »**.

**Vérifié (grep, pas supposé)** : aucun mécanisme de preset/template de config cold
n'existe. `grep -ri "preset" internal/ console/src/components/settings/` → seulement
`BlogSettings.tsx` (éditeur blog) + `orchestrator_test.go` (sans rapport).
`grep -ri "warmup"` → uniquement des commentaires/help-text dans les bricks
existantes, jamais un preset orchestré.

## Confirmation des briques (API de config exacte, lues dans le code)

| Brique | Fichier | Clé de config | Cap=1/jour possible ? |
|---|---|---|---|
| Daily cap par CLASSE | `internal/service/queue/veridian_daily_cap.go` + `internal/domain/veridian_provider_class.go` | map `{classe: int/jour}` sous clés `veridian_provider_class_daily_cap` (broadcast.metadata → `EmailProvider` → workspace.Settings) | **OUI** — `VeridianProviderClassDailyCapFromMetadata` retient tout entier `> 0`, donc `1` est valide. Source de vérité = `COUNT(message_history)` depuis minuit UTC. |
| Daily cap par DESTINATAIRE | idem | int `veridian_per_recipient_daily_cap` (même cascade) | OUI (anti-harcèlement, généralement `1`). |
| Sender rotation round-robin | `internal/domain/veridian_sender_rotation.go` | **AUCUNE clé** — automatique : `VeridianSelectSender` fait du round-robin keyé par classe destinataire **dès que l'infra a `> 1` sender ET qu'on est en contexte cold** (`VeridianIsColdContext`). | N/A — s'active tout seul. |
| Sending window | `internal/domain/veridian_sending_window.go` | objet `veridian_sending_window` `{days,start_hour,end_hour,timezone}` (cascade broadcast → infra → workspace) | N/A — `{days:[1..5], start_hour:9, end_hour:18, timezone:"Europe/Paris"}`. |
| Throttle / rate par classe | `internal/service/queue/veridian_provider_throttle.go` + `veridian_provider_class.go` | map `{classe: float emails/min}` sous `veridian_provider_class_rates` (même cascade) | Fractions OK (`0.5` = 1 mail/2 min). En warmup le **cap/jour=1 domine déjà** ; le rate est surtout utile pour étaler dans la journée. |

### Point clé : « 1 envoi par jour par provider » = cap par CLASSE de provider DESTINATAIRE

Robert dit « par provider ». Dans le modèle Veridian, « provider » = la **classe de
provider destinataire** (`google` / `microsoft` / `yahoo_aol` / `freemail_fr` /
`corporate` / …). C'est exactement `veridian_provider_class_daily_cap = {google:1,
microsoft:1, …}`. Le cap est **partagé pour toute la classe** (1 mail/jour vers
l'ENSEMBLE des Gmail), ce qui est le comportement réputation correct en warmup.

⚠️ **À clarifier avec Robert si besoin** (ne PAS deviner) : « 1/jour par provider »
peut aussi vouloir dire « 1/jour par **adresse d'envoi** (sender) ». Les deux
lectures sont défendables :
- **Lecture A (retenue par défaut V1)** : cap par classe destinataire = protège la
  réputation côté receveur. C'est la brique existante, zéro backend neuf.
- **Lecture B** : cap par sender/IP émetteur = warmup IP classique (X mails/jour
  par boîte qui monte). Cette dimension n'existe PAS en backend (le cap est keyé
  destinataire, pas émetteur). → ticket backend séparé
  `2026-06-16-cap-par-sender-emetteur.md`.

Le preset V1 implémente la lecture A (faisable aujourd'hui). La lecture B est
listée comme manque backend ci-dessous.

## SPEC du preset « Mode warmup » V1

### Nature du preset : 100 % FRONT (pur set de valeurs)

Le preset n'est **rien d'autre qu'un ensemble de valeurs appliquées à la config
cold existante**, qui transite par les endpoints DÉJÀ câblés :
- `POST /api/workspaces.update` (settings workspace : rates, caps, sending window),
- `POST /api/workspaces.updateIntegration` (limites par infra `EmailProvider`, si
  on veut poser le preset au niveau infra plutôt que workspace).

Le backend sait DÉJÀ lire toutes ces clés (prouvé : cascades `veridianResolveDailyCaps`,
`veridianResolveProviderClassRates`, `veridianResolveSendingWindow`). Donc le preset
V1 est **front-only** : un bouton qui pré-remplit les champs du formulaire cold
outreach existant avec les valeurs warmup, l'admin relit et clique « Save ».

**Aucun nouveau backend, aucune nouvelle table, aucune migration pour le preset V1.**

### Valeurs exactes posées par le preset « Mode warmup »

Au niveau WORKSPACE (settings, via `workspaceService.update`) :

```jsonc
{
  // 1 envoi / jour / classe de provider destinataire (les 5 classes principales ;
  // on peut étendre aux 11 via le toggle "show all classes" déjà présent).
  "veridian_provider_class_daily_cap": {
    "google": 1, "microsoft": 1, "yahoo_aol": 1, "freemail_fr": 1, "corporate": 1
  },
  // Anti-harcèlement : jamais 2 mails/jour à la même adresse.
  "veridian_per_recipient_daily_cap": 1,
  // Rythme lent dans la journée (optionnel mais cohérent warmup) : étale les rares
  // envois au lieu d'un burst. 0.5/min = au plus 1 mail / 2 min par classe.
  "veridian_provider_class_rates": {
    "google": 0.5, "microsoft": 0.5, "yahoo_aol": 0.5, "freemail_fr": 0.5, "corporate": 0.5
  },
  // Heures ouvrables (anti-spam + crédible humain).
  "veridian_sending_window": {
    "days": [1,2,3,4,5], "start_hour": 9, "end_hour": 18, "timezone": "Europe/Paris"
  }
  // Pixel par classe : laisser le défaut tunnel (OFF google/microsoft) — déjà géré
  // par effectivePixel(). Le preset NE le touche pas (ne pas dégrader la réputation).
}
```

Le **round-robin entre adresses d'envoi** demandé par Robert ne nécessite AUCUNE
valeur : il s'active automatiquement dès que (a) l'infra a ≥ 2 senders et (b) on est
en contexte cold — et poser n'importe laquelle des clés ci-dessus AU NIVEAU
WORKSPACE fait basculer `VeridianIsColdContext(workspace)` à true. Donc appliquer
le preset suffit à activer le round-robin. **Le preset doit afficher un avertissement
si l'infra a < 2 senders** (« ajoutez au moins 2 boîtes d'envoi pour activer le
round-robin ») — vérif sur `integration.email_provider.senders`.

### Où vit le preset dans l'UI

Dans la section existante
`console/src/components/settings/veridian_cold_outreach_settings.tsx`, en HAUT de la
section owner (juste après le bloc `explainer`), un encart :

- Un `Card` « Presets » avec des boutons : `Mode warmup` (V1), potentiellement
  `Mode croisière` / `Off` plus tard.
- Bouton « Appliquer le mode warmup » → pré-remplit le `Form` (rates, caps) via
  `form.setFieldsValue(...)` + `setTouched(true)` + applique l'état de la
  `SendingWindowCard`. **Il NE sauve PAS tout seul** : l'admin voit les valeurs,
  peut ajuster, puis clique « Save changes » (pattern existant). Évite un push
  destructif silencieux sur une config existante.
- Un `Popconfirm` si une config cold existe déjà (« le preset va écraser vos
  débits/caps/fenêtre actuels — continuer ? »).
- Texte d'explication : « Le mode warmup limite à 1 envoi/jour vers chaque provider
  et répartit les envois sur toutes vos adresses (round-robin). Idéal pour démarrer
  une nouvelle IP/domaine sans griller la réputation. »

⚠️ La `SendingWindowCard` gère son état localement (`useState`, lu depuis
`workspace.settings.veridian_sending_window`). Pour que le preset puisse pré-remplir
la fenêtre, soit (a) hisser l'état window au composant parent
`VeridianColdOutreachSettings`, soit (b) exposer un `ref`/callback `applyPreset` sur
la card. Recommandation : (b) callback `onApplyWarmupWindow()` passé en prop à la
card, qui set ses `useState` internes (enabled/days/start/end/tz) aux valeurs preset.
À l'agent UI de trancher proprement.

### Étapes d'implémentation (agent UI)

1. Ajouter une constante `VERIDIAN_WARMUP_PRESET` (les valeurs ci-dessus) dans
   `console/src/services/api/workspace.ts` à côté de `VERIDIAN_PROVIDER_CLASSES` /
   `VERIDIAN_DEFAULT_OPEN_PIXEL` (source de vérité front des valeurs preset).
2. Dans `veridian_cold_outreach_settings.tsx`, sous-composant `PresetCard` (owner-only) :
   boutons + handler `applyWarmupPreset()` qui fait `form.setFieldsValue` sur les
   rate/cap fields + applique la fenêtre d'envoi (cf. note ci-dessus).
3. Avertissement « < 2 senders » sur les infras sans round-robin possible.
4. `Popconfirm` d'écrasement si config existante détectée.
5. Test colocalisé `veridian_cold_outreach_settings.test.tsx` : clic preset → les
   champs prennent les valeurs warmup ; rien n'est persisté tant que Save n'est pas
   cliqué ; warning si 1 sender.
6. ⚠️ Piège Lingui : libellés de provider en LITTÉRAUX (cf. `classLabel`, bug P0
   2026-06-14). Le texte d'explication du preset, lui, peut rester en `t`...``.
7. ⚠️ Piège SW cache (memory `project_notifuse_console_sw_cache`) : valider le rendu
   staging avec `?cachebust=`.

### DoD V1

- Bouton « Appliquer le mode warmup » visible en Settings → Cold outreach (owner).
- Clic → champs pré-remplis (caps=1/classe, per-recipient=1, rates=0.5, window 9-18
  lun-ven) sans sauvegarde auto.
- Save → persiste ; reload → valeurs présentes ; un envoi cold respecte 1/jour/classe
  + round-robin senders (vérif via E2E `cold-config.spec.ts` existant : provision
  workspace jetable, applique preset, vérifie
  `workspace.settings.veridian_provider_class_daily_cap`).
- Warning si infra < 2 senders.

## Ce qui MANQUE en backend (tickets séparés, hors preset V1)

Le preset V1 pose un cap STATIQUE. Le vrai « warmup » au sens délivrabilité est
PROGRESSIF (1/jour J1, puis 2, 5, 10… sur 2-4 semaines). Cette montée en charge
n'existe PAS — voir tickets séparés ci-dessous. V1 est volontairement statique et
suffisant pour « commencer le tunnel » (la demande littérale de Robert) ; la rampe
auto est l'itération suivante.

→ `todo/2026-06-16-warmup-progressif-rampe-auto.md` (montée de cap auto jour/jour)
→ `todo/2026-06-16-cap-par-sender-emetteur.md` (cap par adresse d'envoi, lecture B)

## Fichiers concernés (exécution)

- `console/src/services/api/workspace.ts` — constante `VERIDIAN_WARMUP_PRESET`.
- `console/src/components/settings/veridian_cold_outreach_settings.tsx` — `PresetCard`.
- `console/src/components/settings/veridian_cold_outreach_settings.test.tsx` — test preset.
- (lecture) `internal/domain/veridian_provider_class.go`, `veridian_sending_window.go`,
  `veridian_sender_rotation.go`, `internal/service/queue/veridian_daily_cap.go`,
  `veridian_provider_throttle.go` — confirment que toutes les valeurs sont déjà lisibles.
- (E2E) `tests/e2e-veridian/specs/cold-config.spec.ts` — étendre pour le preset.

## Impact business

Débloque le démarrage du tunnel cold demandé #1 par Robert : passer d'une config
manuelle de ~20 champs error-prone à un bouton « Mode warmup » cohérent et sûr.
Front-only = livrable rapidement, zéro risque backend/migration.

## ✅ Résolu — 2026-06-17 (SHA d767c23d, backend ac38dbb0)

Preset « Mode warmup » V1 LIVRÉ. Comme le cap émetteur (backend) existe désormais,
le preset pose AUSSI cette dimension :

- **`VERIDIAN_WARMUP_PRESET`** (`workspace.ts`) : cap/classe=1 (11 classes),
  per-recipient=1, **per-sender=20** (warmup IP), rates=0.5/min, fenêtre lun-ven
  9-18 Europe/Paris.
- **`PresetCard`** (owner-only) dans `veridian_cold_outreach_settings.tsx` :
  bouton « Appliquer le mode warmup » + Popconfirm (écrase) + avertissement
  « <2 senders » (round-robin off). `applyWarmupPreset()` pré-remplit le form
  (setFieldsValue) + nonce → SendingWindowCard pré-remplit sa fenêtre. NE SAUVE
  PAS (relecture + Save). Pixel NON touché.
- Champ « per-sender daily cap (warmup) » exposé workspace + infra.
- Tests colocalisés étendus (25 verts). tsc + lint OK.

V1 = cap STATIQUE. Rampe PROGRESSIVE (1→2→5→10/j auto) = ticket séparé
`2026-06-16-warmup-progressif-rampe-auto.md`, NON livré ici.

Promo prod : E2E on-premise staging par le lead.
