# Export opérateur des identifiants SMTP et IMAP (2026-11-02)

## Décision et portée

Robert, 02/11/2026 : l'opérateur doit pouvoir reconstruire les profils d'envoi d'un
workspace (identifiants SMTP et IMAP en clair) derrière VPN chez ses clients. Exception
à la rédaction de `veridianRedactWorkspaceForAPI` (fiche 10) **pour SMTP et IMAP
seulement**. Tout le reste reste masqué, et `workspaces.get` ne change pas.

## Ce qui est en place

- **Route** `POST /api/veridian/admin/email-integration.export`, montée sur `hmac`
  seul. **Jamais `writeRoute`** : le middleware d'idempotence persiste le corps de
  réponse 24 h en base, ce qui écrirait les mots de passe au repos.
- **Body** `{workspace_id, integration_id}` (`integration_id` vide = toutes).
- **Réponse** : `exported` (SMTP et IMAP en clair, construits champ par champ dans
  `internal/service/veridian_integration_export.go`, sans `MarshalJSON` par défaut) et
  `not_exportable` (id, nom, type, kind, raison, **aucun champ**). Liste blanche : seuls
  `email`+SMTP et `imap` sortent.
- **Échec fermé** : un secret chiffré présent mais dont le clair est vide renvoie une
  erreur, jamais un mot de passe vide.
- **En-têtes** : `Cache-Control: no-store`, `Pragma: no-cache`.
- **Rate limit dédié** : 20 appels par heure (fenêtre glissante, en mémoire), 429 au-delà.
- **Audit** : une ligne `veridian_integration_export` par appel, succès et échec
  (`outcome`, `workspace_id`, `integration_id` ou `all`, compteurs, `http_status`,
  IP du pair). Aucun secret, aucun corps. Stdout, donc Loki.
- **CLI** `notifuse-admin integrations:export <ws> [--id I] (--out F | --stdout)
  [--format json|env]`. `--out` écrit en 0600 et refuse d'écraser. Sans `--out` ni
  `--stdout`, refus. Le CLI utilisateur `notifuse` refuse la commande (absente de
  `WORKSPACE_COMMANDS`).
- **Câblage** : `veridian_wiring.go` (une ligne, `SetIntegrationExporter(a.emailService)`).

## Pièges

- Le secret HMAC du staging (`HUB_API_SECRET`, variable Nomad `notifuse-staging`) n'est
  pas celui de `NOTIFUSE_HUB_API_SECRET` dans `~/credentials/.all-creds.env` (celui-ci
  correspond à la prod, mesuré par empreinte). `notifuse-admin --env staging` renvoie
  donc 401 « Invalid signature ». À corriger côté CLI (un secret par environnement).
- `workspaces.get` ne renvoie **pas** en clair les secrets Supabase, Firecrawl et LLM.
  La phrase du SKILL `notifuse-cli` qui disait le contraire était fausse (mesure ci-dessous).
- `encrypted_webhook_secret` (ciphertext, 184 car.) est renvoyé à l'owner sur chaque
  intégration email. Ce n'est pas du clair. Le retirer régénérerait le secret à la mise
  à jour : dette, à traiter avec son chemin de mise à jour.
- Le repo est public, donc les jobs `ubuntu-latest` (deploy-prod, rollback) sont couverts
  par la mention `gitops-ci-allow-hosted`. Hors de ce chantier.

## Preuves

Mesures : voir les sections « Mesure » et « Preuve de fin » ci-dessous (complétées au fil
des jalons).

### Mesure avant changement (staging, build 73ed457d, 02/11/2026)

Workspace de test `expproof1102` : SMTP, IMAP, Firecrawl, LLM anthropic, Supabase, avec
des secrets sentinelles. `workspaces.get` : **aucun des 5 en clair**. Seul champ non vide
de type secret : `encrypted_webhook_secret` (chiffré).

### Mesure prod (02/11/2026, lecture seule)

Workspaces accessibles au compte opérateur (`animalsd`, `greenbeamcraft`,
`robertbrunon`, `sirpe`, `veridian`) : SMTP et IMAP en `has_password` uniquement, aucun
clair. Aucun ne porte d'intégration Supabase, Firecrawl ni LLM, donc ce type-là n'a pas
été mesuré en prod.

## Tests

- `internal/service/veridian_integration_export_test.go` : liste blanche, aucun secret
  hors liste dans le JSON, filtre par intégration, échec fermé, bloc OAuth2, méthode
  `VeridianAdminExportIntegrations`.
- `internal/http/veridian_integration_export_handler_test.go` : statuts, en-têtes
  no-store, HMAC requis (JWT owner et clé API refusés), contrôle positif HMAC, rate
  limit, audit sans secret.
- `internal/http/veridian_handler_test.go` : la route est montée, HMAC seul.
- `~/.claude/skills/notifuse-cli/tests/test_integrations_export.py` : sortie unique,
  0600, refus d'écraser, quoting `--format env`, absence du CLI utilisateur.
