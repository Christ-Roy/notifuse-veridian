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

### Sync upstream

```bash
git checkout main && git pull upstream main
git checkout veridian && git merge main
```

Pre-push hook détecte les commits dont l'auteur est `@notifuse.com` et bypasse
le mapping 1-pour-1 sur les fichiers **non-veridian_***. Les fichiers
`veridian_*.go` restent sous discipline stricte même dans un sync upstream.

### Dokploy + GHCR

- **Image** : `ghcr.io/christ-roy/notifuse-veridian:<tag>`
- **Compose staging** : `compose-bypass-bluetooth-feed-tbayqr`
- **Compose prod** : secret `DOKPLOY_NOTIFUSE_PROD_COMPOSE_ID`
- **Endpoints** : staging `notifuse.staging.veridian.site`, prod `notifuse.app.veridian.site`

### Tests E2E Veridian

`tests/e2e-veridian/` (Playwright). Tag `@prod-safe` pour les tests
read-only autorisés à tourner sur prod.

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
- **Staging** : compose Dokploy `compose-bypass-bluetooth-feed-tbayqr`
  (Notifuse staging) — ENV injectées via `infra/compose/staging.yml`
- **Prod Notifuse** : compose Dokploy `WN0jglLj5bDIrXUFZHNmw` (cf.
  CLAUDE.md racine `veridian-platform/`, section ComposeIds)
- **Prod Hub** : compose Dokploy `_kxAHDCv1LhvsdwNRX3Vk`
- **Inspection live** : `POST /api/compose.one body {composeId}` via
  Dokploy API (header `x-api-key: $DOKPLOY_API_KEY`) pour lire les ENV
  d'une stack sans SSH

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
9. **Promotion prod gatée par marker `[risk:low]` (§20, câblé 2026-06-10)**.
   Un push sur `veridian` qui passe build + e2e-staging part en prod
   **automatiquement UNIQUEMENT si le subject du commit contient `[risk:low]`**
   (1ère ligne — un marker dans le body ne suffit pas, le job `deploy-prod`
   échoue dans ce cas). Sans `[risk:low]` : staging seul + job
   `notify-promotion-needed` alerte Robert (Telegram) qu'une reco agent /
   promotion explicite est attendue. Promotion manuelle d'un commit non-low =
   `workflow_dispatch` avec `deploy_prod=true` (override, ignore le marker).
   Stop-gap `[skip-prod]` / `[wip]` : bloquent toujours la prod.
   ⚠️ **Historique** : avant le 2026-06-10 la prod était auto-promote
   **inconditionnelle** (opt-out `[skip-prod]`) — un commit tier 🔴 (throttle
   `7b813504`) est parti en prod sans gate. Modèle inversé en opt-in `[risk:low]`
   pour aligner sur Hub CI-ARCHITECTURE §20. Le marker n'est PAS vérifié par
   le pre-push hook côté Notifuse (pas de `check-risk-marker.sh` ici, contrairement
   au Hub) : l'agent est responsable de ne mettre `[risk:low]` que sur du tier
   🟢 (doc, todo, tests-only, refactor sans surface API). Tier 🟡/🔴 → push sans
   marker, puis reco/dispatch.
   Toujours valable : si `Dockerfile`, `go.mod`, `docker-compose.yml`,
   `internal/migrations/**` modifiés → staging vert + e2e-staging vert exigés
   avant toute prod (implicite via `needs`).
10. **Deploy via Dokploy API** : `POST /api/compose.redeploy` (Bearer).
    Secrets : `DOKPLOY_URL`, `DOKPLOY_NOTIFUSE_PROD_COMPOSE_ID`. Rotation 6 mois.
11. **Rollback prod auto** sur e2e-prod fail : `:rollback` retagé avant chaque
    `:latest`. Fail → retag broken-<sha> + restore `:rollback` → redeploy →
    Telegram alert.
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
BASE_REF=origin/main scripts/ci/check-test-mapping.sh  # comme pre-push
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
