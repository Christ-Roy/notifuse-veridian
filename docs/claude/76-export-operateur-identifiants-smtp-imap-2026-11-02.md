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

- **Classe de défaut « staging signé avec le secret prod »** (corrigée 02/11/2026). Le CLI
  signait tout avec `NOTIFUSE_HUB_API_SECRET` de `~/credentials` (= secret prod, mesuré par
  empreinte). Le staging répondait 401 « Invalid signature », sans message utile. Correction :
  le secret est choisi PAR ENVIRONNEMENT. `prod` lit `~/credentials` ; `staging` lit
  `HUB_API_SECRET` de la variable Nomad `nomad/jobs/notifuse-staging` à chaque appel
  (rien copié sur disque). Introuvable = « non abouti » code 2, jamais de repli prod.
  Test : `~/.claude/skills/notifuse-cli/tests/test_hub_secret_per_env.py` (3 échecs sur la
  version d'avant, 4 verts après).
- `workspaces.get` ne renvoie **pas** en clair les secrets Supabase, Firecrawl et LLM.
  La phrase du SKILL `notifuse-cli` qui disait le contraire était fausse (mesure ci-dessous).
- **Dette** : `encrypted_webhook_secret` (ciphertext, 184 car.) est renvoyé à l'owner sur chaque
  intégration email par `workspaces.get`. Ce n'est pas du clair. Le retirer régénérerait le
  secret à la mise à jour (`BeforeSave` en recrée un si le clair et le chiffré sont vides) :
  à traiter avec le chemin de mise à jour, pas à la hâte.
- Le repo est public, donc les jobs `ubuntu-latest` (deploy-prod, rollback) sont couverts
  par la mention `gitops-ci-allow-hosted`. Hors de ce chantier.
- **Orphelin prod** : la base `notifuse_ws_expproofprod1103` (workspace de test, vide) reste
  après le wipe. Le `DROP ... WITH (FORCE)` est réservé au staging (règle 5 du CLAUDE.md) :
  à décider par Robert. `jetables.py --purge` (simulation) liste en plus 5 orphelines
  anciennes de prod (clitest*, e2e*), dette préexistante.

## Preuves

Commit `43c5564a` (feature), poussé sur `veridian` puis porté par `f635f495` (un autre agent
y a posé le relais SMTP, désactivé). CI : run 41543165443 vert (staging E2E inclus). Prod :
promotion run 41548660079 (Deploy prod et E2E prod verts). Prod sert `f635f495`.

Mutations (rouge prouvé) : HMAC retiré de la route → `TestVeridianIntegrationExport_RequiresHMAC`
rouge ; type hors liste blanche (Supabase, LLM exportés) → 5 tests service rouges. Vert après.

### Preuve staging (f635f495, workspace jetable `expproof1102`, wipé)

- Export JSON via la fonction CLI : SMTP et IMAP `password` égaux aux secrets connus ; fichier
  0600 ; Firecrawl, LLM, Supabase absents du fichier.
- `--format env` et `--stdout` : secrets présents, sortie unique respectée.
- Sans auth, JWT owner, HMAC faux : 401. HMAC valide (contrôle positif) : 200, `no-store`.
- Échec signé (corps invalide) : 400, audit `invalid`.
- Audit : 5 lignes `veridian_integration_export` (`ok`, `invalid`), zéro secret.
- `workspaces.get` : aucun des 5 secrets en clair.
- Wipe : 200. Base orpheline `notifuse_ws_expproof1102` supprimée (FORCE, staging).

### Preuve prod (f635f495, workspace jetable `expproofprod1103`, wipé, rien envoyé)

- Mêmes contrôles que staging : export SMTP et IMAP égaux, 0600, types non exportables sans
  secret, `--stdout` OK, 401 sans auth et avec JWT owner, 401 HMAC faux, 200 + `no-store`.
- Audit prod : 4 lignes (`ok`, `invalid`), zéro secret.
- `workspaces.get` prod : aucun des 5 secrets en clair.
- Wipe : 200. **Base `notifuse_ws_expproofprod1103` encore présente** (voir Pièges).
- Relais SMTP (flag absent du conteneur prod, `VERIDIAN_SMTP_RELAY_ENABLED` défaut `false`) :
  aucun écouteur sur 587 ni 25 dans le namespace réseau du conteneur (`/proc/net/tcp`). Le JSON
  legacy n'emprunte pas le relais tant que le flag est faux (`veridianInitSMTPRelay` sort tôt).

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
