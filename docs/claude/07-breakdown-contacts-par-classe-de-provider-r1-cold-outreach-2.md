# Breakdown contacts par classe de provider (R1 cold outreach, 2026-06-14)

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

