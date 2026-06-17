# Presets de politique d'envoi cold (warm-up / cruise / prudence Microsoft)

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-16
> **Type** : UI (composition de briques backend existantes) — possible petit helper Go optionnel

## Contexte (demande de Robert)

Robert (audit cold 2026-06-16) : *« il va manquer des presets + leur configuration
avancée pour la politique d'envoi »*.

Toutes les briques de politique d'envoi existent et sont configurables (rates,
caps, fenêtre, jitter, anti-hash, pixel, exclusion de classe une fois le ticket
exclusion livré), MAIS chacune se règle indépendamment, classe par classe, infra
par infra. Régler une infra en warm-up = ~30 champs à remplir à la main. Aucun
**preset** ne compose ces briques en une intention métier nommée (« Warm-up
agressif », « Croisière », « Prudence Microsoft »).

## État réel constaté (code à l'appui)

- `grep -rliE "preset|warmup|warm_up|warm-up" internal/ console/src/` → AUCUN
  fichier de preset de politique d'envoi (les hits `warmup` sont des blocs
  email_builder / commentaires, pas un preset).
- L'UI `veridian_cold_outreach_settings.tsx` présente des champs nus (rates/caps
  par classe, fenêtre, à venir jitter/anti-hash/exclusion) sans bouton
  « appliquer un profil ».
- Les défauts de classe pixel existent (`VERIDIAN_DEFAULT_OPEN_PIXEL` côté front,
  `veridian_open_pixel.go` côté Go) mais c'est le SEUL embryon de profil ; rien
  pour les rates/caps/fenêtre/jitter.

## Demande précise

Approche recommandée : **preset CÔTÉ UI** (zéro backend), un preset = un ensemble
de valeurs pré-remplies que l'admin applique puis ajuste. On NE crée PAS une
nouvelle entité persistée (pas de table preset) — un preset est juste un bouton qui
`form.setFieldsValue(...)` / pose les drafts de `InfraLimitsCard`.

1. Définir 3 presets initiaux dans un module front
   `console/src/services/cold/sending_policy_presets.ts` (constantes typées sur
   `VeridianProviderClass` + champs window/jitter/anti-hash/exclusion) :
   - **Warm-up prudent** : rates très bas par classe (google 1/min, microsoft
     EXCLU via le ticket exclusion, freemail 2/min…), caps/jour bas (google 50,
     freemail 100), fenêtre lun-ven 9h-18h Europe/Paris, jitter ±30 %, anti-hash
     ON 72h, per-recipient cap 1/j.
   - **Croisière** : rates moyens, caps confortables, fenêtre lun-ven 8h-20h,
     jitter ±20 %, anti-hash ON, microsoft réactivé à débit modéré.
   - **Prudence Microsoft** : identique croisière mais microsoft EXCLU (réutilise
     le champ exclusion du ticket `2026-06-16-config-exclusion-provider-cold.md`).
2. UI : dans `veridian_cold_outreach_settings.tsx`, un `Select`/`Segmented`
   « Profil d'envoi » au-dessus des cartes. Choisir un profil → pré-remplit le
   formulaire workspace (et/ou l'infra sélectionnée) avec les valeurs du preset,
   en laissant l'admin AJUSTER avant de sauver (le preset n'est qu'un point de
   départ, pas un mode verrouillé). Bien expliciter « profil appliqué, ajustez puis
   Enregistrer » — ne pas auto-sauver.
3. **Dépendance** : le preset « Prudence Microsoft » et le levier exclusion du
   preset « Warm-up » nécessitent le ticket
   `2026-06-16-config-exclusion-provider-cold.md` livré (sinon le preset ne peut pas
   exprimer « ne pas envoyer à microsoft »). Livrer l'exclusion D'ABORD.
4. (Optionnel, plus tard) si Robert veut des presets PARTAGÉS/versionnés
   cross-workspace : exposer les presets via un petit endpoint read-only Go servant
   un JSON statique (pas de DB). À NE PAS faire en V1 — front-only suffit.

## Impact

- Réduit la config d'une infra warm-up de ~30 champs à 1 clic + ajustements.
- Encode le savoir-faire délivrabilité (quels débits pour une IP fraîche) dans le
  produit au lieu de le laisser dans la tête de Robert / un doc.
- Front-only = tier 🟡, zéro risque backend (sauf si endpoint optionnel ajouté).

## Fichiers exacts

| Fichier | Action |
|---|---|
| `console/src/services/cold/sending_policy_presets.ts` (NOUVEAU) | 3 presets typés + tests |
| `console/src/components/settings/veridian_cold_outreach_settings.tsx` | sélecteur de profil + apply-to-form |
| `console/src/components/settings/veridian_cold_outreach_settings.test.tsx` | +test apply preset → champs pré-remplis |

Dépend de : `2026-06-16-config-exclusion-provider-cold.md` (P0) pour le levier
exclusion. Tier 🟡 → push sans `[risk:low]`, valider rendu Chrome.

---

## ✅ Résolu — 2026-06-17 (agent ui-cold)

Livré dans `console/src/components/settings/veridian_cold_outreach_settings.tsx`
+ `console/src/services/api/workspace.ts` + `console/src/services/cold/sending_policy_presets.ts`.
SHA: 6f7c0ef4 (branche `veridian`). UI-pure (backend allowlist/EmailProvider JSON blob
vérifiés — zéro Go). tsc vert, 48/48 tests front verts, lingui extract+compile OK.
À valider en rendu réel staging (Chrome `?cachebust=`) avant promo prod (tier 🟡).
