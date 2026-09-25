# Custom tracking domain aligné au domaine d'envoi (Lot 5 cold outreach, 2026-06-15)

Les liens de tracking (pixel ouverture `/t/`, redirect clic `/r/`) doivent vivre sur
un sous-domaine ALIGNÉ au domaine d'envoi (envoi depuis `agences-veridian.fr` →
tracking `track.agences-veridian.fr`), pas sur `notifuse.app.veridian.site` global. Un
lien vers un domaine tiers est un signal anti-spam (mismatch perçu, casse l'alignement
DKIM/DMARC). Pratique standard cold (Lemlist/Instantly). Spec : ticket
`todo/2026-06-14-tracking-sous-domaine-aligne-domaine-envoi.md` (Lot A — code Notifuse ;
Lot B DNS/Traefik = ticket infra séparé, hors scope code).

- **Granularité = PAR INFRA d'envoi** (`EmailProvider.VeridianTrackingDomain`), cohérent
  avec les rates/caps par infra (R2) : l'infra EST le domaine d'envoi (host/IP/senders).
- **Cascade** (du plus spécifique au plus général) : `infra (EmailProvider.VeridianTrackingDomain)`
  [NOUVEAU] → `workspace (WorkspaceSettings.CustomEndpointURL)` [upstream, déjà résolu en
  amont par orchestrator/services] → `global (config.APIEndpoint)`. Premier non vide gagne.
  Le niveau workspace>global est DÉJÀ collapsé dans le param `endpoint` passé aux senders ;
  le helper applique uniquement l'override infra par-dessus.
- **Aucun patch sur `template_compilation.go`** : le mécanisme upstream alimente déjà
  les liens via `TrackingSettings.Endpoint` (`GenerateHTMLOpenTrackingPixel` /
  `GenerateEmailRedirectionEndpoint`). On se contente d'alimenter ce champ avec
  l'endpoint résolu infra dans les senders broadcast.
- **Fichier veridian** : `internal/domain/veridian_tracking_domain.go`
  (`VeridianResolveTrackingEndpoint(provider, resolvedEndpoint)` : override infra par-dessus
  l'endpoint déjà résolu ; normalise domaine nu → `https://`, strip slash final, préserve
  `http://` explicite ; provider nil OU champ vide = fallback strict, NON-RÉGRESSION).
  Test colocalisé `veridian_tracking_domain_test.go` (cascade complète + non-régression +
  liens réels générés via les générateurs `/t/` et `/r/`).
- **Pas de migration, pas d'allowlist** : `EmailProvider` est persisté comme JSON blob
  (`integrations` column, affecté par valeur dans Create/UpdateIntegration), le champ
  `omitempty` passe automatiquement — comme les 3 champs R2.
- **UI à venir** (agent uifix) : config tracking domain par infra dans Settings →
  Integrations (par EmailProvider), à côté des rates/caps R2.

⚠️ **Diffs INLINE supplémentaires** (tracking domain par infra) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +1 champ `EmailProvider` : `VeridianTrackingDomain` (string, omitempty) |
| `internal/service/broadcast/queue_message_sender.go` | `buildQueueEntry` : `TrackingSettings.Endpoint = domain.VeridianResolveTrackingEndpoint(emailProvider, endpoint)` (était `endpoint` nu) |
| `internal/service/broadcast/message_sender.go` | `SendToRecipient` : `TrackingSettings.Endpoint = domain.VeridianResolveTrackingEndpoint(emailProvider, endpoint)` (était `endpoint` nu) |

