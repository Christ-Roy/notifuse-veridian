# UI config cold self-service + câblage API IMAP (Lot 8 cold outreach, 2026-06-15)

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

