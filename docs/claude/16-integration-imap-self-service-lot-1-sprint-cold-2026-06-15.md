# Intégration IMAP self-service (Lot 1 sprint cold, 2026-06-15)

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

