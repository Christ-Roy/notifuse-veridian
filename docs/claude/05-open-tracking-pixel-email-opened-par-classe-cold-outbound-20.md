# Open tracking — pixel email.opened PAR CLASSE (cold outbound, 2026-06-11)

Découple le pixel d'**ouverture** (`email.opened`) de la réécriture de **liens**
(clics). Le tunnel cold veut le pixel ON sur les petits providers peu sensibles
(`freemail_fr`/`yahoo_aol`/`corporate`) et OFF sur `google`/`microsoft`
(réputation), tout en gardant le tracking de clics partout. Spec : ticket
`todo/2026-06-10-open-tracking-petits-providers.md` + DoD V1 §1.3.

- **Fichier veridian** : `internal/domain/veridian_open_pixel.go`
  (`VeridianResolveOpenPixel(contact, email, broadcast, workspace) → *bool` :
  défaut tunnel ON petits/FAI, OFF gros ; parsing config
  `veridian_open_pixel_by_class` ; détection contexte tunnel = tag
  `custom_string_5` OU config pixel/rates).
- **Découplage** (dans `template_compilation.go`, fichier critique, pas
  upstream-pur) : nouveau champ `TrackingSettings.EnableOpenPixel *bool`
  (nullable), helper `openPixelEnabled()`. `nil` = comportement upstream (pixel
  suit `EnableTracking`), non-nil = override explicite par classe. Le
  early-return de `TrackLinks` est corrigé pour insérer le pixel même quand
  `EnableTracking=false` (pixel ON sans réécriture de liens).
- **Config** : `broadcast.metadata["veridian_open_pixel_by_class"]` (map
  classe→bool) puis workspace settings `veridian_open_pixel_by_class` (fallback).
  Hors contexte tunnel = `nil` = strictement upstream (non-régression).
- **Fallback workspace câblé côté envoi** (hardening 2026-06-13) : les senders
  ne reçoivent qu'un `workspaceID`, ils passaient donc `workspace=nil` à
  `VeridianResolveOpenPixel` → la config pixel posée au NIVEAU WORKSPACE (chemin
  UI Settings → Cold outreach, persistée par bcc23764) était **ignorée à
  l'envoi**. Corrigé via `veridian_pixel_resolver.go` : DI optionnelle
  (`SetVeridianWorkspaceRepo`, branché par la factory), un seul `GetByID` par
  batch (mémoïsé), best-effort (échec fetch → dégrade vers défaut tunnel, jamais
  d'échec d'envoi). Sans repo injecté = comportement avant-fix (nil-safe).
- **Validé** : E2E réel staging (5/5 reçus alias Lark via relai, pixel ABSENT
  google/microsoft + PRÉSENT freemail_fr/yahoo_aol/corporate, clics ON partout,
  `opened_at` posé au fetch du pixel). Délivrabilité : sans pixel 10/10, avec
  pixel le coût réel = `T_REMOTE_IMAGE` ~0.01 (le pixel ne dégrade quasi rien sur
  petit provider ; prévoir un template à ratio texte/image sain pour éviter
  `HTML_IMAGE_ONLY`).
- 🔴 **Garde-fou envoi** : `scripts/e2e/tunnel-send.sh` NE TIRE PLUS de mail réel
  par défaut (consigne Robert 2026-06-11 — les alias test routent vers sa boîte
  Lark perso). Setup + DRAFT par défaut ; envoi réel = flag `--real-send` après
  GO du lead.

⚠️ **Diffs INLINE supplémentaires** (pixel par classe) :

| Fichier upstream | Diff Veridian |
|---|---|
| `pkg/notifuse_mjml/template_compilation.go` | +champ `TrackingSettings.EnableOpenPixel *bool` + `openPixelEnabled()` + pixel gouverné par ce flag dans `TrackLinks` (early-return inclus) |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings.VeridianOpenPixelByClass` (map[string]bool, omitempty) |
| `internal/service/broadcast/queue_message_sender.go` | `buildQueueEntry(+param contact, +param pixelResolver)` + `EnableOpenPixel = pixelResolver.resolveOpenPixel(...)` avant compilation HTML ; champ `veridianWorkspaceRepo` + `SetVeridianWorkspaceRepo` (DI optionnelle) |
| `internal/service/broadcast/message_sender.go` | 2 call-sites (SendToRecipient + boucle batch) : `EnableOpenPixel = pixelResolver.resolveOpenPixel(...)` ; champ `veridianWorkspaceRepo` + `SetVeridianWorkspaceRepo` |
| `internal/service/broadcast/factory.go` | `CreateMessageSender` appelle `SetVeridianWorkspaceRepo(f.workspaceRepo)` sur les deux senders (câble le fallback workspace pixel) |
| `internal/service/workspace_service.go` | `UpdateWorkspace` recopie les settings champ par champ (allowlist) → +2 lignes pour propager `VeridianProviderClassRates` + `VeridianOpenPixelByClass`, sinon l'UID Settings→Cold outreach sauve sans persister (bug staging 2026-06-11). |

Fichier veridian dédié : `internal/service/broadcast/veridian_pixel_resolver.go`
(`veridianWorkspacePixelResolver` : mémoïse le workspace par batch, nil-safe,
best-effort sur fetch). Test colocalisé `veridian_pixel_resolver_test.go`.

