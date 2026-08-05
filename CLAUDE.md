# notifuse-veridian — fork Veridian de Notifuse

> ## 🔴 Règle d'or Veridian — zéro contournement (gravée Robert 2026-06-10)
> **Interdit absolu** : cron bricolé, SQLite/store parallèle, job maison pour
> ÉVITER l'API ou la DB réelle de l'app. On travaille AVEC le vrai système :
> coder propre → tester staging → fixer la logique → MAJ DB staging si besoin
> → test lourd → push prod. Un blocage (accès, credential) se débloque via le
> lead, il ne se contourne pas. Détail : CLAUDE.md racine veridian-platform.


> Fork du projet [Notifuse](https://github.com/Notifuse/notifuse). Sync régulière
> via `git pull upstream main` sur la branche `veridian`. Tech stack upstream
> (Go 1.25 + Postgres 17 + React 18 + Vite + Ant Design) : voir le `README.md`
> et la doc Notifuse amont — pas reprise ici pour pas polluer le contexte agent.

---

## Customizations Veridian

### Branche et workflow

- **Branche de travail** : `veridian` (push direct OK, CI obligatoire)
- **Branche upstream tracking** : `main` (sync depuis `upstream/main`, jamais de commit Veridian direct)
- **Tags** : `v<upstream>-veridian.<sha8>` générés par CI (`veridian-ci.yml`)

### Convention `veridian_*.go` flat

Tout code custom dans `internal/{http,service,repository,domain}/` est préfixé
`veridian_*.go` au **même niveau** que les fichiers upstream — pas de
sous-dossier `internal/http/veridian/`.

Raisons : idiomatique Go (packages plats par responsabilité), pas d'import
cycle, grep-friendly (`ls internal/http/veridian_*`), sync upstream triviale
(aucun fichier upstream ne porte ce préfixe).

Règle stricte : **ne jamais patcher un fichier upstream** pour les besoins
Veridian. Si un handler upstream doit être étendu, créer
`veridian_<nom>_handler.go` qui wraps/remplace, et router dessus depuis le mux
Veridian.

Exemples existants : `internal/http/veridian_handler.go`,
`veridian_autologin_handler.go`, `veridian_magic_handler.go`,
`internal/domain/veridian.go`, `veridian_token.go`.

### Throttle par provider destinataire (cold outbound, 2026-06-10)

Second étage de rate-limiting keyé par **classe de provider destinataire**
(`google` / `microsoft` / `yahoo_aol` / `freemail_fr` / `corporate`), en
amont du throttle émetteur upstream qui reste intact. Spec : ticket
`todo/2026-06-02-throttle-par-provider-destinataire.md` (archivé dans
`todo/done/`).

- **Fichiers veridian** : `internal/domain/veridian_provider_class.go`
  (classification V1 par suffixe, parsing config, contrat tag contact
  `custom_string_5`), `internal/service/queue/veridian_provider_rate_limiter.go`
  (clone du pattern `IntegrationRateLimiter`, clé `integrationID|classe`),
  `internal/service/queue/veridian_provider_throttle.go` (gate worker
  skip-and-reschedule, pattern circuit breaker : `SetNextRetry` sans
  incrément d'attempts, pas de head-of-line blocking).
- **Config** : par broadcast via `broadcast.metadata["veridian_provider_class_rates"]`
  (map classe → emails/minute, fractions OK : `0.5` = 1 mail/2 min), fallback
  workspace settings `veridian_provider_class_rates`. Aucune config = no-op
  strict (non-régression upstream).

⚠️ **Diffs INLINE sur fichiers upstream** (à re-vérifier à chaque merge
upstream, le pre-push bypass `@notifuse.com` ne les protège pas) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_queue.go` | +2 champs `EmailQueuePayload` : `VeridianProviderClass`, `VeridianProviderClassRates` (JSONB, omitempty) |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings` : `VeridianProviderClassRates` (JSON, omitempty) |
| `internal/service/queue/worker.go` | +champ `providerClassLimiter` + init constructeur + gate `veridianProviderClassGate` dans `processEntry` (avant `MarkAsProcessing`, après circuit breaker) |
| `internal/service/broadcast/queue_message_sender.go` | +2 appels `domain.VeridianApplyProviderThrottle(entry, broadcast, contact)` (SendBatch + SendToRecipient) |

### Plafond JOURNALIER d'envoi (cold outbound, 2026-06-14)

Troisième étage : un **plafond journalier durable**, en amont du throttle minute
(qui est un token-bucket EN MÉMOIRE incapable de garantir un cap/jour : il se
réinitialise au redémarrage worker). Spec : ticket
`todo/2026-06-14-tunnel-vente-ui-controle-et-roadmap.md` section R0.

Deux plafonds, **le plus restrictif gagne** :
- **cap par DESTINATAIRE** (anti-harcèlement) : max N envois/jour vers une même
  adresse.
- **cap par CLASSE** (réputation) : max N envois/jour vers toute une classe
  (`google`/`microsoft`/`yahoo_aol`/`freemail_fr`/`corporate`).

- **Source de vérité = `message_history`** (table v21, peuplée à chaque envoi) :
  PAS de table compteur, PAS de cron de reset. Le "jour" = `sent_at >= minuit UTC`
  calculé à la lecture (`COUNT(*)`). Survit aux redémarrages worker.
- **Fichiers veridian** : `internal/service/queue/veridian_daily_cap.go` (gate
  `veridianDailyCapGate`, méthode du worker → accès `messageHistoryRepo` sans
  nouvelle DI ; même contrat skip-and-reschedule que le throttle minute :
  `SetNextRetry` sans incrément d'attempts, re-check borné à 1h ; best-effort —
  une erreur de COUNT ne bloque jamais l'envoi). Extensions de
  `veridian_provider_class.go` : `VeridianProviderClassDailyCapFromMetadata`,
  `VeridianPerRecipientDailyCapFromMetadata`, `VeridianDomainsForClass` (la
  classe n'est PAS stockée en DB → dérivée à la lecture par liste de domaines ;
  `corporate` = exclusion des domaines connus).
- **Repo** : 2 méthodes ajoutées à `MessageHistoryRepository` :
  `CountSentSinceForContact` (cap destinataire, indexé) +
  `CountSentSinceForDomains(domains, exclude, since)` (cap classe, filtre
  `lower(split_part(contact_email,'@',2))` `= ANY`/`<> ALL`). Décorateur quota +
  mock régénérés.
- **Migration V49** (workspace-only, additive, idempotente, PAS de CONCURRENTLY
  car migrations en TX) : index `message_history(contact_email, sent_at)` +
  `(sent_at)`. Idem dans `init.go` pour les nouveaux workspaces. `config.VERSION`
  bumpé 48.0 → 49.0 + fixture `manager_test`.
- **Config** : `broadcast.metadata["veridian_provider_class_daily_cap"]`
  (map classe→int/jour) + `["veridian_per_recipient_daily_cap"]` (int/jour),
  fallback workspace settings homonymes. Absent/0 = **pas de plafond** (opt-in
  strict, non-régression). Unité **/jour** distincte du **/min** des rates.
- **Perf (décision lead)** : COUNT live, pas d'agrégat. Le cap destinataire est
  index-only. Le cap classe filtre d'abord par `sent_at` (index) ; négligeable
  sur le volume cold quotidien. Si le cap classe devient un point chaud →
  matérialiser la classe sur `message_history` (colonne + index), pas avant.

⚠️ **Diffs INLINE supplémentaires** (plafond journalier) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_queue.go` | +2 champs `EmailQueuePayload` : `VeridianProviderClassDailyCap` (map[string]int), `VeridianPerRecipientDailyCap` (int) — JSONB, omitempty |
| `internal/domain/workspace.go` | +2 champs `WorkspaceSettings` : `VeridianProviderClassDailyCap`, `VeridianPerRecipientDailyCap` (JSON, omitempty) |
| `internal/domain/message_history.go` | +2 méthodes interface `MessageHistoryRepository` : `CountSentSinceForContact`, `CountSentSinceForDomains` |
| `internal/repository/message_history_postgre.go` | +2 impl COUNT (cap destinataire + cap classe par domaines) |
| `internal/service/queue/worker.go` | +gate `veridianDailyCapGate` dans `processEntry` (après le throttle minute, avant `MarkAsProcessing`) |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +2 lignes propageant les caps (sinon UI Settings sauve sans persister, cf. bug pixel 2026-06-11) |
| `internal/database/init.go` | +2 `CREATE INDEX` message_history pour les nouveaux workspaces |
| `config/config.go` | `VERSION` 48.0 → 49.0 |

### Open tracking — pixel email.opened PAR CLASSE (cold outbound, 2026-06-11)

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

### UI console — section Settings « Cold outreach » (2026-06-11)

Expose la config tunnel dans la console React (admin sans curl). Composants
veridian purs (préfixe identique au backend) :
- `console/src/components/settings/veridian_cold_outreach_settings.tsx` : édition
  débits + pixel par classe (lecture seule non-owner), save via
  `POST /api/workspaces.update`.
- `console/src/components/broadcasts/veridian_broadcast_rates_info.tsx` : affichage
  lecture seule des rates posés sur `broadcast.metadata`.
- Branchés : `SettingsSidebar` (+section `cold-outreach`), `WorkspaceSettingsPage`
  (case), `UpsertBroadcastDrawer` (tab content). Types front
  `WorkspaceSettings.veridian_*` + constantes dans `console/src/services/api/workspace.ts`.
- ⚠️ **Piège SW cache** (cf. memory project_notifuse_console_sw_cache) : valider
  toute nouvelle UI staging avec `?cachebust=` sinon l'ancien bundle est servi.

### Breakdown contacts par classe de provider (R1 cold outreach, 2026-06-14)

Endpoint `POST` + `GET` `/api/veridian/contacts.providerBreakdown` : compte les
contacts par classe de provider destinataire pour dimensionner le throttle. Spec :
ticket `todo/2026-06-14-tunnel-vente-ui-controle-et-roadmap.md` (section R1).

- **Auth** : JWT console (`RequireAuth`) + `AuthenticateUserForWorkspace` +
  permission `contacts:read` (gardien dans le service, comme `contacts.list`).
  PAS de HMAC Hub — endpoint console interne.
- **Params** : `workspace_id` (requis), `list_id` (optionnel, filtre liste via
  EXISTS subquery hors soft-delete).
- **Réponse** : `{"breakdown":{"google":N,"microsoft":N,"yahoo_aol":N,"freemail_fr":N,"corporate":N},"total":N}`
  (5 classes canoniques toujours présentes, 0 si vide).
- **Classification** : réutilise `veridian_provider_class.go` à l'identique
  (override `custom_string_5` prime, sinon suffixe domaine, sinon corporate).
  Repo projette `(email, custom_string_5)`, classification en Go (pas de CASE SQL
  → zéro duplication de la table de domaines). Pas de migration (PK email existant).
- **Fichiers veridian** (flat, zéro patch upstream) :
  `internal/domain/veridian_provider_breakdown.go`,
  `internal/repository/veridian_contact_breakdown_postgres.go`,
  `internal/service/veridian_contact_breakdown_service.go`,
  `internal/http/veridian_contact_breakdown_handler.go` (+ tests colocalisés +
  mocks `mock_veridian_contact_breakdown_{repository,service}.go`). Câblé dans
  `internal/app/app.go` (bloc "R1 breakdown contacts").
- ⚠️ **POST ET GET routés explicitement** (piège catchall `root_handler.go`).
- **UI à venir** (agent uifix) : afficher "X contacts" par carte dans
  `veridian_cold_outreach_settings.tsx`.

### Rates + caps par INFRA d'envoi (R2 cold outreach, 2026-06-14)

Ajoute un niveau **INFRA** au MILIEU des cascades de config cold outbound. L'infra
d'envoi = l'intégration `EmailProvider` (host/port/IP/relai SMTP + senders + son
rate). Une IP en warm-up porte ses propres débits/plafonds, indépendants du
workspace. Spec : ticket `todo/2026-06-14-...roadmap.md` (section R2).

- **Cascade étendue** (du plus spécifique au plus général) : `broadcast (payload)`
  → **`infra (EmailProvider)`** [NOUVEAU] → `workspace settings` → rien = no-op.
  S'applique à la fois aux **rates** (emails/min, `veridian_provider_throttle.go`)
  ET aux **caps journaliers** (classe + destinataire, `veridian_daily_cap.go`).
  Premier niveau non vide gagne (pas de merge par classe entre niveaux). Pour les
  caps, cap-classe et cap-destinataire sont résolus INDÉPENDAMMENT.
- **Pas de migration, pas de bug de propagation** : `EmailProvider` est affecté
  PAR VALEUR dans `CreateIntegration`/`UpdateIntegration` (`updatedIntegration.EmailProvider = req.Provider`)
  et persisté comme **JSON blob** (`integrations` column). Les 3 nouveaux champs
  `omitempty` passent donc automatiquement — AUCUNE allowlist champ-par-champ à
  étendre (contrairement à `UpdateWorkspace` pour les settings). `workspace_service.go`
  NON touché.
- **Plomberie worker** : le worker a déjà `integration := workspace.GetIntegrationByID(...)`
  en main avant les gates (worker.go:261) → il passe `&integration.EmailProvider`
  aux deux gates sans requête supplémentaire. Signatures :
  `veridianProviderClassGate(workspace, provider, entry)` et
  `veridianDailyCapGate(workspace, provider, entry)`. `provider == nil` (legacy) →
  niveau infra sauté = comportement pré-R2 strictement inchangé.
- **Fichiers veridian** : `veridian_provider_infra_cascade_test.go` (tests cascade
  3 niveaux rates + caps). Helpers de résolution : `veridianResolveProviderClassRates`
  (throttle) + `veridianResolveDailyCaps` étendu (daily cap, fichier capdaily).
- **UI à venir** (agent uifix) : config rates/caps par infra dans
  Settings → Integrations (par EmailProvider).

⚠️ **Diffs INLINE supplémentaires** (rates+caps par infra) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +3 champs `EmailProvider` : `VeridianProviderClassRates` (map[string]float64), `VeridianProviderClassDailyCap` (map[string]int), `VeridianPerRecipientDailyCap` (int), tous `omitempty` |
| `internal/service/queue/worker.go` | 2 call-sites : `veridianProviderClassGate` + `veridianDailyCapGate` reçoivent `&integration.EmailProvider` (param `provider` ajouté) |

### Profils d'envoi Gmail par mot de passe d'application (2026-08-04)

La console expose un parcours dédié `Gmail + mot de passe d'application` en plus
du SMTP avancé et des autres providers. Le preset verrouille `smtp.gmail.com:587`,
STARTTLS, basic auth, crée le sender depuis l'adresse Gmail et limite le profil à
1 email/minute par défaut. Plusieurs intégrations email restent possibles dans un
même workspace ; les actions `Use for Marketing` et `Use for Transactional`
sélectionnent explicitement le profil actif.

- Le secret de 16 caractères est normalisé sans espaces et chiffré avant
  persistance. Il n'est jamais réaffiché en clair.
- Une édition avec le champ secret vide préserve le ciphertext existant pour tous
  les providers email concernés, au lieu d'effacer silencieusement le credential.
- Les réponses workspace ne retournent ni secret clair ni ciphertext. Elles
  exposent seulement `veridian_credentials_configured` et, pour SMTP, les flags
  `has_password` / `has_oauth2_client_secret` /
  `has_oauth2_refresh_token`. Ces flags API sont nettoyés avant persistance.
- Le test d'un profil sauvegardé passe uniquement son `integration_id` : le
  serveur hydrate le secret en mémoire. Un succès persiste
  `veridian_transport_verified_at`; seuls les profils vérifiés entrent dans un
  pool explicite, sauf le singleton marketing legacy déjà actif (grandfather).
  La route est owner-only, borne le destinataire à l'email du owner connecté et
  applique un plafond dédié de 3 tests/heure/workspace/profil.
- Fichiers Veridian :
  `console/src/components/settings/veridian_email_profiles.ts`,
  `internal/service/veridian_email_provider_secrets.go` et tests 1:1.
- Diffs inline nécessaires :
  `console/src/components/settings/Integrations.tsx`,
  `internal/service/workspace_service.go`, `internal/service/email_service.go`.

### Redaction exhaustive des credentials workspace API (2026-08-05)

Toutes les réponses qui embarquent un `Workspace` passent par
`veridianRedactWorkspaceForAPI`. Le helper clone l'objet sans muter l'état runtime
puis retire le secret workspace chiffré, les clés FileManager claires/chiffrées,
les passwords IMAP clairs/chiffrés, les signatures Supabase claires/chiffrées,
les clés LLM/Firecrawl claires/chiffrées et tous les secrets EmailProvider.

- Les seuls états non sensibles ajoutés sont `file_manager.has_secret_key`, les
  `has_signature_key` des deux hooks Supabase et les flags email déjà existants.
- Les mises à jour avec un champ secret vide préservent le ciphertext stocké ;
  les flags de réponse sont nettoyés avant persistance.
- Le FileManager historique parle directement à S3 depuis le navigateur. Il ne
  peut donc plus réutiliser une clé existante après redaction. Aucun workspace
  PROD n'avait de FileManager configuré lors de la vérification live préalable ; la
  remise en service future exige un proxy S3 backend (dette P1), pas le retour du
  secret dans l'API.

### Custom tracking domain aligné au domaine d'envoi (Lot 5 cold outreach, 2026-06-15)

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

### UI config cold self-service + câblage API IMAP (Lot 8 cold outreach, 2026-06-15)

Expose dans la console (Settings → Cold outreach) ce qui restait curl-only, et
COMBLE le trou qui rendait l'IMAP inconfigurable en self-service. Spec : volet UI
+ giga E2E du sprint cold.

- **Câblage API IMAP self-service (le vrai chantier)** : Lot 1 avait livré le
  DOMAIN IMAP (struct `IMAPSettings`, `Validate`/`Encrypt`/`Decrypt`, poller,
  consumers, `case IntegrationTypeIMAP` dans Validate/BeforeSave/AfterLoad de
  `workspace.go`) MAIS le chemin API public était mort : `CreateIntegrationRequest`
  / `UpdateIntegrationRequest` n'avaient pas de champ `imap_settings`, et le service
  `CreateIntegration`/`UpdateIntegration` n'avait pas de `case IntegrationTypeIMAP`.
  Résultat avant fix : `POST /api/workspaces.createIntegration {type:"imap"}` →
  `400 {"error":"unsupported integration type: imap"}` (prouvé E2E staging). Le
  Lot 8 ajoute ces 3 maillons → IMAP configurable de bout en bout (bounce-loop +
  stop-on-reply activables par un non-dev). Update préserve l'`EncryptedPassword`
  existant si le password clair n'est pas re-fourni (pattern Supabase/LLM/Firecrawl).
- **UI** (`console/src/components/settings/veridian_cold_outreach_settings.tsx`,
  fichier veridian existant étendu) : 2 sous-composants colocalisés —
  `IMAPInboxCard` (CRUD intégration IMAP via createIntegration/updateIntegration,
  password jamais pré-rempli, vide à l'édition = inchangé) et `TrackingDomainCard`
  (édite `EmailProvider.veridian_tracking_domain` PAR INFRA, renvoie le provider
  COMPLET pour conserver senders/rate_limit). Non-owner = lecture seule. Breakdown
  R1 + 11 classes MX déjà présents (R1/Lot 4) — non touchés. Tests colocalisés
  étendus (13 tests, dont 5 neufs IMAP/tracking ; 2 tests pré-cassés Lot 4 réparés
  — matchers exacts au lieu de `getByText(/Corporate/)` ambigu, `BIG_PROVIDERS`=3).
- **Types front** (`console/src/services/api/workspace.ts`) : +interface
  `IMAPSettings`, `IntegrationType` += `'imap'`, `Integration`/`CreateIntegrationRequest`/
  `UpdateIntegrationRequest` += `imap_settings?`, `EmailProvider` += champs R2
  (`veridian_provider_class_rates`/`_daily_cap`/`_per_recipient_daily_cap`) +
  `veridian_tracking_domain` (alignement type front sur le Go).
- **Giga E2E** (`tests/e2e-veridian/specs/cold-config.spec.ts`) : niveau `@prod-safe`
  (routes breakdown POST+GET / createIntegration / updateIntegration montées + rejet
  auth, anti-catchall — 4/4 verts staging) + niveau mutation staging (provision
  workspace jetable → create/update IMAP → reload → persistance + password jamais en
  clair ; tracking domain posé + senders conservés ; breakdown JSON cohérent par
  classe). AUCUN envoi de mail réel. Mutation skip si `HUB_API_SECRET` absent (prod).

### E2E lifecycle cold — stop-on-reply + cap destinataire (2026-06-15)

Valide en E2E DÉROULÉ deux comportements cold que seul l'unitaire couvrait :
réponse reçue → contact `replied` → sortie de séquence (Lot 3/9) ; et 2e envoi
vers une adresse déjà au quota du jour → bloqué par le cap destinataire (V49).

Problème : ni `VeridianReplyService.ProcessInboundMessage` (appelé QUE par le
poller IMAP) ni le gate `veridianDailyCapGate` (worker, lit `message_history`
peuplé QUE par un vrai envoi SMTP) ne sont atteignables par HTTP. Et CONTRAINTE
Robert : zéro mail réel. Voie propre retenue (pas de mock mensonger, pas de
contournement) : **endpoint de test staging-only** `POST /api/veridian/admin/cold-simulate`
(HMAC Hub, 503 hors staging — même garde-fou que `test-tenants-stats`) qui frappe
le VRAI code métier sans transport IMAP ni SMTP.

- **Fichier veridian** : `internal/http/veridian_cold_simulate_handler.go` (+ test
  colocalisé). 6 modes (3 d'origine ci-dessous + 3 ajoutés par la batterie garde-fous
  2026-06-17 : `class_cap_decision`, `per_sender_cap_decision`, `sending_window_decision`) :
  - `inbound_reply` : seed un `message_history` "sent" puis passe un
    `VeridianIMAPMessage` (In-Reply-To = id de l'envoi) au VRAI `ProcessInboundMessage`
    → match fort Message-ID → signal `replied` + exit automations. Renvoie `has_replied`.
  - `seed_sent` : pose N (≤50) entrées `message_history` "sent" via le VRAI repo
    `Create` (secret key workspace = `workspace.Settings.SecretKey`, comme le worker).
  - `daily_cap_decision` : renvoie le COUNT réel (`CountSentSinceForContact` depuis
    minuit UTC) + `would_be_capped` = `count >= cap`, le **prédicat exact** du gate.
- **DI** : `VeridianHandler.SetColdSimulate(replyService, messageHistoryRepo, workspaceRepo, environment)`
  câblé dans `app.go` (staging-only). `coldSimulate` nil = 503.
- **Spec** : `tests/e2e-veridian/specs/cold-lifecycle.spec.ts` — `@prod-safe`
  (route montée + gate staging) + `@cold` mutation staging (provision jetable →
  réponse simulée → `replied` + event timeline `email.replied` ; cap : sous cap →
  pass, seed 1 envoi, au quota → bloqué, relèvement cap → repass). AUCUN mail réel.
- **Non-couvrable en E2E pur** (assumé honnêtement) : l'**exit ACTIF des automations**
  (passage `ContactAutomation` à `exited`/`replied`) n'est pas observé en E2E faute
  d'endpoint d'enrôlement de contact en automation (l'enrôlement passe par un trigger
  SQL, pas d'API). Couvert unitairement (`veridian_reply_service_test.go`). L'E2E
  valide le SIGNAL `replied` (source de vérité que le gate Lot 9 consomme) + l'event
  timeline. Le throttle MINUTE n'est pas re-testé ici (token-bucket RAM, déjà unitaire).

⚠️ **Diffs INLINE supplémentaires** (câblage API IMAP self-service) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/workspace.go` | +1 champ `CreateIntegrationRequest.IMAPSettings` + `UpdateIntegrationRequest.IMAPSettings` (`*IMAPSettings`, `json:"imap_settings,omitempty"`) ; +`case IntegrationTypeIMAP` dans `CreateIntegrationRequest.Validate` (le `default:` rejetait IMAP) + branche IMAP dans `UpdateIntegrationRequest.Validate` |
| `internal/service/workspace_service.go` | +`case IntegrationTypeIMAP` dans `CreateIntegration` (assigne `IMAPSettings`) ET `UpdateIntegration` (préserve `EncryptedPassword` si password clair non re-fourni) |

### Batterie E2E on-premise — garde-fous anti-cramage de domaine (2026-06-17)

Harness rejouable qui PROUVE en conditions réelles (vrai worker staging, vraie DB
staging, vrai SMTP → sink local `smtp-sink`) que les **10 gates de protection du
domaine** tiennent, AVANT tout envoi cold prod. Spec : ticket
`todo/2026-06-17-batterie-e2e-onpremise-garde-fous-domaine.md` (P0). 🔴 ZÉRO mail
externe : SMTP cible = `smtp-sink:1025` (aiosmtpd `-n -d`, cul-de-sac sur dev-pub,
réseau `notifuse-staging_notifuse-internal`) ; double-check du host AVANT tout
envoi + vérif finale que le relai sortant `mail-relay` n'a vu AUCUN mail.

- **Harness** (réutilisable, à rejouer avant chaque campagne) :
  `scripts/e2e/cold-garde-fous.sh` (orchestrateur : provision workspace jetable
  `gfcheck<stamp>` + intégration SMTP→sink 2 senders + double-check host + wipe) +
  `scripts/e2e/cold-garde-fous-gates.sh` (les 10 gates). Pour chaque gate : cas
  NON-RÉGRESSION (config absente = no-op) ET cas ENFORCED (config active = bloque/
  étale). Lit le sink (`docker logs smtp-sink`, QP-normalisé), `message_history` et
  la queue. Verdict PASS/FAIL par gate + récap. Env : `NOTIFUSE_HUB_API_SECRET`.
  6 gates prouvés par CAMPAGNE RÉELLE → sink (exclusion, throttle, pré-filtre,
  anti-hash+spintax, pixel par classe, round-robin) ; 4 gates état/temps via
  `cold-simulate` (prédicat exact) + enforcement worker (circuit breaker, daily cap,
  per-sender cap, sending window).

- **Extension `cold-simulate` (3 modes neufs)** : pour prouver les gates état/temps
  sans envoi réel ni mock, frappant le PRÉDICAT EXACT du gate worker :
  - `class_cap_decision` : `CountSentSinceForDomains(classe) >= class_cap`
    (cap-CLASSE de `veridian_daily_cap.go`). ⚠️ Isolation : le COUNT est réel et
    partagé par classe → un test doit lire le baseline ou utiliser une classe non
    polluée par la campagne (sinon faux "FAIL" : la classe a déjà des envois du jour).
    **Étendu 2026-06-18** : param optionnel `sender_domain` (domaine nu OU adresse
    dont on extrait le domaine, normalisé comme `veridianEmailDomain`). Fourni → le
    COUNT passe par `CountSentSinceForDomainsAndSenderDomain` = le prédicat EXACT de
    `veridianCountClassForInfra` (compteur par INFRA ÉMETTRICE). Absent → COUNT
    workspace-global `CountSentSinceForDomains` inchangé (non-régression). La réponse
    expose `sender_domain` (normalisé) + `per_infra` (bool, true = chemin par infra).
  - `per_sender_cap_decision` : `CountSentSinceForSender >= per_sender_cap` (warmup
    IP, `veridian_per_sender_cap.go`). `seed_sent` accepte désormais `sender_email`
    (pose `veridian_sender_email`).
  - `sending_window_decision` : `IsWithinWindow(now)` pur (`veridian_sending_window_gate.go`),
    renvoie `within`/`would_be_skipped`/`next_opening_unix`.
  - Fichier : `internal/http/veridian_cold_simulate_handler.go` (+ tests colocalisés).

- **Pièges vécus (gravés)** :
  - **Circuit breaker — race provider-switch** : pour exercer le circuit, on bascule
    le provider du workspace sur une intégration SMTP KO (port fermé). Les envois
    échoués sont RESCHEDULÉS (backoff) ; si on RESTAURE le provider sain avant la fin
    de l'observation, le worker re-tente avec le sink sain → les mails partent au sink
    (faux négatif). FIX : workspace jetable, on NE restaure PAS — le provider KO reste
    actif, les entrées restent en échec, 0 sink. Preuve = `failed_at`≥1 + 0 sink +
    logs `connection refused`.
  - **Dead-DNS du pré-filtre** : un domaine corporate « valide » de test doit être
    RÉSOLVABLE (A/MX) sinon le pré-filtre le coupe lui aussi → on utilise `example.com`
    (A records), pas `.example`/`.invalid` (NXDOMAIN, réservés au cas dead-DNS).
  - **QP wrapping au sink** : aiosmtpd imprime le HTML en quoted-printable (soft-break
    `=\n`, `=3D`) → dé-wrapper avant de grep `/t/`, `/r/`, spintax.

### Spintax — variation de contenu anti-empreinte (Lot 6 cold outreach, 2026-06-15)

Résout la syntaxe spintax `{option A|option B|option C}` du contenu email, PAR
DESTINATAIRE, avec un seed DÉTERMINISTE = l'email du contact. But cold : 500 mails
au HTML identique = signature spam triviale (fuzzy hashing) ; varier le corps casse
l'empreinte commune. Même destinataire re-rendu → même variante (debug, audit) ;
destinataires différents → variantes potentiellement différentes.

- **Package veridian dédié** : `pkg/veridian_spintax/veridian_spintax.go` —
  fonction pure `ResolveSpintax(input, seed string) string`. Parseur récursif
  descendant (FNV-1a sur le seed + finaliseur splitmix64 par index de groupe).
  Gère le **nesting** (`{Bonjour {Monsieur|Madame}|Salut}`), **préserve Liquid**
  (`{{ }}` et `{% %}` recopiés intacts, jamais interprétés comme spintax ; les
  accolades Liquid ne comptent pas dans l'équilibrage), **no-op STRICT** sans
  spintax (sortie = entrée à l'octet près), **robuste** aux accolades
  déséquilibrées (best-effort, zéro panic). Convention : `{{` collé = TOUJOURS
  Liquid (jamais nesting spintax) ; `{texte}` sans `|` = littéral préservé.
- **Point d'appel** (une ligne, zone disjointe du tracking Lot 5) :
  `pkg/notifuse_mjml/template_compilation.go` — résolution sur `mjmlString`
  complet APRÈS tout rendu Liquid et AVANT `preprocessMjmlForXML` (les `{{ }}`
  sont déjà remplacés, le spintax restant n'est que des accolades simples).
  Sujet + preview spintaxés juste après leur rendu Liquid (un sujet identique
  est aussi une signature spam). Helper `veridian_spintax_apply.go`
  (`veridianApplySpintax` : seed vide = no-op strict).
- **Graine câblée** = email du destinataire, dans les deux senders broadcast.
  Le sujet (rendu hors `CompileTemplate`) est spintaxé explicitement côté sender.

⚠️ **Diffs INLINE supplémentaires** (spintax) :

| Fichier upstream | Diff Veridian |
|---|---|
| `pkg/notifuse_mjml/template_compilation.go` | +1 champ `CompileTemplateRequest.VeridianSpintaxSeed` (string, omitempty) ; +appel `veridianApplySpintax` sur le corps (avant `preprocessMjmlForXML`) et sur subject/preview (après rendu Liquid) |
| `internal/service/broadcast/queue_message_sender.go` | `buildQueueEntry` : `CompileTemplateRequest.VeridianSpintaxSeed = email` + `subject = veridian_spintax.ResolveSpintax(subject, email)` |
| `internal/service/broadcast/message_sender.go` | `SendToRecipient` : `CompileTemplateRequest.VeridianSpintaxSeed = email` + `processedSubject = veridian_spintax.ResolveSpintax(processedSubject, email)` |

### Intégration IMAP self-service (Lot 1 sprint cold, 2026-06-15)

**BRIQUE FONDATRICE.** Notifuse poll LUI-MÊME une boîte IMAP de retour (creds
saisis 100 % via config/UI, zéro script externe). Deux lots downstream
consomment le poller comme handlers : **bounce-loop** (NDR Postfix) et
**stop-on-reply** (réponse humaine d'un prospect).

- **Lib** : `github.com/emersion/go-imap/v2` (même écosystème emersion que
  go-smtp/go-sasl déjà présents). Isolée dans un seul fichier adapter.
- **Creds** : sur l'`Integration` du workspace (type `imap`, struct
  `IMAPSettings`), persistés en **JSON blob** dans la colonne `integrations`,
  password chiffré au repos via le pattern SMTP (`EncryptString` /
  `DecryptFromHexString`, passphrase = `config.Security.SecretKey`). **Aucune
  migration pour les creds.** Config : host, port, username, password, useTLS,
  folder (défaut INBOX), polling_interval_seconds (borné min 30s).
- **Idempotence durable** : table **système** `veridian_imap_uid_seen`
  (migration **V50**), clé `(workspace_id, integration_id, folder, uid_validity,
  uid)`. `uid_validity` OBLIGATOIRE dans la clé (RFC 3501 : si le serveur change
  l'UIDVALIDITY, les anciens UID sont invalidés). Un UID n'est JAMAIS
  re-dispatché → survit aux redémarrages (PAS de store en mémoire).
- **Poller** : `internal/service/queue/veridian_imap_poller.go`
  (`VeridianIMAPPollerService`, goroutine + ticker calqué sur
  `VeridianIdempotencyCleanupService`). À chaque tick : `List()` workspaces →
  intégrations IMAP dues → dial (timeout court) → SEARCH SINCE (fenêtre 7j) →
  `FilterUnseen` → dispatch aux consumers (panic-isolé) → `MarkSeen`.
  **Best-effort de bout en bout** : échec dial/login/fetch d'une boîte = log +
  skip (jamais de crash) ; erreur DB sur uid_seen = skip la boîte (anti
  double-dispatch) ; panic d'un consumer = recovered, n'affecte pas les autres.
  Contrat **at-most-once dispatch** : un UID est marqué vu QUOI QU'IL ARRIVE
  (même si le consumer erreur) — les lots 2/3 doivent être idempotents côté
  métier.
- **Démarrage gated par consumer** : `Start()` est un **no-op tant qu'aucun
  consumer n'est enregistré** (poller une boîte pour dispatcher à personne =
  travail inutile + goroutine parasite). Le poller s'active dès que lot 2 ou 3
  appelle `RegisterConsumer(...)` dans le bloc `app.go` "poller IMAP" (avant
  `app.Start()`). État actuel (Lot 1 seul) = poller câblé mais dormant jusqu'à
  l'arrivée des lots downstream.
- **API publique consommée par lots 2/3** :
  `(*VeridianIMAPPollerService).RegisterConsumer(domain.VeridianIMAPConsumer)`.
  L'interface `domain.VeridianIMAPConsumer` = `{ Name() string ;
  OnNewMessage(*domain.VeridianIMAPMessage) error }`. DTO neutre
  `VeridianIMAPMessage` (UID, UIDValidity, Folder, WorkspaceID, IntegrationID,
  MessageID, InReplyTo, References, From, To, Subject, Date, RawBody) — découple
  les handlers de go-imap. Câblage : `a.veridianIMAPPoller` créé + démarré dans
  `app.go`, les lots 2/3 ajoutent leur `RegisterConsumer(...)` là où le poller
  est instancié (bloc "poller IMAP").
- **Fichiers veridian** (flat, préfixe respecté) :
  `internal/domain/veridian_imap_integration.go` (IMAPSettings + interfaces
  consumer/repo + DTO), `internal/service/queue/veridian_imap_client.go`
  (interface narrow `veridianIMAPClient`/`veridianIMAPDialer` + adapter
  emersion), `internal/service/queue/veridian_imap_poller.go` (poller),
  `internal/repository/veridian_imap_uid_seen_postgres.go` (repo idempotence),
  `internal/migrations/v50.go` (table système) + tests colocalisés + mocks
  `mock_veridian_imap_{consumer,uid_seen_repository}.go`.

⚠️ **Diffs INLINE supplémentaires** (intégration IMAP) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/workspace.go` | +1 champ `Integration.IMAPSettings *IMAPSettings` (omitempty) + 3 `case IntegrationTypeIMAP` dans `Integration.Validate` / `BeforeSave` / `AfterLoad` (validate+chiffre / chiffre / déchiffre le password) |
| `config/config.go` | `VERSION` 49.0 → 50.0 |
| `internal/migrations/manager_test.go` | fixture sqlmock `db_version` 49 → 50 |
| `internal/app/app.go` | +repo `veridianIMAPUIDSeenRepo` + service `veridianIMAPPoller` (création + `Start()` dans le bloc crons, disabled en demo) |

### Bounce-loop NDR IMAP → suppression contact (cold outbound, 2026-06-15)

Ferme la boucle de bounce du cold outbound : le relai SMTP Postfix self-hosted
renvoie les NDR (Non-Delivery Reports) ASYNCHRONES dans la boîte du Return-Path
du domaine d'envoi. Notifuse ne les voit pas via un webhook provider (il n'y en
a pas). Ce lot 2 consomme l'IMAP du Lot 1 (consumer enregistré sur le poller),
détecte les NDR et alimente la chaîne de suppression EXISTANTE — **rien n'est
réinventé**. Spec : ticket `todo/2026-06-14-bounce-loop-postfix-suppression-cold.md`.

- **Chaîne réutilisée** (zéro duplication de la suppression) :
  `NDR brut` → `veridian_ndr.Parse` → `domain.SMTPWebhookPayload{Event:"bounce"}`
  → `InboundWebhookEventService.ProcessWebhook` (EXISTANT) → `processSMTPWebhook`
  → `ClassifyBounce` → `MarkEmailsAsBounced` → `contact_lists.status='bounced'`
  → plus jamais renvoyé.
- **Parseur NDR** : `pkg/veridian_ndr/parser.go` (`Parse(raw, from, subject) Result`).
  Robuste : MIME structuré RFC 3464 (`multipart/report; report-type=delivery-status`,
  part `message/delivery-status` → `Status`, `Final-Recipient`, `Diagnostic-Code`)
  PUIS heuristiques de repli (From MAILER-DAEMON/postmaster, sujets NDR multi-langue,
  scan code DSN 5.x.x/4.x.x + adresse). Un message non-NDR (vraie réponse de
  prospect, auto-reply OOO) → `Result{IsNDR:false}`, jamais d'erreur (le Lot 3
  stop-on-reply le traite). Sévérité dérivée du code DSN : 5.x.x = hard, 4.x.x =
  soft. Fixtures de test : Postfix, Gmail, Outlook/Exchange, text/plain, garbage
  MIME, vraie réponse, auto-reply.
- **Consumer** : `internal/service/veridian_bounce_consumer.go`
  (`VeridianBounceConsumer` implémente `domain.VeridianIMAPConsumer`, `Name()` =
  "bounce-loop"). `OnNewMessage` résout l'intégration d'ENVOI SMTP du workspace
  (`GetIntegrationsByType(email)` filtré sur `Kind==smtp`) — car `ProcessWebhook`
  route sur `EmailProvider.Kind`, et le poller donne l'ID de la boîte IMAP de
  RÉCEPTION, pas celui du provider d'envoi. Best-effort de bout en bout (erreur
  loggée + retournée, le poller marque vu quoi qu'il arrive). **Idempotence
  métier** garantie en aval : `MarkEmailsAsBounced` est idempotent (UPDATE ...
  WHERE status NOT IN ('complained','bounced')) → rejouer le même NDR ne supprime
  qu'une fois.
- **Enregistrement** : `app.go` crée le consumer et appelle
  `veridianIMAPPoller.RegisterConsumer(...)` au point d'ancrage prévu par Lot 1
  (ce qui ACTIVE le poller — no-op tant qu'aucun consumer).
- **Pré-filtrage (lot A/B du ticket)** : la suppression empêchait DÉJÀ le
  ré-envoi via `GetContactsForBroadcast` / `CountContactsForBroadcast`
  (`cl.status <> 'bounced'/'complained'`), MAIS ces clauses étaient gatées par le
  flag optionnel `audience.ExcludeUnsubscribed` → un broadcast sans ce flag
  réincluait les bounced. **Corrigé** : `bounced`/`complained` (statuts terminaux)
  sont désormais TOUJOURS exclus, indépendamment du flag (qui ne gate plus que
  `unsubscribed`). Réputation #1 : on ne renvoie jamais à une adresse morte.
- **Classification hard/soft SMTP** : `processSMTPWebhook` force `BounceType="Bounce"`
  (libellé mort pour la classif). `ClassifyBounce` (cas SMTP) enrichi pour lire le
  **code DSN** dans `Subtype` (BounceCategory) puis `Diagnostic` : 5.x.x → Hard
  (suppression immédiate), 4.x.x → SoftCount (seuil). Sans ce diff, un hard bounce
  cold n'aurait supprimé qu'après 5 occurrences.

⚠️ **Diffs INLINE supplémentaires** (bounce-loop) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/bounce_classification.go` | +helper `classifyDSNCode` (regex code DSN enrichi → Hard/Soft) + cas `EmailProviderKindSMTP` enrichi : à défaut de `hardbounce`/`softbounce`, classe par code DSN du subtype puis du diagnostic. Additif, ne change aucun mapping existant. |
| `internal/repository/contact_postgres.go` | `GetContactsForBroadcast` + `CountContactsForBroadcast` : clauses `status <> 'bounced'` et `<> 'complained'` SORTIES du `if ExcludeUnsubscribed` (toujours appliquées). L'ordre des `$N` change (Bounced, Complained, puis Unsubscribed conditionnel) → `WithArgs` des tests réordonnés. |
| `internal/app/app.go` | +création `VeridianBounceConsumer` + `veridianIMAPPoller.RegisterConsumer(...)` au point d'ancrage Lot 1 (active le poller). |

Fichiers veridian dédiés : `pkg/veridian_ndr/{parser.go,parser_test.go}`,
`internal/service/veridian_bounce_consumer{,_test}.go`.

### Stop-on-reply — réponse prospect → exit séquence (Lot 3 sprint cold, 2026-06-15)

Réflexe cold #1 de décence/réputation : si un prospect RÉPOND, on **arrête
immédiatement** de le relancer. Second consumer du poller IMAP (Lot 1), à côté
du bounce-loop (Lot 2). Spec : mission Lot 3.

- **Détection de réponse** (`internal/domain/veridian_reply_detection.go`, PURE) :
  - **MATCH FORT (privilégié)** par Message-ID. À l'envoi on pose un header
    RFC822 déterministe `Message-ID: <{message_history.id}@{domaine}>` (cf. diff
    INLINE `smtp_service.go`). La réponse recopie ce Message-ID dans
    `In-Reply-To`/`References` → on en ré-extrait la local-part
    (`VeridianExtractMessageIDLocalParts`) = notre `message_history.id`, et on
    confirme que c'est NOTRE envoi vers CE contact via
    `MessageHistoryRepository.FindContactEmailByMessageID` (projection légère
    `contact_email` par id, **sans secretKey ni déchiffrement**).
  - **FALLBACK FAIBLE** : `From` = contact connu du workspace ET pas un NDR.
  - **NDR exclus** : un rapport de non-remise n'est JAMAIS une réponse — verdict
    délégué à `pkg/veridian_ndr.Parse` (parseur canonique PARTAGÉ avec le Lot 2,
    zéro divergence d'heuristique) + garde-fou `VeridianFromLooksLikeDaemon`
    (bloque MAILER-DAEMON/postmaster AVANT le match fort, robuste même si le
    RawBody est absent — cas où `veridian_ndr` rendrait `IsNDR=false`).
- **Signal durable** : table WORKSPACE `veridian_contact_reply` (migration V51,
  PK `contact_email`, `ON CONFLICT DO NOTHING`). PAS custom_string_5 (occupé par
  la classe), PAS contact_lists.status (sémantique abonnement). Source de vérité
  requêtée par `HasReplied`.
- **Action** (`internal/service/veridian_reply_service.go`) : pose le signal +
  timeline `email.replied` + **exit ACTIF** des automations actives du contact
  (`Status=Exited`, `ExitReason='replied'`, `IncrementAutomationStat("exited")`,
  timeline `automation.end`). Best-effort, **idempotent** (fast-path `HasReplied`
  + `ON CONFLICT` + exit borné aux automations `active` → re-dispatch IMAP = 1
  seul effet).
- 🔌 **CONTRAT exit-on-replied pour le Lot 9** : `VeridianReplyService` implémente
  `service.ColdReplyChecker` (`HasReplied(ctx, workspaceID, email) (bool, error)`,
  défini par le Lot 9 dans `veridian_cold_exit.go`). Branché dans `app.go` via
  `automationExecutor.SetColdReplyChecker(a.veridianReplyService)`. Le gate Lot 9
  (PASSIF, à chaque tick) ET l'exit actif (PUSH, à la détection) sont
  complémentaires : le gate rattrape ce que l'exit actif raterait (best-effort).
- **Consumer** : `VeridianReplyConsumer` (`Name()="stop-on-reply"`) implémente
  `domain.VeridianIMAPConsumer`, enregistré dans `app.go` via `RegisterConsumer`.

⚠️ **Diffs INLINE supplémentaires** (stop-on-reply) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/smtp_service.go` | +pose `Message-ID` RFC822 déterministe `<message_id@from-domain>` via `msg.SetMessageIDWithValue(...)` (au lieu de l'aléatoire go-mail), pour rendre les réponses matchables. Vide => fallback go-mail (non-régression). Construit par `veridian_send_message_id.go`. |
| `internal/domain/message_history.go` | +1 méthode interface `MessageHistoryRepository.FindContactEmailByMessageID` (projection `contact_email` par id, match fort stop-on-reply). |
| `internal/repository/message_history_postgre.go` | +impl `FindContactEmailByMessageID` (SELECT contact_email WHERE id, found bool). |
| `internal/repository/veridian_message_history_decorator.go` | +passthrough `FindContactEmailByMessageID` (lecture, aucun side-effect quota). |
| `internal/database/init.go` | +`CREATE TABLE veridian_contact_reply` pour les workspaces créés après V51. |
| `internal/app/app.go` | +repo `veridianContactReplyRepo` + service `veridianReplyService` + `SetColdReplyChecker` sur l'executor + `RegisterConsumer(veridianReplyConsumer)`. |
| `config/config.go` | `VERSION` 50.0 → 51.0. |

Fichiers veridian dédiés : `internal/domain/veridian_reply_detection.go`,
`internal/domain/veridian_contact_reply.go`,
`internal/repository/veridian_contact_reply_postgres.go`,
`internal/service/veridian_reply_service.go`,
`internal/service/veridian_reply_consumer.go`,
`internal/service/veridian_send_message_id.go`,
`internal/migrations/v51.go` (+ tests colocalisés + mock
`mock_veridian_contact_reply_repository.go`).

### Classification destinataire par MX RÉEL (Option A, Lot 4, 2026-06-14)

Le throttle/cap par classe était **aveugle** : la classe dérivait du **suffixe**
de domaine → ~70% des leads B2B jetés en `corporate` alors qu'ils sont hébergés
Google Workspace / M365 / OVH → on tapait Google/Microsoft à plein régime sans
throttle = réputation grillée. Le fix résout le **MX réel** du domaine (le DNS
dit où le mail atterrit) et mappe le **hostname MX** à une classe via une **table
de patterns versionnée**. Spec : ticket
`todo/2026-06-14-classification-mx-table-patterns-option-A.md`.

- **11 classes** (5 historiques + 6 MX, `VeridianAllProviderClasses()`) :
  `google` · `microsoft` · `yahoo_aol` · `freemail_fr` · `corporate` (suffixe
  inconnu AVANT MX, rétrocompat) **+** `ovh` · `ionos` · `apple_icloud` ·
  `security_gateway` (anti-spam pro → débit ultra-prudent) · `other_hoster`
  (infomaniak/gandi/zoho/proton…) · `corporate_selfhost` (MX inconnu, fallback).
  Toutes acceptées par `IsValidProviderClass` (un seul set = source de vérité
  pour throttle/cap/pixel/breakdown). Les 5 historiques inchangées (non-régression).
- **Hot path rapide** : `ClassifyProviderClass(email)` reste PURE (suffixe seul,
  zéro I/O — call-sites historiques inchangés). La couche MX vit dans
  `internal/domain/veridian_provider_class_mx.go` (`VeridianMXClassifier`) :
  suffixe connu → classe directe SANS lookup ; suffixe inconnu → MX caché.
  Le worker classe via `veridianClassifyRecipient(entry)` (tag amont prime, sinon
  MX) dans les DEUX gates (throttle minute + daily cap).
- **Resolver** : `MXResolver` (interface DI, mockable en test). Prod =
  `net.Resolver` forcé sur **8.8.8.8 / 1.1.1.1** (le resolver local conteneur est
  instable). Timeout court **2s**, best-effort STRICT : échec/timeout/NXDOMAIN/
  pattern inconnu → `corporate_selfhost`, **jamais de blocage d'envoi**.
- **Cache** : **in-memory** domaine→classe, TTL **7 jours** (MX changent rarement),
  thread-safe (RWMutex). PAS de table DB → **PAS de migration, `config.VERSION`
  NON bumpé** : les MX sont une donnée d'infra GLOBALE (pas par workspace), une
  table par workspace serait fausse ; le worker est long-lived ; le
  pré-remplissage massif (7,8M) se fait via le tag contact `custom_string_5`
  (override option B, posé à l'import par Prospection → ticket séparé) qui
  court-circuite tout lookup. Cache partagé inter-process = à matérialiser SI/quand
  mesuré nécessaire, pas avant.
- **Table de patterns MX→classe** : `veridianMXPatternTable` dans
  `veridian_provider_class_mx.go`, dérivée de la VRAIE data (email_verification
  48k + prospection prod 286k, cf. `docs/PROVIDERS-DESTINATAIRES-CARTOGRAPHIE.md`).
  Match **case-insensitive** sur **suffixe** du hostname MX. Ordre significatif :
  **gateways anti-spam testées EN PREMIER** (elles frontent un MX d'entreprise).
  Pour l'étendre : ajouter une ligne (suffixe lowercase OBSERVÉ en data, pas deviné).
- **Daily cap par classe — dégradation gracieuse documentée** : `VeridianDomainsForClass`
  renvoie une liste VIDE pour les classes MX (un domaine custom n'est rangé là que
  par son MX, NON stocké en DB) → le COUNT-par-domaine du cap-CLASSE ne s'enforce
  PAS pour ovh/ionos/… Le **throttle par MINUTE** (clé `integrationID|classe`),
  lui, protège pleinement la réputation sur le hot path. Si le cap-classe doit
  s'enforcer sur les classes MX → matérialiser la classe sur `message_history`
  (colonne+index), pas de COUNT par domaines (décision lead, cf. v49.go).
- **Fichiers veridian** : `internal/domain/veridian_provider_class_mx.go` (+test).
  Extensions de `veridian_provider_class.go` (constantes classes MX, set étendu,
  `VeridianAllProviderClasses`, helpers `veridianDomainFromEmail`/`classifyBySuffix`).
  Breakdown + pixel + UI étendus aux 11 classes.
- **UI** : 11 classes dans `console/src/services/api/workspace.ts`
  (`VERIDIAN_PROVIDER_CLASSES`, `VERIDIAN_DEFAULT_OPEN_PIXEL`) + libellés littéraux
  (piège Lingui : pas de `t` hors composant) dans `veridian_cold_outreach_settings.tsx`
  et `veridian_broadcast_rates_info.tsx`.

⚠️ **Diffs INLINE supplémentaires** (classification MX) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/queue/worker.go` | +champ `providerMXClassifier *domain.VeridianMXClassifier` + init constructeur (`domain.NewVeridianMXClassifier(nil)`) ; les gates classent via `w.veridianClassifyRecipient(entry)` (MX) au lieu de `domain.ClassifyProviderClass` |

### Pré-filtrage d'envoi — skip adresses invalides AVANT SMTP (Lot 7, 2026-06-14)

Quatrième gate du worker (après circuit breaker → throttle minute → daily cap).
BUT : ne PAS taper le serveur SMTP pour une adresse qu'on SAIT déjà morte —
chaque envoi vers une adresse invalide est un bounce probable qui grille la
réputation IP et gaspille du quota. Spec : ticket
`todo/2026-06-14-bounce-loop-postfix-suppression-cold.md` (lot B « pré-filtrage »).

- **3 conditions DURABLES filtrées** (une adresse invalide ne redevient jamais
  valide) : (1) **syntaxe** invalide (`net/mail.ParseAddress`, RFC 5322, +
  rejet display-name / espaces / `@` multiples → adresse NUE exigée) ; (2)
  **domaine jetable** (`pkg/disposable_emails.IsDisposableEmail` appelé sur le
  **DOMAINE** — la liste embarquée est une liste de domaines, pas d'emails) ;
  (3) **domaine DNS-mort DÉCISIF** (NXDOMAIN, ou ni MX ni A/AAAA — implicit MX
  RFC 5321). RÉUTILISE le classifier MX du Lot 4 (nouvelle méthode
  `VeridianMXClassifier.ResolveDeliverability` qui distingue verdict DÉCISIF vs
  transitoire), PAS de nouveau lookup ni de duplication.
- **Marquage SANS re-tentative en boucle** (≠ throttle/cap qui reschedulent) :
  une adresse pré-filtrée part en **échec PERMANENT** via le chemin upstream
  existant `handleError(ClassifiedError{Type:recipient, Retryable:false})` →
  `MarkAsProcessing` (incrémente attempts) → `message_history` avec `FailedAt`
  (trace durable) → `Delete` de l'entrée queue. Statut « invalid » durable POUR
  CET ENVOI ; AUCUN SMTP ouvert ; le circuit breaker n'est PAS déclenché (erreur
  destinataire). Pas de touche à `contact_lists` : la **suppression durable du
  contact** reste la prérogative du bounce RÉEL (Lot 2, NDR Postfix →
  `MarkEmailsAsBounced`) — la dupliquer ici serait le contournement interdit. Le
  pré-filtre est la DERNIÈRE ligne de défense PAR ENVOI.
- **Best-effort STRICT (non-régression critique)** : syntaxe + jetable = checks
  PURS zéro I/O (toujours actifs, déterministes). DNS = best-effort : timeout /
  erreur transitoire / resolver sans capacité host / suffixe public connu →
  verdict INDÉTERMINÉ → l'envoi PASSE. JAMAIS un glitch DNS ne bloque un
  destinataire légitime. Suffixe public connu (gmail/orange/…) = délivrable
  SANS lookup (hot path).
- **Fichiers veridian** : `internal/service/queue/veridian_prefilter.go`
  (gate `veridianPrefilterRecipient` + helpers `veridianValidEmailSyntax` /
  `veridianEmailDomain`) + `_test.go` colocalisé (syntaxe / jetable / NXDOMAIN
  skippés ; adresse valide PASSE ; suffixe connu sans lookup ; timeout DNS ne
  bloque pas ; classifier absent ne bloque pas). Extension de
  `veridian_provider_class_mx.go` : `ResolveDeliverability` +
  `VeridianMXDeliverability` (3 états) + `veridianMXNotFound` (NXDOMAIN décisif
  vs transitoire) + `LookupHostAddrs` sur le resolver de prod (fallback A/AAAA),
  capacité optionnelle `veridianHostResolver` détectée par type-assertion (les
  resolvers de test sans elle restent INDÉTERMINÉS = ne bloquent pas).
- Pas de migration, pas de `config.VERSION` bump (aucun schéma DB touché).

⚠️ **Diffs INLINE supplémentaires** (pré-filtrage Lot 7) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/queue/worker.go` | +gate `veridianPrefilterRecipient` dans `processEntry` (APRÈS le daily cap, AVANT `MarkAsProcessing`) → route vers `handleError` permanent (skip SMTP, jamais re-tenté). Importe `fmt`/`emailerror` déjà présents. |

### Priorisation follow-up sous contrainte de capacité (Lot FOLLOWUP cold outreach, 2026-06-15)

Quand la capacité quotidienne d'envoi est CONTRAINTE (caps par provider destinataire
`veridian_daily_cap.go`, sending windows, nombre de senders/IP limité en warm-up), le
batch de scheduling (`AutomationExecutor.ProcessBatch`, taille `batchSize`) ne vide pas
tout le dû à chaque tick. Il faut donc PRIORISER : les follow-up dont la fenêtre se ferme
passent AVANT les envois qui peuvent attendre. Roadmap Robert 2026-06-15 (*"contraintes de
date"*). Spec : ticket `todo/2026-06-14-tunnel-vente-ui-controle-et-roadmap.md`.

- **Critère = RETARD** (`overdue = now - ScheduledAt`, décroissant) : le plus en retard sur
  son échéance d'abord. Un J+7 dû depuis 2 jours (prospect qui refroidit, sens de la relance
  qui se périme) prime un J+0 dû à l'instant (peut attendre demain sans perte). L'étape de
  séquence (J+7 > J+3 > J+0) n'est PAS un critère premier : elle est captée par le retard,
  et ça évite de matérialiser le n° d'étape sur `ContactAutomation` (non stocké) — `now -
  ScheduledAt` est dérivable à la lecture, **zéro nouvelle colonne, zéro migration**.
- **Départage stable** : à retard égal → `ScheduledAt` le plus ancien (FIFO, aligné sur le
  `scheduled_at ASC` du repo) → `EnteredAt` le plus ancien → `ID` (déterminisme total).
- **Non-régression hors contrainte** : si le batch absorbe tout le dû (cas nominal faible
  volume), le tri ne change PAS le set traité, seulement l'ordre — et "le plus en retard
  d'abord" reste un FIFO sain sur l'échéance. Le tri ne tranche QUE quand on doit couper au
  `limit`, et alors il coupe les moins urgents.
- **Couche scheduling, pas persistance** : tri PUR appliqué dans `ProcessBatch` APRÈS le
  fetch round-robin (qui garde l'anti-starvation inter-workspace), AVANT la boucle `Execute`.
  Le repo `GetScheduledContactAutomationsGlobal` (upstream-pur) n'est PAS touché.
- **Fichier veridian** : `internal/service/veridian_followup_prioritizer.go`
  (`veridianPrioritizeFollowups(items, now)` tri en place + `veridianOverdue` helper, tous
  deux nil-safe). Test colocalisé `veridian_followup_prioritizer_test.go` (J+7 dû avant J+0
  dû, retard avant à-l'heure, départages, troncature au limit garde les plus urgents,
  non-régression set inchangé, stabilité).

⚠️ **Diffs INLINE supplémentaires** (priorisation follow-up) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/automation_executor.go` | `ProcessBatch` : hisse `now` hors du call repo + appel `veridianPrioritizeFollowups(contacts, now)` après le fetch, avant la boucle `Execute` (fichier déjà étendu Veridian : gate cold exit + `coldReplyChecker`). |
### Multi-SMTP round-robin par provider destinataire + capacité alignée (2026-06-15)

Répartit les envois cold sur TOUS les senders d'une infra (les 3 boîtes
`agences-veridian.fr` p.ex.), en **round-robin keyé par CLASSE de provider
destinataire** : chaque classe (`google`/`microsoft`/…) a son propre curseur, on
ne martèle pas le même couple (sender → provider) → préservation réputation
IP/domaine. Spec : ticket `todo/2026-06-15-...` (Lot ENVOI). + **alignement de
capacité** : le débit global de l'infra = `RateLimitPerMinute · N senders` (les N
boîtes envoient en parallèle, chacune porte sa part).

- **Sélection à l'ENQUEUE** : le sender est figé dans le payload
  (`buildQueueEntry` → `FromAddress`/`FromName`). C'est là (et dans le sender
  direct `SendToRecipient`) que la rotation remplace `GetSender`. Le worker ne
  choisit PAS le sender (il consomme le payload).
- **Fichiers veridian** : `internal/domain/veridian_sender_rotation.go`
  (`VeridianSenderRotator` thread-safe, curseur `integrationID|classe` ;
  `EmailProvider.VeridianSelectSender` respecte le SenderID explicite du template
  puis round-robin si >1 sender ; `VeridianActiveSenderCount` /
  `VeridianEffectiveRateLimit` ; `VeridianIsColdContext` centralise la détection
  tunnel — tag contact OU config broadcast OU config workspace) +
  `internal/service/broadcast/veridian_sender_rotation.go` (`veridianResolveSender` :
  détection cold via workspace mémoïsé du pixel resolver, classification par
  suffixe — pas de lookup MX, la précision MX reste réservée au throttle/cap).
- **OPT-IN strict** : rotation active uniquement si `len(senders) > 1` ET contexte
  cold. Sinon `GetSender` upstream figé (non-régression). rotator nil = upstream.
- **Rotator partagé** par la factory (`f.veridianSenderRotator`), injecté dans les
  deux senders → curseurs persistants entre batchs.

### Fenêtre d'envoi — horaires ouvrables (sending windows, 2026-06-15)

Gate worker qui ne laisse envoyer que dans une fenêtre configurable (jours +
heures + timezone, ex. lun-ven 9h-18h Europe/Paris). Hors fenêtre →
skip-and-reschedule à la prochaine ouverture (`NextOpening`), MÊME contrat que les
gates throttle/cap : `SetNextRetry` SANS incrément d'attempts, délai borné à 24h.
Aucune fenêtre = envoi 24/7 (non-régression). Spec : ticket Lot WINDOWS.

- **Fichiers veridian** : `internal/domain/veridian_sending_window.go`
  (`VeridianSendingWindow` : `IsValid`/`IsWithinWindow`/`NextOpening` ; plage
  croissante stricte, pas de wrap minuit = pas de fenêtre ; parsing
  `VeridianSendingWindowFromMetadata`) +
  `internal/service/queue/veridian_sending_window_gate.go`
  (`veridianSendingWindowGate` + `veridianResolveSendingWindow` cascade). Timezone
  de la fenêtre, si vide → fallback `WorkspaceSettings.Timezone`.
- **Cascade** identique aux rates/caps : `broadcast (metadata)` → `infra
  (EmailProvider)` → `workspace settings` → rien = pas de fenêtre. Premier niveau
  VALIDE gagne.
- **Câblage worker** : gate dans `processEntry` APRÈS le daily cap, AVANT le
  pré-filtre (pas de classification/COUNT si on est hors fenêtre).

⚠️ **Diffs INLINE supplémentaires** (multi-SMTP round-robin + sending windows) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +1 champ `EmailProvider.VeridianSendingWindow` (*VeridianSendingWindow, omitempty) ; +3 méthodes via `veridian_sender_rotation.go` : `VeridianSelectSender`, `VeridianActiveSenderCount`, `VeridianEffectiveRateLimit` (déclarées hors ce fichier, comptées sur `veridian_sender_rotation.go`) |
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianSendingWindow` (*VeridianSendingWindow, omitempty) — copié à l'enqueue |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings.VeridianSendingWindow` (*VeridianSendingWindow, omitempty) ; le `Timezone` workspace sert de fallback à la fenêtre |
| `internal/domain/veridian_provider_class.go` | `VeridianApplyProviderThrottle` propage la sending window broadcast → payload |
| `internal/service/queue/worker.go` | +gate `veridianSendingWindowGate` dans `processEntry` (après daily cap, avant pré-filtre) ; `RateLimitPerMinute` → `VeridianEffectiveRateLimit()` au call-site rate limiter ET dans `getMinEmailRateLimit` (capacité alignée multi-SMTP) |
| `internal/service/broadcast/message_sender.go` | `GetSender` → `veridianResolveSender` dans `SendToRecipient` ; +champ `veridianSenderRotator` + `SetVeridianSenderRotator` |
| `internal/service/broadcast/queue_message_sender.go` | `GetSender` → `veridianResolveSender` dans `buildQueueEntry` ; +champ `veridianSenderRotator` + `SetVeridianSenderRotator` |
| `internal/service/broadcast/factory.go` | +champ `veridianSenderRotator` (créé une fois, partagé) injecté dans les deux senders via `SetVeridianSenderRotator` |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +1 ligne propageant `VeridianSendingWindow` (sinon l'UI Settings sauve sans persister) |

### Vague hygiène/conformité cold (2026-06-15) — 6 lots

Salve de 6 lots (team-orchestration) sur le backlog cold + sécu + admin :

- **Masquage secrets en sortie JSON** (🔴 sécu, historique) : les MarshalJSON IMAP
  et SMTP protègent le clair pendant la persistance. Depuis le hardening du
  2026-08-05, la couche HTTP workspace retire aussi tous les ciphertexts et les
  autres familles de credentials avant réponse API ; voir la section dédiée plus
  haut. Fichiers Veridian : `internal/http/veridian_workspace_redaction.go`,
  `internal/domain/veridian_imap_integration.go` et
  `internal/domain/email_provider_smtp.go`.
- **Conformité mail cold** : multipart `text/plain + text/html` (au lieu de HTML-only) +
  retrait du header `X-Message-ID` sur le chemin SMTP. Helper `internal/service/veridian_html_to_text.go`
  (basé `golang.org/x/net/html`, best-effort → fallback HTML-only).
- **Cold no-unsubscribe** : `internal/domain/veridian_unsubscribe.go`
  (`VeridianSuppressUnsubscribe`, délègue à `VeridianIsColdContext`) → en contexte
  tunnel, `oneclick_unsubscribe_url` non propagé (ni header List-Unsubscribe ni footer).
- **Linter délivrabilité** : `pkg/veridian_deliverability` (`Score(input) Result` pur,
  ~30 règles SA-public, modes strict/lenient par classe) + endpoint
  `POST/GET /api/veridian/templates.deliverabilityScore` (JWT console, `templates:read`).
- **Listing admin** : `VeridianPlanRepository.ListAllIDs` → bucket `managed` cohérent
  sans prefix (bug `collectPlanIDs(prefix=="")` → nil).
- **OpenAPI** : `transactional.create/update/delete` documentés (doc-only).

⚠️ **Diffs INLINE supplémentaires** (vague cold 2026-06-15) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/smtp_service.go` | (1) **supprimé** `SetGenHeader("X-Message-ID", ...)` (chemin SMTP/cold ; SES garde le sien) ; (2) HTML-only → si `veridianHTMLToText(Content) != ""` : `SetBodyString(TypeTextPlain, plain)` + `AddAlternativeString(TypeTextHTML, Content)` (multipart/alternative), sinon fallback HTML-only |
| `internal/service/broadcast/queue_message_sender.go` | `buildQueueEntry` : gate `VeridianSuppressUnsubscribe` avant de propager `oneclick_unsubscribe_url` (workspace mémoïsé via pixelResolver) |
| `internal/service/broadcast/message_sender.go` | `SendToRecipient` : idem gate `VeridianSuppressUnsubscribe` |
| `internal/domain/veridian.go` | interface `VeridianPlanRepository` += `ListAllIDs(ctx, limit)` |
| `internal/app/app.go` | +câblage endpoint `templates.deliverabilityScore` (handler+service veridian) |

### Jitter temporel du throttle par classe (cold outbound, 2026-06-15)

Casse le rythme métronomique du throttle minute par classe destinataire (tell de
machine cold : token-bucket burst 1 = espacement strictement régulier que les
filtres/warmup détectent). On disperse le **délai de re-planification** du gate
throttle autour de sa valeur nominale (±jitter_pct, défaut **±30 %**), SANS
toucher le `rate.Limiter` → le débit MOYEN reste piloté par le token-bucket
(c'est son `Allow()` qui autorise l'envoi au tick suivant), seuls les re-checks
sont dispersés. On ne jitte QUE le throttle par classe (l'étage qui gouverne la
cadence cold), PAS le `Wait` émetteur upstream (hors scope).

- **Fichier veridian** : `internal/service/queue/veridian_jitter.go`
  (`veridianApplyJitter(delay, pct, rng)` PUR + `veridianResolveJitterPct` cascade
  + `veridianJitterDelay` appliqué PAR LE GATE). Test colocalisé.
- **Piège pointeur** : `*float64` partout. `nil` = non configuré → **défaut cold
  0.30** (le gate n'est atteint qu'en cold : des rates par classe sont actifs) ;
  `*0` = jitter **explicitement désactivé** (opt-out). À tester (nil vs *0).
- **Cascade** : `broadcast (metadata veridian_jitter_pct)` → `infra
  (EmailProvider, JSON blob, pas de migration)` → `workspace (settings + allowlist
  UpdateWorkspace)`. Clamp `[0, 0.9]`. `math/rand` (pas crypto).
- **Le gate applique LUI-MÊME le jitter** sur son délai avant `SetNextRetry` →
  **diff worker.go = 0** (le worker n'en sait rien).

⚠️ **Diffs INLINE supplémentaires** (jitter) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +1 champ `EmailProvider.VeridianJitterPct *float64` (omitempty) — JSON blob, pas de migration |
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianJitterPct *float64` (omitempty) — copié à l'enqueue |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings.VeridianJitterPct *float64` (omitempty) |
| `internal/domain/veridian_provider_class.go` | +clé `VeridianJitterPctMetadataKey` + helper `VeridianJitterPctFromMetadata` (renvoie `(pct, present)` : 0 présent = OFF, absent = défaut) + propagation broadcast→payload dans `VeridianApplyProviderThrottle` |
| `internal/service/queue/veridian_provider_throttle.go` | le gate enveloppe son `delay` nominal dans `veridianJitterDelay(workspace, provider, entry, delay)` AVANT le clamp 1s/5min |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +1 ligne `VeridianJitterPct` |

### Anti-hash identique par classe de provider destinataire (cold outbound, 2026-06-15)

Empêche deux mails au **rendu identique** (sujet + corps normalisés = même hash)
de partir vers la **même classe de provider destinataire** dans une fenêtre
glissante (défaut **72h**). Le spintax SEUL ne suffit pas (1 groupe `{A|B}` =
2 variantes → ~250 rendus identiques sur 500 envois gmail). On bloque
l'**identité de hash** (rendu identique modulo whitespace/casse), PAS la
similarité floue (pas de fuzzy hashing maison = usine à gaz refusée). **Jamais de
perte de mail** sur ce motif (≠ pré-filtre Lot 7 permanent).

- **VARIÉTÉ garantie à l'ENQUEUE** (le sender a le template brut) :
  `internal/service/broadcast/veridian_content_dedup.go`
  (`veridianContentDedup.Resolve`) calcule le hash du rendu final, vérifie la
  collision par classe via `ExistsContentHashSince`, et en cas de collision
  **RE-SPIN** avec un seed perturbé (`email:r1`, `:r2`…, déterministe) jusqu'à
  **3 fois**. Template sans variété (re-spin ne change pas le hash) → envoi du
  rendu courant + warning (pas de perte). DI optionnelle via la factory (construit
  avec `messageHistoryRepo`). No-op strict hors contexte cold / anti-hash off /
  dedup nil.
- **FILET best-effort au WORKER** :
  `internal/service/queue/veridian_content_hash_gate.go`
  (`veridianContentHashGate`, **LOG-ONLY**) constate une collision résiduelle (le
  worker ne peut pas re-varier un payload figé) et la TRACE sans bloquer. Placé
  après le pré-filtre, avant `MarkAsProcessing`. Best-effort strict (pas de hash =
  no-op, erreur DB = pass).
- **Hash PUR** : `internal/domain/veridian_content_hash.go`
  (`VeridianContentHash(subject, body)` = `SHA-256(normalize(subj)+0x00+normalize(body))`
  tronqué **128 bits** = 32 hex). `normalize` = lowercase + collapse whitespace +
  trim. + helpers de config cascade (`VeridianAntiHashEnabledFor`,
  `VeridianAntiHashWindow`, extracteurs metadata).
- **Stockage (voie A)** : colonne `message_history.veridian_content_hash CHAR(32)`
  (nullable) + index PARTIEL `(veridian_content_hash, sent_at) WHERE NOT NULL`,
  **migration V52**. Posé à l'enqueue, écrit en `message_history` au succès
  (worker `upsertMessageHistory`, NULLIF vide → NULL hors index). EXISTS
  index-only par classe (filtre domaines comme le daily cap → **même dégradation
  gracieuse MX** assumée : classes MX non enforced via ce chemin, le throttle
  minute protège le hot path).
- **Linter délivrabilité** (`pkg/veridian_deliverability`) : règle informative
  `LOW_SPINTAX_VARIETY` (poids 1.0) déclenchée si l'appelant renseigne
  `VariantCount` (via `pkg/veridian_spintax.CountVariants(templateBrut)`, PUR,
  ajouté) `<` `TargetVolume`. Guide, pas filet. Inactif si l'un des deux = 0
  (non-régression).
- **Config** : `broadcast.metadata["veridian_anti_hash_enabled"/"veridian_anti_hash_window_hours"]`
  → infra (`EmailProvider`, JSON blob) → workspace (settings + allowlist).
  `enabled` = `*bool` (nil=défaut cold ON, *false=OFF) ; `window_hours` int
  (<=0 = défaut 72h). Cascade `broadcast→infra→workspace→défaut`.

⚠️ **Diffs INLINE supplémentaires** (anti-hash) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianContentHash string` (omitempty) — posé à l'enqueue |
| `internal/domain/email_provider.go` | +2 champs `EmailProvider.VeridianAntiHashEnabled *bool` + `VeridianAntiHashWindowHours int` (omitempty) — JSON blob, pas de migration |
| `internal/domain/workspace.go` | +2 champs `WorkspaceSettings.VeridianAntiHashEnabled *bool` + `VeridianAntiHashWindowHours int` (omitempty) |
| `internal/domain/message_history.go` | +1 champ `MessageHistory.VeridianContentHash string` (omitempty) + 1 méthode interface `ExistsContentHashSince(ctx, ws, hash, domains, exclude, since)` |
| `internal/repository/message_history_postgre.go` | `Create`/`Upsert` : +colonne `veridian_content_hash` (NULLIF vide→NULL ; Upsert `DO UPDATE` COALESCE) + impl `ExistsContentHashSince` (EXISTS par hash+fenêtre+classe-par-domaines) |
| `internal/repository/veridian_message_history_decorator.go` | passthrough `ExistsContentHashSince` (lecture, zéro side-effect quota) |
| `internal/service/queue/worker.go` | +gate `veridianContentHashGate` (filet log-only) après pré-filtre ; `upsertMessageHistory` propage `entry.Payload.VeridianContentHash` → `message_history` |
| `internal/service/broadcast/queue_message_sender.go` | `buildQueueEntry` : capture `subjectLiquid` (pré-spintax) + appel `veridianContentDedup.Resolve` (re-spin) + pose `Payload.VeridianContentHash` ; +champ `veridianContentDedup` + `SetVeridianContentDedup` (DI) |
| `internal/service/broadcast/factory.go` | `CreateMessageSender` : `SetVeridianContentDedup(newVeridianContentDedup(f.messageHistoryRepo, f.logger))` sur le queue sender |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +2 lignes `VeridianAntiHashEnabled` + `VeridianAntiHashWindowHours` |
| `internal/database/init.go` | +colonne `message_history.veridian_content_hash` + index partiel (nouveaux workspaces) |
| `config/config.go` | `VERSION` 51.0 → 52.0 |
| `internal/migrations/manager_test.go` | fixture sqlmock `db_version` 51 → 52 |
| `pkg/veridian_deliverability/veridian_deliverability.go` | +champs `Input.VariantCount`/`TargetVolume` + règle `LOW_SPINTAX_VARIETY` |

Fichiers veridian dédiés : `internal/domain/veridian_content_hash.go`,
`internal/service/broadcast/veridian_content_dedup.go`,
`internal/service/queue/veridian_content_hash_gate.go`,
`internal/migrations/v52.go`, `pkg/veridian_spintax/veridian_spintax_variants.go`
(+ tests colocalisés + mock `mock_message_history_repository.go` régénéré).
Migration V52 dans `migrations-pending.txt` (override safety §12, runner en TX).

### Exclusion de classes de provider destinataire (cold outbound, 2026-06-16)

Levier DÉDIÉ pour EXCLURE une ou plusieurs classes de provider destinataire de
l'envoi cold (cas #1 Robert : « ne PAS envoyer à microsoft/outlook » sur une IP
fraîche, Microsoft = warm-up le plus dur). Les contacts d'une classe exclue sont
SKIPPÉS proprement (échec PERMANENT par envoi, pas de SMTP, pas de bounce, entrée
queue Delete) ; le reste du broadcast part normalement. Spec : ticket
`todo/2026-06-16-config-exclusion-provider-cold.md`.

- **Pourquoi un levier neuf** : les leviers throttle/cap sont OPT-IN « 0 = pleine
  vitesse / illimité ». Mettre `rate microsoft = 0` = « envoie microsoft SANS
  throttle » (`veridian_provider_throttle.go:75`), l'INVERSE d'une exclusion. Donc
  une LISTE de classes exclues, distincte des rates/caps.
- **Cascade** (du plus spécifique au plus général), identique aux rates/caps :
  `broadcast (metadata veridian_excluded_provider_classes)` → `infra
  (EmailProvider, JSON blob, pas de migration)` → `workspace settings`. Premier
  niveau NON VIDE gagne. Liste vide/nil partout = no-op strict (non-régression).
- **Gate worker** : `veridianExcludedClassGate` (fichier dédié
  `internal/service/queue/veridian_excluded_class_gate.go`) câblé dans
  `processEntry` **APRÈS le circuit breaker, AVANT le throttle minute** (inutile de
  réserver un token pour une classe qu'on skippe). Classe résolue via
  `w.veridianClassifyRecipient(entry)` (tag amont prime, sinon MX — MÊME résolution
  que throttle/cap, zéro duplication). Si exclue → chemin du pré-filtre Lot 7 :
  `MarkAsProcessing` puis `handleError(ClassifiedError{Type:recipient,
  Retryable:false})` (raison `excluded_provider_class:<classe>`) → `message_history`
  FailedAt + Delete. Circuit breaker NON déclenché (décision de politique, pas
  erreur provider).
- **Fichiers veridian** : `internal/domain/veridian_excluded_classes.go`
  (`VeridianResolveExcludedClasses` cascade → set O(1) + extracteur metadata
  `VeridianExcludedProviderClassesFromMetadata` + normalisation/dédup/validation) +
  `internal/service/queue/veridian_excluded_class_gate.go` (+ tests colocalisés). UI :
  `console/src/components/settings/veridian_cold_outreach_settings.tsx`
  (`ExcludedClassesCard` workspace avec warning chiffré « X contacts ignorés » via
  breakdown R1 + multi-select par infra dans `InfraLimitsCard`) + types front
  (`WorkspaceSettings` ET `EmailProvider` += `veridian_excluded_provider_classes`).
- **Pas de migration, pas de `config.VERSION` bump** (aucun schéma DB touché ;
  `EmailProvider` JSON blob, settings JSON).

⚠️ **Diffs INLINE supplémentaires** (exclusion de classes) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianExcludedProviderClasses []string` (omitempty) — copié à l'enqueue |
| `internal/domain/email_provider.go` | +1 champ `EmailProvider.VeridianExcludedProviderClasses []string` (omitempty) — JSON blob, pas de migration |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings.VeridianExcludedProviderClasses []string` (omitempty) |
| `internal/domain/veridian_provider_class.go` | `VeridianApplyProviderThrottle` propage l'exclusion broadcast → payload |
| `internal/service/queue/worker.go` | +gate `veridianExcludedClassGate` dans `processEntry` (après circuit breaker, AVANT le throttle minute) → route vers `handleError` permanent (skip SMTP, jamais re-tenté) |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +1 ligne `VeridianExcludedProviderClasses` (sinon l'UI Settings sauve sans persister) |

### Cap journalier par SENDER émetteur — warmup IP (cold outbound, 2026-06-16)

Quatrième dimension de plafonnement, **keyée ÉMETTEUR** (≠ les caps V49 keyés
DESTINATAIRE). But : warmup IP/domaine classique — chaque boîte d'envoi monte son
propre volume jour après jour, indépendamment du destinataire. Le PLUS RESTRICTIF
gagne avec les caps destinataire/classe. Spec : ticket
`todo/2026-06-16-cap-par-sender-emetteur.md` (lecture B « 1/jour par provider »).

- **Audit pré-code (clé)** : `message_history` ne stockait AUCUNE colonne sender
  exploitable — le FROM ne vivait que dans le JSON `channel_options.FromName`
  (display name, non-queryable). Migration NÉCESSAIRE (pas de dérivation
  bricolée). **Migration V53** (workspace-only, additive, idempotente, PAS de
  CONCURRENTLY car runner en TX) : colonne `message_history.veridian_sender_email
  VARCHAR(255)` (nullable, stockée lowercase) + index PARTIEL
  `(veridian_sender_email, sent_at) WHERE NOT NULL`. `config.VERSION` 52→53,
  fixture `manager_test`, `migrations-pending.txt` (override safety §12). Idem
  `init.go` pour les nouveaux workspaces.
- **Écriture** : posée à l'envoi (`worker.go:upsertMessageHistory`) depuis
  `entry.Payload.FromAddress` (sender figé à l'enqueue, sender-rotation incluse).
  `Create`/`Upsert` : `NULLIF(lower($26),'')` → vide = NULL (hors index partiel) ;
  Upsert `DO UPDATE COALESCE` préserve.
- **Repo** : `MessageHistoryRepository.CountSentSinceForSender(senderEmail, since)`
  (index-only, COUNT `WHERE veridian_sender_email = lower($1) AND sent_at >= $2`).
  Décorateur quota (passthrough lecture) + mock régénéré (ajout manuel, mockgen
  bloqué par go.sum).
- **Gate worker** : `veridian_per_sender_cap.go` (`veridianPerSenderCapGate` +
  `veridianResolvePerSenderCap`), JUMEAU de `veridianDailyCapGate`. Placé dans
  `processEntry` APRÈS le daily-cap destinataire, AVANT la sending-window. Même
  contrat skip-and-reschedule (`SetNextRetry` sans incrément attempts, re-check
  borné 1h via `veridianRescheduleCapped`). Best-effort : erreur COUNT = pass ;
  `FromAddress` vide = no-op (pas de clé d'attribution).
- **Config** : `EmailProvider.VeridianPerSenderDailyCap int` (JSON blob infra, pas
  de migration pour la config) + `WorkspaceSettings` (allowlist `UpdateWorkspace`)
  + `EmailQueuePayload` (metadata broadcast `veridian_per_sender_daily_cap` via
  `VeridianApplyProviderThrottle` + helper `VeridianPerSenderDailyCapFromMetadata`).
  Cascade `broadcast → infra → workspace`. 0/vide = pas de plafond (opt-in strict).
- **UI** : champ « per-sender daily cap (warmup) » exposé au NIVEAU WORKSPACE
  (carte caps globaux) ET par INFRA (`InfraLimitsCard`) dans
  `veridian_cold_outreach_settings.tsx`. + **preset « Mode warmup »**
  (`VERIDIAN_WARMUP_PRESET` dans `workspace.ts`, `PresetCard` owner-only) :
  bouton qui pré-remplit caps=1/classe + per-recipient=1 + per-sender=20 + rates
  0.5/min + fenêtre lun-ven 9-18 Europe/Paris, sans sauver (relecture + Save). Le
  round-robin s'active tout seul (≥2 senders + contexte cold) — warning si <2.

⚠️ **Diffs INLINE supplémentaires** (cap par sender + preset warmup) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +1 champ `EmailProvider.VeridianPerSenderDailyCap int` (omitempty) — JSON blob, pas de migration |
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianPerSenderDailyCap int` (omitempty) — copié à l'enqueue |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings.VeridianPerSenderDailyCap int` (omitempty) |
| `internal/domain/message_history.go` | +1 champ `MessageHistory.VeridianSenderEmail string` (omitempty) + 1 méthode interface `CountSentSinceForSender` |
| `internal/domain/veridian_provider_class.go` | +clé `VeridianPerSenderDailyCapMetadataKey` + helper `VeridianPerSenderDailyCapFromMetadata` + propagation broadcast→payload dans `VeridianApplyProviderThrottle` |
| `internal/repository/message_history_postgre.go` | `Create`/`Upsert` : +colonne `veridian_sender_email` (NULLIF lower vide→NULL ; Upsert COALESCE) + impl `CountSentSinceForSender` |
| `internal/repository/veridian_message_history_decorator.go` | passthrough `CountSentSinceForSender` (lecture, zéro side-effect quota) |
| `internal/service/queue/worker.go` | +gate `veridianPerSenderCapGate` dans `processEntry` (après daily cap, avant sending window) ; `upsertMessageHistory` propage `entry.Payload.FromAddress` → `veridian_sender_email` |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +1 ligne `VeridianPerSenderDailyCap` |
| `internal/database/init.go` | +colonne `message_history.veridian_sender_email` + index partiel (nouveaux workspaces) |
| `config/config.go` | `VERSION` 52.0 → 53.0 |
| `internal/migrations/manager_test.go` | fixture sqlmock `db_version` 52 → 53 |

Fichiers veridian dédiés : `internal/service/queue/veridian_per_sender_cap.go`,
`internal/migrations/v53.go` (+ tests colocalisés ;
`console/src/components/settings/veridian_cold_outreach_settings.tsx` étendu :
preset + champ cap sender ; `console/src/services/api/workspace.ts` :
`VERIDIAN_WARMUP_PRESET`). Migration V53 dans `migrations-pending.txt` (override
safety §12, runner en TX). Rampe progressive auto = ticket séparé non livré ici.

### Warmup progressif — rampe AUTO du cap journalier (cold outbound, 2026-06-17)

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

### FIX P1 — warmup = cap TOTAL par infra (toutes classes, MX compris) (2026-06-19)

Le warmup progressif (ci-dessus) prétendait plafonner le **volume TOTAL** émis par une
infra (« J1 = 5 max, toutes classes »), mais l'implémentation d'origine **réutilisait le
COUNT-par-classe** (`veridianCountClassForInfra` → `CountSentSinceForDomains*`). Deux bugs
qui rendaient la rampe inutile pour son objectif (audit cohérence cold). Spec :
`todo/done/2026-06-19-audit-warmup-cap-non-enforce-mx-et-total.md` (P1, tier 🔴).

- **Bug 1 — cap PAR CLASSE, pas TOTAL** : le COUNT était filtré par les domaines de la
  classe du destinataire courant → une infra en warmup J1 (cap=5) envoyait `5 × nombre de
  classes` (5 google + 5 microsoft + 5 freemail…), pas 5 au total. Le « volume TOTAL »
  documenté n'était jamais calculé.
- **Bug 2 (plus grave) — bypass TOTAL sur classes MX** : `VeridianDomainsForClass` renvoie
  `[]` pour les 6 classes MX (ovh/ionos/apple_icloud/security_gateway/other_hoster/
  corporate_selfhost) → `COUNT … domain = ANY('{}')` = 0 → jamais ≥ cap → **jamais
  enforcé**. Or la MAJORITÉ des leads B2B cold résolvent en classe MX (cf. cartographie
  providers) → une IP fraîche blastait SANS plafond vers exactement la population à
  protéger. Inverse du but.

- **Voie propre** : le warmup est conceptuellement un cap TOTAL par infra (par DOMAINE
  d'envoi), **indépendant de la classe destinataire** (qui est justement le point faible
  sur MX). Il ne passe donc PLUS par le COUNT-par-classe :
  - **Nouvelle méthode repo** `CountSentSinceForSenderDomain(workspaceID, senderDomain,
    since)` = `COUNT(*) WHERE lower(split_part(veridian_sender_email,'@',2)) = senderDomain
    AND sent_at >= since` — **AUCUN filtre de classe destinataire** (c'est ce qui le rend
    robuste aux MX). Réutilise l'infra V53 (colonne `veridian_sender_email` + index
    partiel). Décorateur quota passthrough + mock régénéré (à la main, mockgen cassé).
  - **Gate** (`veridian_daily_cap.go`) : quand `warmupCap > 0`, branche WARMUP dédiée qui
    compte le TOTAL par domaine émetteur et compare à `warmupCap`, puis **court-circuite**
    (le cap-classe statique n'est PAS aussi évalué — le warmup gouverne le plafond de
    l'infra pendant la rampe). Hors warmup (`warmupCap == 0`), le cap-classe statique
    garde son comportement antérieur EXACT (dégradation MX assumée OK pour lui).
  - **Best-effort inchangé** : erreur COUNT = pass. Sender vide (legacy/pré-V53) = pas
    d'attribution infra → warmup non enforçable (pass documenté).
  - `veridianWarmupClassCap` renommée `veridianWarmupCap` (ce n'est plus un cap *de
    classe*). **Pas de migration, pas de bump VERSION** (la colonne V53 suffit ; le COUNT
    filtre déjà `sent_at` indexé + préfixe sender_email indexé — décision « COUNT live,
    pas d'agrégat », cf. v53.go).

- **Validation E2E ON-PREMISE (tier 🔴)** : `cold-simulate` étendu d'un mode
  `warmup_cap_decision` (`CountSentSinceForSenderDomain(senderDomain) >= warmup_cap`, le
  prédicat EXACT de la branche warmup) + harness `scripts/e2e/cold-warmup-total.sh`
  (workspace jetable vierge → seed 1 envoi google + 1 envoi ovh-MX depuis `infra-a`
  → 3e envoi n'importe quelle classe = `would_be_capped=true` à cap=2 ; infra-b séparée
  reste sous cap ; recoupé par COUNT psql). ZÉRO mail externe. Le gate worker réel
  consomme le même `CountSentSinceForSenderDomain` → le prédicat testé EST le code prod.

⚠️ **Diffs INLINE supplémentaires** (fix warmup total) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/message_history.go` | +1 méthode interface `MessageHistoryRepository.CountSentSinceForSenderDomain` (COUNT TOTAL par domaine émetteur, sans filtre classe destinataire) |
| `internal/repository/message_history_postgre.go` | +impl `CountSentSinceForSenderDomain` (COUNT `lower(split_part(veridian_sender_email,'@',2)) = lower($2)` ; senderDomain vide = 0 sans requête) |
| `internal/repository/veridian_message_history_decorator.go` | +passthrough `CountSentSinceForSenderDomain` (lecture, zéro side-effect quota) |

Fichiers veridian touchés : `internal/service/queue/veridian_daily_cap.go` (branche warmup
= cap TOTAL via `CountSentSinceForSenderDomain` + court-circuit du cap-classe ; helper
renommé `veridianWarmupCap`), `internal/domain/veridian_warmup.go` (doc corrigée : plafond
holistique total, pas par classe), `internal/http/veridian_cold_simulate_handler.go` (mode
`warmup_cap_decision`) + tests colocalisés + mock `mock_message_history_repository.go`
régénéré. Harness `scripts/e2e/cold-warmup-total.sh`. **Pas de migration, pas de bump
`config.VERSION`** (la colonne V53 suffit).

### V55 — réservation atomique des quotas journaliers (2026-08-05)

Le `COUNT(message_history) → SMTP` historique était un TOCTOU : plusieurs
workers pouvaient lire la même capacité puis dépasser le plafond. V55 conserve
les COUNT comme préfiltre, mais l'autorisation finale passe par un ledger
PostgreSQL dans chaque DB tenant :

- `veridian_daily_quota_counters`, clé jour UTC + workspace + type + domaine
  émetteur + classe finale, incrément conditionnel `used < cap` ;
- `veridian_daily_quota_reservations`, idempotence `(workspace, message_id,
  quota_kind)` et compensation avant acceptation SMTP ;
- le cap par classe ET le cap warmup total se réservent tous les deux lorsqu'ils
  sont configurés ; échec du second → libération du premier créé par ce worker ;
- toute erreur COUNT/réservation est fail-closed (report horaire, sans consommer
  d'attempt grâce au remboursement atomique du claim) ;
- réservation seulement après exclusion, fenêtre, préfiltre et garde automation,
  juste avant SMTP ;
- `message_history.veridian_provider_class` persiste la classe finale payload/MX.
  Au premier passage du jour, les succès historiques sans classe sont classifiés
  puis backfillés. Les lignes `failed_at IS NOT NULL` ne consomment jamais le cap.

Migration V55 additive/expand-safe, index sans `CONCURRENTLY` car le runner de
migrations est transactionnel (allowlist `migrations-pending.txt`).

### V56 — profils d'envoi multi-intégrations et quota exact (2026-08-05)

- `WorkspaceSettings.veridian_marketing_email_provider_ids` configure un pool
  ordonné d'intégrations email complètes. La queue choisit par hash stable
  workspace + classe destinataire + message : allocation et restart convergent
  vers le même profil, puis l'`integration_id` est figé dans l'entrée.
- `EmailProvider.veridian_profile_daily_cap` est un plafond total par profil,
  toutes classes et senders confondus. Gmail prend 30/j par défaut et refuse
  toute valeur supérieure à 50.
- Le worker réserve atomiquement le quota `profile` dans le ledger V55 avec
  l'IntegrationID exact. `message_history.veridian_profile_id` attribue les
  acceptations au profil exact; migration V56 additive et indexée.
- `GET|POST /api/veridian/emailProfiles.usage` renvoie `used`/`remaining` depuis
  le compteur atomique (autorité quota), plus `accepted_used` et
  `accepted_by_provider_class` depuis l'historique. Une issue SMTP ambiguë peut
  donc produire `used > accepted_used` sans mentir sur la capacité restante.
  Le jour de politique est explicitement UTC et les classes inconnues restent
  sous la clé `unclassified`, jamais reclassées artificiellement `corporate`.
- Le cache OAuth Google inclut un digest opaque du refresh token : deux comptes
  partageant le même client OAuth ne partagent jamais un access token, même si
  leur username est vide. Aucun secret n'apparaît dans la clé ou les logs.
- Lifecycle fail-safe : une entrée déjà en queue n'est jamais reroutée si son
  profil atteint son cap (elle attend le jour suivant). Une suppression prend
  un verrou partagé sur `email_queue` et refuse en `409` toute intégration encore
  référencée par une entrée `pending` ou `processing`; les enqueue concurrents
  prennent le verrou conflictuel puis revalident l'intégration avant insertion.

### Pixel d'ouverture PAR INFRA — dernier levier non-infra harmonisé (cold outbound, 2026-06-17)

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

### Events comportementaux cold↔web — `email.opened/clicked/replied` → Hub (2026-06-17)

Câble l'émission des events comportementaux que le réconciliateur de scoring prospect
du Hub (livré prod, `ingestProspectEvent`) ATTENDAIT sans jamais les recevoir → backend
Hub orphelin (0 row `prospect_events`/`prospect_scores`). La donnée open/click/reply
existait en interne Notifuse mais n'était jamais `.Emit()`. Spec : ticket
`todo/2026-06-17-emettre-events-comportementaux-email-opened-clicked-replied-hub.md`.

- **RÉUTILISE le `VeridianWebhookEmitter` existant** (voie legacy HMAC consommée par le
  Hub `app/api/webhooks/notifuse/route.ts:dispatchLegacyEvent`) — PAS de nouveau canal.
  Best-effort de bout en bout (Emit part en goroutine, retry 3x) → ne bloque JAMAIS le
  pixel d'ouverture / la redirection de clic / la détection de réponse.
- **3 events** (constantes `internal/domain/veridian.go`) : `EventEmailOpened` /
  `EventEmailClicked` / `EventEmailReplied`. `tenant_id` (param Emit) = workspaceID
  Notifuse = `notifuseWorkspaceSlug` côté Hub ; `event_id` (UUID auto par Emit) =
  idempotency_key applicative (replay ne ré-incrémente pas le score).
- **Payload `data`** (lu par le Hub, CONTRAT-HUB §7.5.1/§7.5.2) : `contact_email`
  (✅ CLÉ DE JOINTURE V1 — résolu via `messageRepo.FindContactEmailByMessageID`,
  normalisé), `message_id`, `occurred_at` (RFC3339 UTC), + `match_type` (reply).
  `vid` = étage 2 (ticket vid séparé, BLOQUÉ Hub) → slot prêt, non posé.
- **Points d'émission** : open/click depuis `EmailService.OpenEmail`/`VisitLink` (la
  donnée y est, l'anti-bot du handler `email_handler.go` gate déjà : on n'émet que sur
  `shouldRecord==true`, pas de pollution bot) ; reply depuis
  `VeridianReplyService.ProcessInboundMessage` (après `MarkReplied`, fast-path idempotent).
- **DI optionnelle nil-safe** (`SetVeridianWebhookEmitter`, câblée dans `app.go` après
  création de l'emitter) : emitter noop/absent = aucune émission, comportement upstream
  strictement inchangé (Notifuse self-hosted / Hub non configuré).
- **Fichier veridian** : `internal/service/veridian_behavioral_emit.go`
  (`veridianEmitBehavioral` : lookup contact_email + Emit, best-effort) + tests
  colocalisés. Reply : helper `veridianEmitReplied` dans `veridian_reply_service.go`.

⚠️ **Diffs INLINE supplémentaires** (events comportementaux) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/veridian.go` | +3 constantes `VeridianEvent` : `EventEmailOpened`/`EventEmailClicked`/`EventEmailReplied` |
| `internal/service/email_service.go` | +champ `veridianWebhookEmitter` (DI optionnelle) ; `OpenEmail`/`VisitLink` appellent `veridianEmitBehavioral` après `SetOpened`/`SetClicked` réussis |
| `internal/app/app.go` | +`a.emailService.SetVeridianWebhookEmitter(...)` + `a.veridianReplyService.SetVeridianWebhookEmitter(...)` après création de l'emitter |

Fichiers veridian dédiés : `internal/service/veridian_behavioral_emit.go`
(+ test) ; helper reply dans `internal/service/veridian_reply_service.go`
(+ test). Aucune migration (la donnée existe déjà : message_history + signal replied).

### KPI dashboard cold — engagement par classe + bounce hard/soft + reply tooltip (2026-06-16/17)

Trois KPI du dashboard mails (`console/src/components/analytics/`), pour piloter
la délivrabilité cold sans curl. Tickets `todo/done/2026-06-16-kpi-engagement-par-classe-provider.md`,
`...2026-06-16-kpi-bounce-hard-soft-dashboard.md`, `...2026-06-17-reply-kpi-ignore-message-type-filter.md`.

- **Engagement PAR CLASSE de provider destinataire** (🟡, endpoint neuf) :
  `POST/GET /api/veridian/messages.engagementByClass` agrège
  sent/delivered/bounced/opened/clicked par classe sur la fenêtre du dashboard.
  La classe n'est PAS une dimension de `message_history` (Lot 4) → le repo agrège
  par DOMAINE en SQL (`COUNT(*) FILTER`, indexé created_at), le service mappe
  domaine → classe en Go via `veridian_provider_class.go` (zéro CASE SQL, **pas de
  migration**). ⚠️ Classification par SUFFIXE (pas MX) → classes MX (ovh/ionos/…)
  tombent en `corporate` (même dégradation gracieuse que le breakdown R1 / le
  daily-cap classe ; documentée dans le tooltip du tableau). Auth JWT console +
  `contacts:read` (gardien service, comme breakdown R1 / reply stats). Fichiers
  veridian flat : `internal/domain/veridian_engagement_by_class.go`,
  `internal/repository/veridian_engagement_by_class_postgres.go`,
  `internal/service/veridian_engagement_by_class_service.go`,
  `internal/http/veridian_engagement_by_class_handler.go` (+ tests + mocks),
  câblé dans `app.go`. Front :
  `console/src/components/analytics/veridian_engagement_by_class.tsx` (tableau Ant
  Design, bounce >5% en rouge) + `console/src/services/api/veridian_engagement_by_class.ts`,
  monté dans `AnalyticsDashboard.tsx` sous `EmailMetricsChart`.
- **Bounce HARD vs SOFT** (🟢) : la colonne `message_history.bounce_type` EXISTAIT
  (v8) mais n'était **JAMAIS écrite** (le chemin bounce ne posait que `bounced_at`
  + `status_info`). Donc « +2 mesures analytics » seul = KPI mort (toujours 0). Voie
  propre (R0, pas de faux KPI, **pas de migration**) : on ÉCRIT le label typé
  `HardBounce`/`SoftBounce` dans `bounce_type` au moment du bounce, dérivé de
  `domain.ClassifyBounce` (helper veridian `VeridianBounceTypeLabel` dans
  `internal/domain/veridian_bounce_type.go`). ⚠️ Seuls les HARD posent `bounced_at`
  sur message_history (un soft transitoire n'est PAS terminal — poser `bounced_at`
  déclencherait les triggers `contact_lists.status='bounced'` + webhook = faux) →
  `count_bounced_hard` est fidèle, `count_bounced_soft` reste ~0 sur ce flux
  (assumé). Front : carte Bounced reste le TOTAL, split hard/soft révélé au tooltip.
- **Reply KPI ignore le filtre** (🔵, ~tooltip) : la carte Replies (livrée v53) est
  contact-level (`veridian_contact_reply`, pas rattaché à un envoi) → ne respecte
  PAS le Segmented All/Broadcasts/Transactional. Fix = tooltip explicite (option 1
  du ticket), zéro backend.

⚠️ **Diffs INLINE supplémentaires** (KPI dashboard cold) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/analytics.go` | +2 mesures schéma `message_history` : `count_bounced_hard` (`bounce_type ILIKE 'hard%'`), `count_bounced_soft` (`'soft%'`). Map de données, pas de func → pas de test colocalisé requis. |
| `internal/domain/message_history.go` | +1 champ `MessageEventUpdate.BounceType *string` (écrit sur `bounce_type` pour le groupe Bounced uniquement) |
| `internal/repository/message_history_postgre.go` | `SetStatusesIfNotSet` : sur le groupe Bounced, 4e colonne VALUES `bounce_type` + `bounce_type = COALESCE(message_history.bounce_type, updates.bounce_type)` (idempotent re-dispatch). Autres groupes : forme 3-colonnes inchangée. |
| `internal/service/inbound_webhook_event_service.go` | cas `BounceClassificationHard` : `MessageEventUpdate.BounceType = VeridianBounceTypeLabel(class)` (`"HardBounce"`). |

Fichiers veridian dédiés : `internal/domain/veridian_bounce_type.go` (+test),
`internal/domain/veridian_engagement_by_class.go` (+test),
`internal/repository/veridian_engagement_by_class_postgres.go` (+test),
`internal/service/veridian_engagement_by_class_service.go` (+test),
`internal/http/veridian_engagement_by_class_handler.go` (+test) + mocks
`mock_veridian_engagement_by_class_{repository,service}.go`. Front :
`console/src/components/analytics/{veridian_engagement_by_class.tsx,EmailMetricsChart.tsx,AnalyticsDashboard.tsx}`,
`console/src/services/api/veridian_engagement_by_class.ts`.

### FIX P0 — dashboard 500 `bounce_type` absent + garde-fous CI (2026-06-17)

Le KPI bounce hard/soft (commit `5414a3d7`, promu prod) filtrait `bounce_type
ILIKE 'hard%'` sur `message_history` en supposant « la colonne existe depuis
v8 ». **C'était FAUX** : la seule déclaration `bounce_type` dans `init.go`
(ligne 246) appartient à `inbound_webhook_events`, PAS à `message_history` ; le
bloc CREATE TABLE `message_history` ne l'a JAMAIS eue et aucune migration ne
l'ajoutait. Vérifié sur la DB staging réelle : colonne ABSENTE sur TOUS les
workspaces (dasherr873/coldtunnel/canary*). → `POST /api/analytics.query` = 500
`pq: column "bounce_type" does not exist`, tout l'Email Metrics tombe. Le chemin
d'ÉCRITURE du KPI (`SetStatusesIfNotSet` posant `bounce_type`) était aussi cassé.

- **Fix racine (R0, voie A)** : **migration V54** (`internal/migrations/v54.go`,
  workspace-only, additive, idempotente) `ALTER TABLE message_history ADD COLUMN
  IF NOT EXISTS bounce_type VARCHAR(100)` → rattrape les workspaces existants.
  PAS d'index (les mesures filtrent déjà `bounced_at IS NOT NULL` + `created_at`
  indexé ; volume bouncé faible). PAS dans `migrations-pending.txt` (aucun CREATE
  INDEX → safety §12 ne le flag pas). `config.VERSION` 53→54 + fixture
  `manager_test` (AddRow "54"). `init.go` : colonne ajoutée au CREATE TABLE
  `message_history` (cohérence nouveaux workspaces). Le KPI bounce hard/soft
  devient enfin FONCTIONNEL (avant V54 il était mort/cassé).
- **Front — état d'erreur propre** : `console/src/services/api/client.ts` —
  l'`AnalyticsHandler.writeErrorResponse` (upstream) renvoie `{error: true,
  message: "..."}` (`error` = BOOLÉEN), donc `errorData.error` valait `true` →
  `ApiError("true")` → l'écran affichait « ApiError: true ». Fix : extraction
  robuste (string `error` sinon `message` sinon générique). `EmailMetricsChart`
  affiche désormais un bandeau lisible (« Unable to load email metrics ») + le
  détail technique + bouton **Retry**, jamais une valeur brute.
- **Garde-fou CI #1 (smoke E2E dashboard, BLOQUANT)** :
  `tests/e2e-veridian/specs/dashboard-smoke.spec.ts` — provisionne un workspace
  NEUF (HMAC), auto-login, CHARGE le dashboard headless contre le VRAI schéma DB,
  ASSERTE 0 réponse ≥500 sur analytics.query/replyStats/engagementByClass/
  providerBreakdown + 0 erreur console (ApiError/Error) + graphique rendu (carte
  « Sent »). C'est le chaînon manquant : les unit tests mockent la DB et ne
  voient pas un schéma cassé. Tourne dans le job `e2e-staging` existant (pas de
  nouveau job — il fait tourner tout `specs/`). Skip en prod (pas de tenant).
- **Garde-fou CI #2 (Husky test-par-composant analytics)** :
  `scripts/ci/check-test-mapping.sh` — un `console/src/components/analytics/*.tsx`
  modifié sans `.test.tsx` colocalisé BLOQUE le pre-push (états loading/error/
  data, retry, rendu). Vitest mocke la DB → complémentaire du smoke E2E, pas un
  substitut. Test colocalisé livré : `EmailMetricsChart.test.tsx`.

⚠️ **Diffs INLINE supplémentaires** (fix dashboard 500) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/database/init.go` | +colonne `message_history.bounce_type VARCHAR(100)` dans le CREATE TABLE (était absente ; la mesure analytics + le write path la supposaient) |
| `config/config.go` | `VERSION` 53.0 → 54.0 |
| `internal/migrations/manager_test.go` | fixture sqlmock `db_version` 53 → 54 |
| `console/src/services/api/client.ts` | extraction d'erreur robuste : string `error` sinon `message` sinon générique (gère `{error: true, message}` d'analytics_handler.go) |
| `console/src/components/analytics/EmailMetricsChart.tsx` | bandeau d'erreur lisible + bouton Retry (jamais de valeur brute « true ») |
| `scripts/ci/check-test-mapping.sh` | +règle front : `analytics/*.tsx` modifié exige `.test.tsx` colocalisé |

Fichiers veridian dédiés : `internal/migrations/v54.go` (+test),
`tests/e2e-veridian/specs/dashboard-smoke.spec.ts`,
`console/src/components/analytics/EmailMetricsChart.test.tsx`.

### Wrapper veridian analytics — error-shape standardisé (cleanup, 2026-06-18)

Aligne les routes analytics sur l'error-shape standard `{"error":"<string>"}`. Le
handler upstream `internal/http/analytics_handler.go:writeErrorResponse` renvoyait
`{"error": true, "message": "..."}` (error = BOOLÉEN), seul endroit de l'API à
diverger de `internal/http/utils.go:WriteJSONError`. Le front était déjà durci
(symptôme `ApiError: true` fermé par le P0 dashboard ci-dessus) → ce lot est du PUR
alignement de cohérence API. Spec : ticket
`todo/done/2026-06-17-analytics-handler-error-shape-non-standard.md`.

- **Convention respectée — ZÉRO patch upstream** : `analytics_handler.go` reste
  intact. Nouveau handler veridian `internal/http/veridian_analytics_handler.go`
  (`VeridianAnalyticsHandler`) qui prend le **MÊME** `domain.AnalyticsService` que
  l'upstream (aucune nouvelle DI), réplique la fine couche transport HTTP et émet
  les erreurs via `WriteJSONError`. Aucune logique métier dupliquée (le service est
  partagé). Réutilise les types `AnalyticsQueryRequest`/`AnalyticsSchemasRequest`
  upstream (même package `http`).
- **Routage (app.go)** : `analyticsHandler := httpHandler.NewVeridianAnalyticsHandler(...)`
  REMPLACE `NewAnalyticsHandler(...)` au point de câblage existant ; la ligne
  `analyticsHandler.RegisterRoutes(a.mux)` est inchangée (le wrapper expose la même
  méthode). Le handler upstream n'est PLUS enregistré dans le mux (sinon panic
  pattern dupliqué). `POST /api/analytics.query` + `POST /api/analytics.schemas`
  routés explicitement (Go 1.22 method routing ; le front ne fait que des POST).
- **NON-RÉGRESSION garantie** : les réponses 200 sont STRICTEMENT identiques à
  l'upstream — `handleQuery` renvoie le `*analytics.Response` brut (data+meta
  top-level, consommé direct par `console/src/services/api/analytics.ts`),
  `handleGetSchemas` renvoie `{"schemas": ...}`. SEULE la forme d'erreur change
  (booléen → string lisible). Test colocalisé `veridian_analytics_handler_test.go`
  prouve les deux : erreur = `{error:"<string>"}` jamais `{error:true}` + succès
  inchangé.

Aucun fichier upstream modifié (le wrapper se substitue au point de routage seul).

### Cap journalier par classe KEYÉ PAR INFRA ÉMETTRICE — warm-up multi-domaine (cold outbound, 2026-06-18)

Rend le **cap journalier par classe destinataire** (`veridian_provider_class_daily_cap`)
keyé **PAR INFRA ÉMETTRICE** = couple (domaine émetteur × classe destinataire). Avant,
`CountSentSinceForDomains` comptait au niveau workspace, toutes infras d'envoi
confondues → dès qu'un 2e domaine d'envoi est ajouté (scaling cold multi-domaine), les
deux infras **partagent** le compteur de classe et se marchent dessus, violant la
doctrine warm-up §7.3bis (« 1 infra IP+domaine → 1 classe destinataire = N/jour »). Spec :
ticket `todo/2026-06-18-cap-journalier-par-infra-emettrice-x-classe.md` (P1, tier 🔴).

- **Option A retenue** (la plus fidèle à la doctrine) : compter **par DOMAINE du
  sender** (`lower(split_part(veridian_sender_email,'@',2))`), pas par adresse exacte —
  les N adresses d'un même domaine partagent l'IP/réputation, donc comptent ENSEMBLE.
  La colonne `message_history.veridian_sender_email` existe déjà (V53, peuplée à
  l'envoi, index partiel) → **PAS de nouvelle migration**, juste un COUNT croisant
  domaines-classe ET domaine-émetteur.
- **Pas d'index neuf** (décision « COUNT live, pas d'agrégat », cf. v49.go/v53.go) : le
  COUNT filtre d'abord par `sent_at` (index V49) + le préfixe `veridian_sender_email`
  (index partiel V53) ; volume cold quotidien négligeable. À matérialiser seulement si
  mesuré nécessaire.
- **Cap par DESTINATAIRE inchangé** (`CountSentSinceForContact`) : reste workspace-
  global (anti-harcèlement = ne jamais sur-solliciter une personne, peu importe l'infra).
- **Fallback non-régression** : si l'entrée n'a PAS de FROM exploitable (legacy /
  pré-V53), le gate retombe sur le COUNT workspace-global `CountSentSinceForDomains`
  (comportement strictement antérieur). Best-effort inchangé (erreur COUNT = pass).
- **Cascade config inchangée** (`broadcast → infra (EmailProvider) → workspace`) : le
  cap-classe posé AU NIVEAU INFRA (`EmailProvider.VeridianProviderClassDailyCap`) prend
  alors tout son sens — il plafonne CETTE infra vers la classe.
- **Dégradation MX assumée** (déjà documentée) : `VeridianDomainsForClass` renvoie [] pour
  les classes MX → ce chemin ne les enforce pas ; le throttle minute par classe protège
  le hot path.
- **Preset warm-up réaligné** (`console/src/services/api/workspace.ts`,
  `VERIDIAN_WARMUP_PRESET` + `WARMUP_PRESET` policy preset) : **retrait** de
  `veridian_per_sender_daily_cap` (per-sender individuel = faux modèle réputationnel ; le
  cap-classe par infra couvre la réputation par domaine). Le champ reste supporté backend
  (réglable à la main), il n'est juste plus posé par CE preset. Caps classe=1 (par infra) +
  per-recipient=1 + rates 0.5/min + fenêtre conservés. Tests front colocalisés adaptés.

⚠️ **Diffs INLINE supplémentaires** (cap par classe par infra émettrice) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/message_history.go` | +1 méthode interface `MessageHistoryRepository.CountSentSinceForDomainsAndSenderDomain` (COUNT classe-par-domaines ET `lower(split_part(veridian_sender_email,'@',2)) = senderDomain`) |
| `internal/repository/message_history_postgre.go` | +impl `CountSentSinceForDomainsAndSenderDomain` (clone de `CountSentSinceForDomains` + prédicat domaine émetteur ; senderDomain vide ou domaines vide non-exclude = 0 sans requête) |
| `internal/repository/veridian_message_history_decorator.go` | +passthrough `CountSentSinceForDomainsAndSenderDomain` (lecture, zéro side-effect quota) |

Fichiers veridian touchés : `internal/service/queue/veridian_daily_cap.go`
(`veridianDailyCapGate` : le COUNT de classe passe par le nouveau helper
`veridianCountClassForInfra` qui dérive `senderDomain` de `entry.Payload.FromAddress`
via `veridianEmailDomain`, fallback workspace-global si FROM vide) + tests colocalisés
(`veridian_daily_cap_test.go` : 2 infras cappées indépendamment, alias même domaine
partagent le compteur, legacy sans FROM = fallback global, erreur COUNT = pass).
Front : `console/src/services/api/workspace.ts`,
`console/src/services/cold/sending_policy_presets.ts` (+ tests). Mock
`mock_message_history_repository.go` régénéré (méthode ajoutée à la main, mockgen cassé).
**Pas de migration, pas de `config.VERSION` bump** (la colonne V53 suffit).

**Validation E2E ON-PREMISE (tier 🔴, 2026-06-18)** : prouvée contre la vraie DB
staging via le prédicat EXACT du gate, ZÉRO mail. `cold-simulate.class_cap_decision`
étendu d'un param `sender_domain` (route vers `CountSentSinceForDomainsAndSenderDomain`
quand fourni, fallback `CountSentSinceForDomains` sinon) + harness dédié
`scripts/e2e/cold-cap-par-infra.sh` (workspace jetable vierge → cap google=1 → seed 1
envoi google depuis `infra-a` → infra-a `would_be_capped=true` (compteur 1) / infra-b
`would_be_capped=false` (compteur 0, SÉPARÉ) / global sans `sender_domain`=1 ; recoupé
par 3 COUNT psql directs). Run staging : toutes assertions vertes. Le gate worker
réel consomme le même `CountSentSinceForDomainsAndSenderDomain` → le prédicat testé EST
le code de production.

### DROP DATABASE WITH (FORCE) — wipe orphelin + GC bases workspace orphelines (staging, 2026-06-18)

Le DROP de base workspace upstream (`workspaceRepository.DeleteDatabase`) fait
`DROP DATABASE IF EXISTS` **sans `WITH (FORCE)`**. Sur staging, le worker
`EmailQueueWorker` poll tous les workspaces en round-robin et rouvre une connexion
entre le `pg_terminate_backend` et le `DROP` → `database is being accessed by other
users` → DROP raté → base orpheline. Observé : 811 bases `notifuse_ws_*` pour 108
records = 703 orphelines. Spec : `todo/done/2026-06-17-orphan-workspaces-staging-db-starvation.md`.
Prouvé à la racine (psql staging) : DROP nu échoue avec 1 backend actif, DROP FORCE
réussit.

- **Fichiers veridian** : `internal/repository/veridian_workspace_drop.go` (méthodes
  sur `workspaceRepository` : `VeridianForceDropDatabase` = REVOKE CONNECT +
  terminate best-effort + `DROP DATABASE ... WITH (FORCE)`, idempotent, identifiant
  sanitize regex `[a-zA-Z0-9_]` ; `VeridianListOrphanWorkspaceDBs` = pg_database moins
  records `workspaces`, diff en Go car catalogue global non joignable ;
  `VeridianForceDropDatabaseByName` ; `VeridianWorkspaceDBPrefix`),
  `internal/service/veridian_workspace_db_cleanup.go` (DROP FORCE de rattrapage
  dans `wipeOneTenant`, `ConfigureWorkspaceDBCleanup(prefix)`),
  `internal/service/veridian_orphan_db_gc.go` (`VeridianGCOrphanWorkspaceDBs` :
  liste + DROP SÉQUENTIEL, exclut `defaultSafetyClientPrefixes` dont `canary`, cap +
  dry_run), `internal/http/veridian_orphan_db_gc_handler.go` (endpoint).
- **Pas d'import `repository` depuis `service`** (cycle `automation_postgres→service`) :
  la capacité DROP du repo est détectée par **type-assertion** sur une interface
  étroite (`veridianForceDropper` / `veridianOrphanDBGCRepo`). Zéro patch upstream.
- **Wipe (Partie A)** : `wipeOneTenant` DROP FORCE en rattrapage APRÈS
  `DeleteWorkspace` → couvre la race ET le cas record-absent/base-restante. Câblé
  via `ConfigureWorkspaceDBCleanup(config.Database.Prefix)` dans app.go (actif
  partout, jamais destructif sur une base à record). Best-effort.
- **GC (Partie B)** : endpoint **STAGING-ONLY** (503 hors staging, garde-fou comme
  cold-simulate) `POST`+`GET /api/veridian/admin/gc-orphan-workspace-dbs` (HMAC,
  POST+GET routés = anti-catchall). DROP **SÉQUENTIEL** (jamais en rafale = piège
  crash container vécu `include_orphans:true` en batch), exclut canary + clients
  réels. Validé E2E réel : 811→153 en 2 passes (838 DROP, 0 erreur, container
  healthy, 3 canary intactes). Wipe prouvé : tenant jetable → base ABSENTE de
  pg_database après wipe.
- **Partie C (TTL)** : le cron `tst*` existant profite du DROP FORCE de rattrapage.
  Reste-à-faire (ticket de suite si l'accumulation reprend) : un sweep cron
  staging-only appelant `VeridianGCOrphanWorkspaceDBs` pour les orphelines hors
  prefix `tst`. La cause racine étant supprimée, l'accumulation devrait ralentir.

⚠️ **Diffs INLINE supplémentaires** (DROP FORCE wipe + GC) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/veridian_service.go` | +champ `dbPrefix` sur `veridianService` ; `wipeOneTenant` : +appel `forceDropWorkspaceDBBestEffort` (DROP FORCE de rattrapage) après `DeleteWorkspace` |
| `internal/http/veridian_handler.go` | +champ `orphanDBGC` + routes `POST`/`GET /api/veridian/admin/gc-orphan-workspace-dbs` |
| `internal/app/app.go` | +`ConfigureWorkspaceDBCleanup(config.Database.Prefix)` (type-assert) + `SetOrphanDBGC(service, config.Environment)` après le câblage cold-simulate |

### Wipe RECORD-FIRST — couper la ré-élection worker avant le DROP (staging, 2026-06-19)

Le force-drop d'hier (DROP FORCE) traitait le SYMPTÔME (la base) mais pas la CAUSE :
après un wipe, la base droppée était **RECRÉÉE dans la seconde**. Cause racine : le
`Delete` upstream (`workspace_postgres.go:250`) fait **DROP DATABASE D'ABORD**, puis
supprime le record `notifuse_system.workspaces`. Quand le DROP rate sur la race
« being accessed by other users » (le worker round-robin rouvre une connexion entre
`pg_terminate_backend` et le DROP), `Delete` **return tôt → le record `workspaces`
SURVIT**. Or le worker élit les workspaces via `List()` = `SELECT … FROM workspaces`
(`worker.go:189`) : tant que le record vit, le worker ré-élit le ws mort et une task
segment-queue EN VOL **recrée la base** (`init.go`, 25 tables, oid récent). Le
force-drop n'était donc jamais la DERNIÈRE opération. Spec : ticket
`todo/done/2026-06-19-wipe-recree-base-workspace-record-system-survit.md`. Workaround
manuel validé (record-first) confirmé en prouvant le diagnostic.

- **Fix (voie propre, record-first)** : dans `wipeOneTenant`, supprimer le **record
  système EN PREMIER** (`workspaces` puis `user_workspaces` + `workspace_invitations`),
  PUIS DROP FORCE la base **en DERNIER**. La suppression du record vit sur la base
  SYSTÈME (indépendante de la base workspace → JAMAIS bloquée par la race sur la base
  workspace) → elle coupe immédiatement la ré-élection worker → plus aucune task ne
  peut recréer la base après le force-drop.
- **Fichier veridian** : `internal/repository/veridian_workspace_drop.go`
  (`VeridianDeleteWorkspaceSystemRecord` : 3 DELETE system-DB, `workspaces` en premier,
  idempotent — 0 row = OK, nil-systemDB rejeté). Service
  `internal/service/veridian_workspace_db_cleanup.go` (capacité type-assert
  `veridianSystemRecordDeleter` + `deleteWorkspaceSystemRecordBestEffort` → retourne
  `recordCut bool`). Aucun import `repository` depuis `service` (même pattern
  type-assertion que le force-drop). **Pas de migration** (DELETE sur tables system
  existantes).
- **Garde-fou prefix/canary** : inchangé — la suppression du record n'est atteinte
  qu'à l'intérieur de `wipeOneTenant`, lui-même gardé par `defaultSafetyClientPrefixes`
  (canary + clients réels) en amont dans `WipeTestTenants`. Un record canary*/client
  réel n'arrive JAMAIS au record-first.
- **Tolérance erreur DeleteWorkspace bénigne** : si le record-first a réussi
  (`recordCut`) et que le force-drop est fait, une erreur résiduelle de
  `DeleteWorkspace` (ex. `ErrWorkspaceNotFound` car le record est déjà parti, ou auth
  owner échouée) est traitée comme bénigne (log, pas d'échec). Best-effort : record-first
  en erreur → on continue (DeleteWorkspace upstream nettoiera, ou le prochain wipe/GC).
- **Validé E2E ON-PREMISE staging** : workspace jetable AVEC activité (broadcast en
  queue → worker l'élit) → wipe → record ABSENT + base ABSENTE de pg_database + **NE
  RÉAPPARAÎT PAS après 90s** (avant : base recréée avec oid récent dans la seconde).

⚠️ **Diffs INLINE supplémentaires** (wipe record-first) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/veridian_service.go` | `wipeOneTenant` réordonné : `deleteWorkspaceSystemRecordBestEffort` (record-first) AVANT `DeleteWorkspace`, force-drop EN DERNIER ; nouveau cas `recordCut` (erreur DeleteWorkspace bénigne si record déjà coupé) |

Fichiers veridian touchés : `internal/repository/veridian_workspace_drop.go`
(`VeridianDeleteWorkspaceSystemRecord`), `internal/service/veridian_workspace_db_cleanup.go`
(`veridianSystemRecordDeleter` + `deleteWorkspaceSystemRecordBestEffort`) + tests
colocalisés.

### Sync upstream

```bash
git checkout main && git pull upstream main
git checkout veridian && git merge main
```

Pre-push hook détecte les commits dont l'auteur est `@notifuse.com` et bypasse
le mapping 1-pour-1 sur les fichiers **non-veridian_***. Les fichiers
`veridian_*.go` restent sous discipline stricte même dans un sync upstream.

### Déploiement — GitOps Nomad (⚠️ Dokploy DÉCOMMISSIONNÉ 2026-07-10)

Migration Dokploy → **cluster Nomad 3 nœuds** terminée. Le déploiement passe
désormais par des **jobs Nomad versionnés dans CE repo** (`deploy/*.nomad.hcl`),
poussés par la CI via `nomad job run` (plus de Dokploy, plus de `infra/compose/*.yml`).

- **Image** : `ghcr.io/christ-roy/notifuse-veridian:<tag>`
- **Jobs** : `deploy/notifuse.nomad.hcl` (prod, contabo-bastion) +
  `deploy/notifuse-staging.nomad.hcl` (ovh-dev, privé Tailscale + internal-only).
  Source de vérité gitops de l'app ; miroir infra : `~/nomad-veridian/jobs/`.
- **Deploy — canon SSH-bastion** (décision Robert, cf `veridian-prospection/deploy/README.md`) :
  CI `veridian-ci.yml` → `scripts/ci/nomad-ssh-deploy.sh <env> <tag>` → SSH vers le
  bastion (clé dédiée CI), pré-pull image ghcr (auth du nœud), scp le HCL (qui déclare
  `variable image_tag`), `nomad job run -var image_tag=<tag>` + `deployment status
  -monitor`. **Le NOMAD_TOKEN ne quitte JAMAIS le bastion** (lu in situ). deploy-staging
  = runner self-hosted (steps post-deploy tailnet) ; deploy-prod/rollback = ubuntu-latest.
- **Rollback** : `nomad job revert notifuse <version-1>` via SSH-bastion (job `rollback`,
  auto sur e2e-prod fail). Stanza `update{auto_revert=true}` = filet Nomad si deployment KO.
- **Secrets CI** : `NOMAD_DEPLOY_SSH_KEY` (clé ed25519 dédiée notifuse, publique dans
  authorized_keys bastion) + `NOMAD_BASTION_HOST` + `NOMAD_BASTION_USER`. Secrets
  applicatifs = Nomad Variables `nomad/jobs/notifuse{,-staging}` (`template{env=true}`).
  ⚠️ Piège n°1 : Nomad ne pull pas les images privées ghcr → pré-pull authentifié +
  auth ghcr root sur les nœuds (bastion + ovh-dev, déjà posé).
- **Endpoints** : staging `notifuse.staging.veridian.site`, prod `notifuse.app.veridian.site`
- **Pilotage cluster** : skill `/nomad` (`nomad-v state`/`doctor`/`plan`/`deploy`).
  Control-plane = bastion Contabo. Détail migration : ticket
  `todo/2026-07-11-migration-ci-gitops-nomad.md`.
- ⚠️ **Legacy à nettoyer** (non bloquant) : `infra/compose/{staging,prod}.yml`,
  `scripts/ci/bump-compose-image.sh`, job `compose-validate`, secrets `DOKPLOY_*` —
  morts depuis la décommission Dokploy.

### Tests E2E Veridian

`tests/e2e-veridian/` (Playwright). Tag `@prod-safe` pour les tests
read-only autorisés à tourner sur prod.

### Rate-limit GLOBAL de l'API (OWASP API4:2023, 2026-06-15)

Le `pkg/ratelimiter` upstream n'était appliqué qu'À LA MAIN, handler par
handler, et UNIQUEMENT sur les endpoints publics non-auth (subscribe /
preferences). TOUS les endpoints JWT/HMAC (dont le custom Veridian cold :
breakdown, attach-member, sso, automations.*) étaient sans rate-limit → trou
API4:2023 (Unrestricted Resource Consumption), critique vu que Notifuse va être
peuplé en prod. Fermé par un middleware GLOBAL en defense-in-depth.

- **Fichier veridian** : `internal/http/middleware/veridian_rate_limit.go`
  (`VeridianAPIRateLimitMiddleware`). Wrappe le handler racine → couvre TOUTES
  les routes du mux `a.mux` par construction. Limite par 2 dimensions
  indépendantes : **IP** (X-Forwarded-For premier hop, défaut 300/min) +
  **identité authentifiée best-effort** (user_id décodé du Bearer JWT SANS
  valider la signature, ou `x-veridian-app`+workspace HMAC ; défaut 600/min).
  L'identité est une clé de bucketing, PAS une frontière de sécu (un user_id
  forgé se fait juste limiter sur une autre clé ; la dimension IP reste). 429 +
  header `Retry-After`. Réutilise le RateLimiter PARTAGÉ `a.rateLimiter` (pas de
  2e goroutine de cleanup). Best-effort : `rl==nil` ou désactivé = passthrough.
- **Exemptions** : `/api/health`, `/api/version`, `/api/tenants/{id}/health`
  (observabilité + smoke CI) + flux HMAC Hub (`X-Veridian-Hub-Signature`
  présent : déjà protégé signature+anti-replay, et le cron reconcile Hub fait
  des rafales légitimes — limite basse = faux positifs `tenant_missing_app`).
  Tout ce qui n'est pas sous `/api/` (console SPA, assets, `/subscribe`,
  `/preferences`, `/health`, `/healthz`) n'est pas inspecté.
- **Config** : ENV `VERIDIAN_API_RATE_LIMIT_{ENABLED,PER_IP,PER_IDENTITY}`, lue
  AU CÂBLAGE dans le middleware (pattern `VeridianSecurityHeadersMiddleware` qui
  lit `CORS_ALLOW_ORIGIN`) — **PAS** d'ajout au struct `config.Config` upstream.
  Défaut activé + seuils sains ; valeur invalide → défaut (best-effort boot).
- **Garde-fou Husky/CI** : `scripts/ci/check-rate-limit-coverage.sh` (appelé par
  `.husky/pre-push` + step CI dans le job `test-mapping`). BLOQUE si le
  middleware global est dé-câblé d'app.go (règle 1). ALERTE non bloquant sur un
  `http.NewServeMux()` hors allowlist (`app.go` + `pkg/tracing` metrics) — un mux
  parallèle servant de l'API la bypasserait. Le script documente honnêtement ses
  limites (pas d'analyse de flot, ne vérifie pas les seuils).

⚠️ **Diffs INLINE supplémentaires** (rate-limit global) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/app/app.go` | +câblage `middleware.VeridianAPIRateLimitMiddleware(a.rateLimiter, ...)` dans `Start()`, entre graceful-shutdown et tracing (rejet 429 avant les lookups DB paywall/frozen) |
| `.husky/pre-push` | +étape appelant `check-rate-limit-coverage.sh` (câblage bloquant, mux parallèle informatif) |
| `.github/workflows/veridian-ci.yml` | +step `Run check-rate-limit-coverage.sh` dans le job `test-mapping` (transverse, tous events) |

---

## Invariants CONTRAT-HUB v1.5 (gravés 2026-05-21/22)

> **Source de vérité** : `../veridian-hub/docs/CONTRAT-HUB.md` v1.5 +
> `../veridian-hub/docs/CONTRAT-HUB-API-REF.md` v1.1. Lire ces docs
> avant toute modif de `internal/service/veridian_service.go`,
> `internal/http/veridian_handler.go`, `internal/repository/veridian_*`.

### §1.4 — Hub source de vérité + résilience apps

**Anti-pattern interdit** : call Hub synchrone dans un hot path utilisateur
(login, page protégée). Notifuse doit pouvoir continuer à fonctionner si
le Hub est down (mode dégradé "best effort") :
- Plan lookup : colonne locale `tenants.plan` + cache TTL 5 min
- Magic link déjà émis : reste valide même si Hub down (token signé)
- API key tenant : 100 % locale (hash dans `workspaces.api_key_hash`)
- Webhooks app → Hub : best-effort 3 retries backoff puis log + skip

### §1.4bis — Résilience billing niveau 1 (`last_hub_sync_at`)

3 phases mesurées par middleware paywall :
- **Fresh** < 24h : mode normal
- **Stale** 24h-72h : grace period optimiste + log warn rate-limité
- **Dead** > 72h : writes → `503 hub_sync_dead`, reads passent

Constantes côté Notifuse : `HubSyncFreshThreshold` / `HubSyncDeadThreshold`
(migration V39 livre la colonne, middleware `EvaluateHubSyncStatus`).

### §3.7 — Modèle identité user cross-app

Trois identifiants distincts :
- **`hub_app.users.id`** (Hub) — source de vérité unique de l'identité
- **`notifuse.users.id`** (local) — PK interne, distinct par construction
- **`notifuse.users.hub_user_id`** (V46, nullable) — backfillé au 1er contact

**Règles de jointure** :
- Hub appelle Notifuse → body inclut `email` + `hub_user_id`. Notifuse
  résout `hub_user_id` en local, sinon `email`, sinon crée user.
- Notifuse appelle Hub → utilise `email` ou `hub_user_id` selon dispo.
- App appelle app → **INTERDIT** (tout cross-app passe par le Hub).

**Garde-fous** : un même `hub_user_id` ne peut être lié qu'à un seul
user Notifuse (invariant V46). Jamais changer le `hub_user_id` d'un
user existant sans audit + migration explicite.

### §4.4 — Cycle de vie membre (2 manières exclusives)

Un humain peut être lié à un workspace Notifuse de **2 manières** :

1. **Owner** : click "Commencer l'essai" sur SON Hub Dashboard → Hub
   appelle `POST /api/tenants/provision` → Notifuse crée workspace + user
   owner + api_key.
2. **Membre invité** : un autre humain l'invite via `/dashboard/team` →
   Hub envoie magic link → user accepte → Hub appelle
   `POST /api/veridian/workspaces/{id}/attach-member` (ou `/api/tenants/{id}/attach-member`).

Le signup Hub **ne crée AUCUN tenant ni AUCUN membership** côté Notifuse.

### §5.18.2 — DÉPRÉCIÉ : doublon admin invite-member

Le doublon `POST /api/admin/tenants/{id}/invite-member` côté Hub est
déprécié en v1.5. Tout flow invitation passe par P1 §5.22 (endpoint
`/api/invitations/create` côté Hub + `/api/veridian/workspaces/{id}/attach-member`
côté apps). Notifuse n'a jamais consommé l'endpoint déprécié : rien à
nettoyer côté app.

### §5.22.2 — `attach-member` workspace-level

Notifuse expose **deux routes** vers le même handler (mono-workspace :
`tenantId == workspaceId`) :
- `POST /api/tenants/{tenantId}/attach-member` (historique tenant-level lot B 2026-05-21)
- `POST /api/veridian/workspaces/{tenantId}/attach-member` (alias v1.5 conforme §5.22.2)

### §5.22.4 — JAMAIS écraser un rôle existant

Si un user est déjà membre du workspace avec un rôle différent de celui
demandé par le Hub : retourner 200 `already_member=true` avec le **rôle
LOCAL inchangé**, log info `role conflict ignored`. L'admin Notifuse a
le contrôle souverain sur ses rôles internes (§5.18.4 : Hub non-autoritatif).
**Pas de UPDATE remove+re-add** (downgrade silencieux interdit).

### §5.22 — Endpoints membre actifs cross-app

| Niveau | Endpoint Notifuse | Auth | Cas |
|---|---|---|---|
| Workspace | `POST /api/veridian/workspaces/{id}/attach-member` (§5.22.2) | HMAC | Voie normale : invitation user-side |
| Workspace | `POST /api/tenants/{tenantId}/attach-member` (alias) | HMAC | Equivalent mono-workspace |
| Tenant | `POST /api/tenants/{id}/sync-member` (§5.18.3) | HMAC | Voie admin/migration script |
| Webhook | `tenant.member_role_changed` (§5.18.4) | Bearer | App → Hub, audit only |

### §6bis.7 — Logout cross-app local

Modèle "logout local" : chaque app gère son logout indépendamment. Pas
de propagation cross-app obligatoire. Scope cookie en staging :
`.staging.veridian.site` (multi-tenant subdomain).

### §7.1 — Webhooks app → Hub étendus

Nouveaux events disponibles côté Notifuse (à émettre quand applicable) :
- `tenant.member_role_changed` (élévation/abaissement local)
- `tenant.member_added` (post sync-member réussi)
- `tenant.member_removed` (post remove-member réussi)

### §11bis — Permissions cross-app

Matrice des droits par action documentée côté Hub (CONTRAT-HUB-API-REF
section PERMS). Notifuse reçoit le `target_role` du Hub mais peut
l'**élever** localement via UI Team Settings (pattern §5.18.4 informatif).

---

## Secrets HMAC cross-app — matrice exhaustive

> **But** : éviter qu'un agent perde 30 min à chercher quel secret /
> header / canonical-string utiliser pour un endpoint HMAC donné. Tout
> nouveau endpoint HMAC cross-app DOIT étendre cette table.
>
> Symétrie : un secret HMAC est partagé entre les 2 parties. Le **même
> matériel cryptographique** vit sous des **noms d'env divergents** côté
> Notifuse vs côté Hub. La colonne "même valeur" l'indique explicitement.

### Vue d'ensemble par flux

| Sens du flux | Env Notifuse | Env Hub | Header signature | Canonical string | Endpoint(s) |
|---|---|---|---|---|---|
| Hub → Notifuse (mutations + reads admin + cron reconcile GET) | `HUB_API_SECRET` | `NOTIFUSE_HUB_API_SECRET` (= même valeur) | `X-Veridian-Hub-Signature` | **Toujours `${ts}.${rawBody}`** quel que soit la méthode HTTP. Pour un GET (body vide), `rawBody = ""` donc canonical = **`${ts}.`** (timestamp + point + chaîne vide). Pattern volontairement "pixel-parfait" côté Hub (`lib/sync/discovery.ts:signGet` lignes 105-114 : "On garde le format `${ts}.${rawBody}` ici aussi (body=''), pour rester pixel-parfait avec les clients existants") afin d'éviter d'avoir 2 conventions HMAC à gérer côté app. Validé par smoke prod 2026-05-25 : `${ts}.` → 200 ; `${ts}.GET.${path}?${query}` → 401. | `/api/tenants/*`, `/api/veridian/admin/*`, `/api/veridian/workspaces/{id}/attach-member`, `/api/sso/issue-magic-link`, **`POST /api/users/by-email`** ET **`GET /api/users/by-email`** (les 2 routées — cf. piège catchall plus bas) |
| Notifuse → Hub (discovery user, GET au login user) | `HUB_API_SECRET` (même secret) | `NOTIFUSE_HUB_API_SECRET` (même) | `x-veridian-hub-signature` (lowercase pour outbound, accepté côté Hub) | **`${ts}.${METHOD}.${pathname}?${sortedQuery}`** — pattern OWASP "HMAC Signing for GET requests" (cf. `veridian-hub/lib/discovery/hmac.ts:buildCanonicalGetString` lignes 80-100). METHOD en majuscule. Tri alphabétique des clés de query (anti-malléabilité proxy). Encodage `encodeURIComponent`-compatible (PAS `url.QueryEscape` côté Go — diverge sur espaces). Réf code Notifuse outbound : `pkg/hub_discovery/client.go:buildCanonicalGetString` lignes 293-302. ⚠️ **Convention différente du flux Hub→Notifuse** : ce sens-là utilise bien `${ts}.METHOD.path?query`, contrairement au flux inverse qui reste sur `${ts}.${rawBody}`. | `GET <hub>/api/users/by-email?email=...` |
| Notifuse → Hub (invitation cross-app) | `HUB_INVITATION_SECRET_NOTIFUSE` | `HUB_INVITATION_SECRET_NOTIFUSE` (même nom) | `x-veridian-invitation-signature` | `${ts}.${rawBody}` | `POST <hub>/api/invitations/create` |
| Notifuse → Hub (webhooks lifecycle) | `HUB_WEBHOOK_SECRET` (+ `HUB_WEBHOOK_URL`) | `NOTIFUSE_HUB_WEBHOOK_SECRET` (= même valeur) | `X-Veridian-Notifuse-Signature` | `${ts}.${rawBody}` | `POST ${HUB_WEBHOOK_URL}` (cf. §7.1 events `tenant.*`, `email.*`) |

### Headers communs à toutes les requêtes HMAC

- **`x-veridian-app`** : nom canonique de l'app caller (`notifuse`,
  `prospection`, `analytics`, `cms`). Côté Hub, sélectionne le bon
  secret. Côté Notifuse, pas vérifié (un seul secret possible).
- **`x-veridian-timestamp`** : Unix epoch en **millisecondes**. Drift
  max anti-replay : **5 minutes** (constante `MaxClockDrift` côté
  Notifuse, équivalent côté Hub). Body lu avec `MaxBodySize = 1 MiB`.

### Où trouver les valeurs

- **Source de vérité dev** : `~/credentials/.all-creds.env` (noms
  canoniques `NOTIFUSE_HUB_API_SECRET`, `NOTIFUSE_HUB_WEBHOOK_SECRET`,
  `HUB_INVITATION_SECRET_NOTIFUSE`, etc.)
- **Prod/Staging** (Dokploy décommissionné 2026-07-10) : les secrets vivent
  dans les **Nomad Variables** du job (`nomad/jobs/notifuse`,
  `nomad/jobs/notifuse-staging`, `nomad/jobs/hub`), injectés au conteneur via
  `template { env = true }`.
- **Inspection live** : `nomad var get nomad/jobs/notifuse` (valeurs de la
  Variable) ou `nomad-v exec <alloc> printenv | grep HUB` (ENV réelles du
  conteneur), plutôt que l'ancienne `POST /api/compose.one` de l'API Dokploy.

### Conventions code (où regarder)

- `internal/http/middleware/veridian_hmac.go` — middleware **inbound**
  (Hub → Notifuse), header `X-Veridian-Hub-Signature`, canonical
  `${ts}.${rawBody}`. 503 si `HUB_API_SECRET` vide (pas 401). Sait
  gérer les deux modes : si body absent (GET), `rawBody = ""` et le
  canonical devient `${ts}.` (timestamp + point sec).
- `internal/http/veridian_discovery_handler.go` — handlers **inbound**
  jumeaux `handleDiscovery` (POST) et `handleDiscoveryGET` (GET) pour
  `/api/users/by-email`. Les deux sont nécessaires : POST sert le
  SDK/UI Notifuse, GET sert le cron reconcile Hub. Si un seul est
  routé, l'autre tombe dans le catchall SPA (cf. piège).
- `pkg/hub_discovery/client.go` — client **outbound** GET signé pour
  `/api/users/by-email`. Constantes `AppHeaderName`,
  `TimestampHeaderName`, `SignatureHeaderName` exportées. **encodage
  `encodeURIComponent`-compatible** (PAS `url.QueryEscape` — diverge sur
  espaces et caractères réservés). Tri alphabétique des clés impératif
  pour matcher `lib/discovery/hmac.ts` côté Hub.
- `internal/service/veridian_hub_invitation_client.go` — client
  **outbound** POST signé pour `/api/invitations/create`. Headers
  lowercase. Mode "disabled" silencieux si `HUB_INVITATION_SECRET_NOTIFUSE`
  vide (retourne `ErrHubInvitationDisabled`).
- `internal/service/veridian_webhook_emitter.go` — emitter **outbound**
  POST best-effort vers `HUB_WEBHOOK_URL`. Header `X-Veridian-Notifuse-Signature`
  (note : **différent** du header inbound, car le Hub a besoin de
  distinguer l'origine du signal). Noop si URL ou secret manquants.

### Pièges historiques (vécus, pas hypothétiques)

- **Catchall `root_handler.go` qui mange les méthodes HTTP non routées**
  (incident 2026-05-25, P0 prod) : Go 1.22+ exige la méthode HTTP
  explicite dans `mux.Handle("METHOD /path", ...)`. Si tu route
  uniquement `POST /api/foo` et qu'un caller appelle `GET /api/foo`, le
  mux **ne match pas** → la requête tombe sur le catchall
  `root_handler.go` qui sert la **SPA console** (HTML 200) → le caller
  parse le body comme JSON et lit "body vide" silencieusement. Aucun log
  d'erreur côté Notifuse, aucun 404, juste un 200 trompeur. Vécu :
  17 faux positifs `tenant_missing_app` côté Hub reconcile cron parce
  que `GET /api/users/by-email` n'était pas routé. **Règle** : pour tout
  endpoint HMAC critique, router POST **ET** GET explicitement (même si
  un seul est utilisé aujourd'hui — l'autre cause un crash silencieux le
  jour où un nouveau caller arrive). Smoke test obligatoire : `curl -X
  <METHOD>` chaque méthode déclarée dans la matrice ci-dessus.
- **`HUB_INVITATION_SECRET_NOTIFUSE` absent des composes Dokploy prod**
  jusqu'au 2026-05-23 (corrigé en session). Toujours vérifier les ENV
  des **DEUX** composes (Notifuse `WN0jglLj5bDIrXUFZHNmw` ET Hub
  `_kxAHDCv1LhvsdwNRX3Vk`) en parallèle quand un nouveau secret HMAC
  est introduit — sinon HMAC marche en staging et plante en prod.
  À noter : ce secret n'est PAS dans `infra/compose/{staging,prod}.yml`
  du repo Notifuse, il est injecté directement via l'UI/API Dokploy.
- **Canonical string ASYMÉTRIQUE selon le sens du flux pour le MÊME
  secret** (`HUB_API_SECRET` / `NOTIFUSE_HUB_API_SECRET`) :
  - **Hub → Notifuse** (inbound côté app, y compris GET cron reconcile) :
    `${ts}.${rawBody}` toujours. GET = `${ts}.` (body vide).
    Réf : `internal/http/middleware/veridian_hmac.go` (Notifuse inbound)
    + `veridian-hub/lib/sync/discovery.ts:signGet` (Hub outbound).
  - **Notifuse → Hub** (outbound discovery) :
    `${ts}.${METHOD}.${pathname}?${sortedQuery}`.
    Réf : `pkg/hub_discovery/client.go:buildCanonicalGetString` (Notifuse
    outbound) + `veridian-hub/lib/discovery/hmac.ts:buildCanonicalGetString`
    (Hub inbound).

  Coller le mauvais format = `401 Invalid signature` opaque. Le smoke
  prod 2026-05-25 a confirmé l'asymétrie : un canonical
  `${ts}.GET.${path}?${query}` envoyé sur le flux Hub→Notifuse retourne
  401 ; seul `${ts}.` (body vide) passe.
- **Tri alphabétique des query params obligatoire** sur la signature GET
  côté Notifuse→Hub uniquement (le flux Hub→Notifuse ne signe pas le path).
  Cf. `encodeSortedQuery` dans `pkg/hub_discovery/client.go`.
- **Ne JAMAIS écrire une cellule de cette matrice sans avoir lu le code
  source des DEUX parties (client signataire + serveur vérificateur)**.
  Cette matrice a été corrigée en v3 (2026-05-25) après que le team-lead
  vague 4 ait perdu plusieurs minutes en validation P0 parce que la v2
  documentait `${ts}.GET.${path}?${sortedQuery}` côté flux Hub→Notifuse
  alors que le vrai code Hub `lib/sync/discovery.ts:signGet` (lignes
  105-114) signe `${ts}.`. Réflexe minimum avant d'éditer cette section :
  `grep -rn "createHmac\|hmac.New" <repo>/{lib,pkg,internal}/` côté
  caller ET côté receiver, et coller la ligne de code exacte dans la
  cellule (pas une paraphrase). Une doc qui contredit le code est pire
  qu'une doc absente — elle envoie les agents dans le mur avec
  confiance.
- **Header webhook ≠ header inbound** : outbound webhook utilise
  `X-Veridian-Notifuse-Signature`, inbound mutations utilise
  `X-Veridian-Hub-Signature`. Symétrie volontaire : le destinataire sait
  à quel secret matcher.
- **Validation UUID stricte côté Notifuse `hub_user_id`** (V46) : si
  payload Hub envoie un non-UUID, stocké comme NULL silencieusement
  (cf. fix `7d5b352d`). HMAC valide mais data perdue.
- **Casse des headers HTTP** : Go normalise via `r.Header.Get` (case
  insensitive) donc lowercase outbound et CamelCase inbound coexistent
  sans problème. Ne pas s'alarmer si on voit les deux dans le code.

### Vue Hub-side

La VUE Hub (quels endpoints exposent l'inbound HMAC pour chaque app,
quelles ENV `<APP>_HUB_API_SECRET` sont définies) est maintenue côté
Hub. Voir `../veridian-hub/CLAUDE.md` section équivalente — ticket
ouvert `../veridian-hub/todo/2026-05-25-secrets-hmac-cross-app-doc-CLAUDE.md`
pour la créer en miroir.

---

## Pricing — source de vérité

> **Source unique cross-app** : `../veridian-hub/docs/PRICING-VERIDIAN.md`.
> Lire ce doc avant tout travail pricing/trial/paywall/feature gate.

**Philosophie figée par Robert 2026-05-21** : générosité maximale, **tout
illimité partout y compris Free** (emails, contacts, OAuth BYO, automation,
seats, custom domains, A/B testing, historique). L'app ne doit **jamais** être
défigurée par des limites visibles ou murs béton.

**Seules différenciations** :
- Free → durée 15j visibles (révélée à J+2 après 5 mails envoyés) puis paywall
- Business 99€ vs Pro 29€ → white-label custom (footer client custom)

**Flow trial** : signup silencieux → 5 mails déclenchent timer 2j serveur
invisible → J+2 bandeau trial 15j → ajout CB = cadeau **inconditionnel** de
30j → débit auto Pro à expiration si CB présente, sinon paywall lecture seule.
Détails complets : doc Hub.

### Interdits côté code

- Mur béton `402 Payment Required` sur une feature
- Compteur visible "il vous reste X mails / contacts / domaines"
- Menu grisé "🔒 Pro", pop-up "passez Pro pour faire ça"
- Branding obligatoire qui dégrade les emails du client
- Toute limite enforced sur contacts / OAuth / seats / automation / historique / custom domains / A/B
- Affichage du timer trial **avant J+2** (le timer 2j post-5mails reste invisible UI)

### Acceptable côté code

- Bandeau trial visible uniquement en phase 4+ (J+2)
- Compte à rebours pendant les 15j (puis 30j si CB)
- Lien Upgrade, paywall lecture seule à expiration
- White-label custom = différenciation Business+ uniquement

### État côté Notifuse (post-pivot 2026-05-21)

- **V37 lots 4b/4c/4d et 5** : tous annulés (aucun enforcement de dimensions)
- **Lot 4a A/B feature gate** : reverté (A/B gratuit pour tous, `featureGatedPaths` vide)
- **DefaultPlanLimits** : tout à `-1` / `true` sauf `FeatureWhiteLabel` (Business+ uniquement)

### Compteurs invisibles (télémétrie interne)

- `emails_sent_lifetime` : signal d'activité
- `activity_threshold_reached_at` (post-5e mail) : timestamp serveur consommé par Hub
- **Jamais exposés UI client** tant que phase 3 (J+2) n'est pas atteinte

### Tickets actifs reliés

- `todo/2026-05-21-trial-eligible-signal.md` (signal 5 mails Notifuse→Hub)
- `todo/2026-05-21-paywall-degraded-mode-soft-deleted.md` (UX dégradée)
- `todo/2026-05-20-pricing-plans-implementation.md` (V37)
- Hub : `2026-05-21-trial-state-machine.md`, `2026-05-21-stripe-webhook-orchestrator.md`

---

## Constitution CI — règles non négociables

Standard de référence : `../CI-ARCHITECTURE.md` (racine `veridian-platform/`).
Adaptations Go pour ce repo :

1. **1 fichier critique = 1 test colocalisé.** Scopes :
   `internal/{http,service,repository,domain}/**/*.go`. Convention Go :
   `foo.go` ↔ `foo_test.go` au même niveau. Zéro exception.
2. **Pre-push hook bloquant** via `.husky/pre-push` →
   `scripts/ci/check-test-mapping.sh`. Setup : `make setup-hooks`.
3. **Jamais `--no-verify`.** Si le hook bloque : fix le test ou ajoute à
   `tests-pending.txt` (dette tracée).
4. **Règle 1-pour-1 stricte** : chaque nouvelle func exportée dans un fichier
   critique = au moins un nouveau `TestXxx` dans le test colocalisé.
5. **`tests-pending.txt`** : baseline transitoire, cible 0 sous 90 jours. Toute
   ligne retirée doit s'accompagner du test.
6. **`test-coverage-map.yaml`** : si un fichier est légitimement couvert
   ailleurs, déclarer avec `reason:` explicite + `covered_by`.
7. **Exception upstream sync** : auteur `@notifuse.com` bypasse le mapping
   pour les fichiers **non-veridian_***. Les `veridian_*.go` restent sous
   discipline stricte.
8. **Skip docs** : `paths-ignore: ['**.md', 'docs/**', 'runbooks/**', 'plans/**']`.
9. **Promotion prod — L'AGENT TRANCHE (gravé Robert 2026-06-16).**

   **C'est l'agent propriétaire de Notifuse qui décide de la promo prod, pas
   Robert.** Robert délègue le résultat : il ne valide PAS chaque push, il
   n'attend PAS au bout d'une notif Telegram. L'agent juge le risque, lance les
   tests qu'il faut, promeut quand c'est vert, rend compte après. Robert
   intervient seulement (a) en veto explicite (`stop`/`rollback`/`freeze`), ou
   (b) sur le tier 💀 destructif-irréversible (cf. ci-dessous).

   **Le critère de promo n'est PAS un marker, c'est le NIVEAU DE PREUVE atteint.**
   L'agent classe son lot par risque et choisit la batterie de tests en
   conséquence — les unit tests de la CI sont le PLANCHER, jamais le plafond :

   | Tier | Exemples | Preuve EXIGÉE avant promo (par l'agent) | Promo |
   |---|---|---|---|
   | 🟢 BAS | doc, todo, tests-only, refactor sans surface API | CI verte (unit + mapping) | Marker `[risk:low]` → auto-promote |
   | 🟡 MOYEN | UI, route non-auth, bump dep patch | CI verte + smoke ciblé staging (curl/Chrome sur la surface touchée) | Agent promote (`workflow_dispatch deploy_prod=true`) après smoke OK |
   | 🔴 HAUT | envoi mail core, throttle/pixel cold, HMAC, lib partagée, gros bump dep (CVE), sync upstream | CI verte + **TEST ON-PREMISE staging** = E2E réel sur le vrai système (DB/SMTP staging, sink local si besoin), état vérifié à la main, pas un mock | Agent promote après E2E on-premise vert + monitoring 10 min post-deploy |
   | 💀 CRITIQUE | DROP COLUMN, rotation secret prod, suppression tenant prod, migration destructive | **SEUL tier où l'agent demande go/stop à Robert** | Robert tranche |

   **Test on-premise = complément OBLIGATOIRE des unit tests sur le tier 🔴.**
   La CI ne lance que du Vitest/Go-test qui mocke SMTP/DB/apps downstream — un
   mock vert ne prouve RIEN sur le flux réel (incident `pk_test_fake` 2026-05-23,
   bug pixel-workspace `workspace=nil` 2026-06-13 : tests unit verts, flux réel
   cassé). Donc pour tout lot 🔴, AVANT de promouvoir, l'agent DOIT dérouler le
   vrai flux sur staging (le vrai worker, la vraie DB staging, le vrai SMTP — ou
   le sink local `aiosmtpd` pour le cold, cf. `todo/2026-06-13-e2e-tunnel-validation-sink-local.md`),
   lire l'état réel (queue, message_history, HTML émis), et ne promouvoir que
   sur preuve observée. Pas d'E2E on-premise vert = pas de promo 🔴.

   **Mécanique de promo** :
   - 🟢 `[risk:low]` dans le subject (1ère ligne) → `deploy-prod` auto.
   - 🟡/🔴 → push SANS `[risk:low]` (staging only), puis l'agent promeut
     lui-même via `gh workflow run veridian-ci.yml -f deploy_prod=true` une fois
     sa batterie de preuve verte. PAS d'attente d'un GO Robert (sauf tier 💀).
   - Stop-gap `[skip-prod]` / `[wip]` : bloquent toujours la prod (WIP en cours).
   - ⚠️ Piège connu (mémoire `feedback_risk_low_doc_commit_auto_promote_trap`) :
     ne JAMAIS finir une vague par un commit doc `[risk:low]` si du tier 🟡/🔴
     non encore validé est dans le même push range — le head-commit `[risk:low]`
     déclenche l'auto-promote de TOUT le lot. Valider d'abord, archiver/documenter
     `[risk:low]` après.
   - Toujours valable : si `Dockerfile`, `go.mod`, `docker-compose.yml`,
     `internal/migrations/**` modifiés → staging vert + e2e-staging vert exigés
     avant toute prod (implicite via `needs`).

   **Fluidité du sprint (gravé 2026-06-16)** : l'objectif est un flow CONTINU,
   pas une file d'attente de validations. L'agent enchaîne coder → push staging
   → preuve (selon tier) → promo → monitoring → ticket suivant, SANS s'arrêter
   demander « je promeus ? » entre chaque. Une vague de team se termine par UNE
   promo groupée des lots mûrs + UN récap, pas par N demandes de GO. Le seul
   point d'arrêt est le tier 💀 ou un veto Robert.
10. **Deploy via Nomad SSH-bastion** (Dokploy décommissionné 2026-07-10, canon
    prospection) : `scripts/ci/nomad-ssh-deploy.sh <env> <tag>` → SSH bastion →
    `nomad job run -var image_tag=<tag>`. Secrets CI : `NOMAD_DEPLOY_SSH_KEY` +
    `NOMAD_BASTION_HOST` + `NOMAD_BASTION_USER`. Le token ne quitte pas le bastion.
11. **Rollback prod auto** sur e2e-prod fail : `nomad job revert notifuse <version-1>`
    via SSH-bastion → wait `/api/setup.status` → Telegram alert.
12. **Migrations Expand & Contract obligatoire**. Le tag Docker précédent doit
    tourner sur le schéma actuel. Versions majeures (V6, V7…) additives ;
    DROP COLUMN / NOT NULL sur table peuplée = 2 PRs sur 2 deploys.
13. **Findings GitHub Security tab** : Trivy + gitleaks SARIF via
    `upload-sarif@v3`. Pas de `.trivyignore` sans VEX écrit.
14. **Renovate auto-merge** (à activer) : patch + minor + CVE auto-mergés si
    CI verte. Major → review humaine.

### Commandes CI utiles

```bash
make setup-hooks                                 # one-time, installe pre-push
BASE_REF=HEAD scripts/ci/check-test-mapping.sh   # test local working tree
BASE_REF=origin/veridian scripts/ci/check-test-mapping.sh  # comme pre-push
wc -l tests-pending.txt                          # voir la dette
git ls-files | grep -E 'veridian_|veridian\.go'  # lister fichiers custom
```

---

## Commandes tests (Makefile)

```bash
make test-unit          # tous les tests unit
make test-domain        # domain layer
make test-service       # service layer
make test-repo          # repository
make test-http          # HTTP handlers
make test-migrations    # migrations
make test-integration   # intégration full stack
make coverage           # rapport HTML
cd console && npm test  # frontend
```

---

## Conventions clés (résumé exécutable)

- **Architecture** : Clean Architecture (domain → service → repository → http). DI par constructor.
- **DB** : Postgres 17, query builder Squirrel, migrations versionnées (`config.VERSION`, `internal/migrations/vN.go` implémentant `MajorMigrationInterface`). Idempotent (`IF NOT EXISTS`), transactionnel, additif.
- **API** : RPC-style `POST /api/<resource>.<verb>`. JWT auth, middleware permissions.
- **Tests Go** : table-driven, Testify (`assert`/`require`/`mock`), GoMock v1.6.0 (`github.com/golang/mock`, **pas** `go.uber.org/mock`), `go-sqlmock` pour la DB.
- **Front console** : React 18 + TS strict + Vite + Ant Design + TanStack Query/Router + Lingui i18n (`useLingui()` + `` t`...` ``).
- **Plans** : si AI-assisted plan, écrire dans `plans/` (kebab-case), inclure stratégie de test + commande `make test-*` à lancer.

Pour le tech stack complet (versions précises, libs front, observabilité, etc.) : lire le `README.md` ou la doc upstream Notifuse.

---

## Claude Agent Rules

- Pas d'auto-attribution : aucune mention "Generated with Claude", "Co-Authored-By: Claude", AI signatures dans commits / releases / PRs / code.
