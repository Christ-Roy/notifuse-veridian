# Pixel d'ouverture PAR INFRA — dernier levier non-infra harmonisé (cold outbound, 2026-06-17)

Décision Robert 2026-06-17 : « toute la config doit être réglable PAR INFRA ». Audit
lead : tous les leviers cold étaient déjà sur `EmailProvider` (rates/caps/exclusion/
fenêtre/jitter/anti-hash/tracking/warmup) SAUF UN — le pixel d'ouverture
`VeridianOpenPixelByClass` vivait au NIVEAU WORKSPACE uniquement et
`VeridianResolveOpenPixel` ne recevait pas le `provider`. Ce lot ajoute le niveau
INFRA au MILIEU de la cascade pixel, exactement comme les rates/caps R2.

- **Cascade pixel étendue** (du + spécifique au + général, PAR CLASSE — pas
  tout-ou-rien) : `broadcast.metadata` → **`infra (EmailProvider.VeridianOpenPixelByClass)`**
  [NOUVEAU] → `workspace settings` → défaut tunnel. Le premier niveau qui DÉFINIT
  EXPLICITEMENT la classe demandée gagne ; un niveau sans entrée pour cette classe
  laisse la main au suivant (cohérent avec la résolution pixel broadcast>workspace
  d'origine, étendue à l'infra). Une IP fraîche peut couper le pixel partout le
  temps du warm-up sans toucher au workspace.
- **Signature** : `VeridianResolveOpenPixel(contact, email, broadcast, provider *EmailProvider, workspace)`.
  Le `provider` est l'infra d'envoi déjà en main des senders au call-site (comme le
  tracking domain / la rotation). `provider == nil` (legacy) = niveau infra sauté =
  comportement pré-infra strictement inchangé (non-régression). La config pixel infra
  est aussi un signal de « contexte tunnel » au même titre que broadcast/workspace.
- **Câblage** : `veridian_pixel_resolver.go` (`resolveOpenPixel` +param `provider`)
  + les 3 call-sites senders broadcast passent `emailProvider`. Pas de migration
  (JSON blob `integrations`, `omitempty`, pas d'allowlist — comme tous les champs R2).
- **UI ergonomie (volet 2)** : refonte de `veridian_cold_outreach_settings.tsx` en
  STRUCTURE 2 NIVEAUX explicite — bannières de scope « Workspace defaults » (bleu) /
  « Per-infrastructure overrides » (vert), chacune en `Collapse` groupé par THÈME
  (Rate & volume / Reputation / Schedule côté workspace ; Sending infrastructures /
  Tracking / Inbox côté infra). Panels `forceRender + defaultActiveKey` (ouverts par
  défaut, repliables). Le pixel par infra est exposé dans `InfraLimitsCard` en Select
  TRI-ÉTAT par classe (`Inherit` = héritage workspace puis défaut tunnel / `On` / `Off`
  = override infra), même pattern que le jitter/anti-hash existants.

⚠️ **Diffs INLINE supplémentaires** (pixel par infra) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +1 champ `EmailProvider.VeridianOpenPixelByClass` (map[string]bool, omitempty) — JSON blob, pas de migration |
| `internal/service/broadcast/queue_message_sender.go` | `buildQueueEntry` : `pixelResolver.resolveOpenPixel(..., emailProvider)` (était sans provider) |
| `internal/service/broadcast/message_sender.go` | `SendToRecipient` + boucle batch : `resolveOpenPixel`/`VeridianResolveOpenPixel` reçoivent `emailProvider` |

Fichiers veridian touchés : `internal/domain/veridian_open_pixel.go`
(`VeridianResolveOpenPixel` +param `provider` + étage infra dans la cascade),
`internal/service/broadcast/veridian_pixel_resolver.go` (`resolveOpenPixel` +param
`provider`) + tests colocalisés étendus (cascade 4 niveaux + non-régression provider
nil). Front : `console/src/components/settings/veridian_cold_outreach_settings.tsx`
(structure 2 niveaux + Collapse thématique + pixel infra tri-état) +
`console/src/services/api/workspace.ts` (`EmailProvider.veridian_open_pixel_by_class`).

