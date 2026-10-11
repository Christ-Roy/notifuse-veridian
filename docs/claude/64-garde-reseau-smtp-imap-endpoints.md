# 64 — Garde réseau partagée : SMTP, IMAP, endpoints des fournisseurs, TLS des tâches (suite de la fiche 61)

Livré le 11/10/2026. Branche `veridian`. Ferme les trois restes signalés par la fiche 61.

## Ce qui n'allait pas

- **SMTP** : `sendRawEmailWithSettings` faisait un `net.Dialer.Dial("tcp", host:port)` sur un hôte saisi par le locataire. Un client pouvait viser `127.0.0.1:25`, un port interne, `169.254.169.254`, un nœud Tailscale.
- **IMAP** : même chose (`imapclient.DialTLS/DialStartTLS` avec un `net.Dialer` nu).
- **Endpoints personnalisés des fournisseurs** : seul `SparkPostSettings.Endpoint` est une URL choisie par le locataire ET appelée par le serveur (envoi + gestion des webhooks, client HTTP nu). `CustomEndpointURL` du workspace n'est PAS appelé : il sert de base aux liens de suivi (le serveur ne fait qu'un `LookupCNAME` sur son hôte), donc pas de SSRF. Postmark, SendGrid, Mailjet, Mailgun, SES ont des hôtes codés en dur.
- **`InsecureSkipVerify` de `task_service`** : posé le 17/05/2025 par l'amont (Pierre Bazoge). Mesuré le 11/10 : la prod appelle `INTERNAL_API_ENDPOINT=http://localhost:8081` (HTTP clair, TLS jamais négocié) et, à défaut, `API_ENDPOINT=https://notifuse.app.veridian.site` (certificat valide, vérifié avec `curl`). Le drapeau ne servait aucun appel légitime : retiré, sans épinglage.

## La garde (une seule, `pkg/netguard`)

- `IsBlockedIP` : LA définition de « adresse interdite » (loopback, RFC1918, link-local dont 169.254.169.254, CGNAT/Tailscale, ULA, multicast, plages réservées, IPv4 cachée dans IPv4-mappé / NAT64 / 6to4). `isBlockedWebhookIP` de la fiche 61 la délègue : une liste, pas une copie par protocole.
- `Control` / `GuardDialer` : posé sur `net.Dialer.Control`, appelé par le système pour chaque adresse réellement tentée APRÈS résolution : pas de rebinding possible, une réponse DNS mixte public + interne ne joint que le public. Utilisé par SMTP et IMAP.
- `Allowlist` : exceptions EXPLICITES, une IP et un port exacts, jamais de plage. Défaut : les 3 relais Veridian en IMAP 993 (nord `100.88.202.29`, agences dev `100.92.215.42`, node02 `100.85.160.17`), ceux des boîtes de rejet branchées le 07/10. Ajout sans recompiler : `NOTIFUSE_OUTBOUND_INTERNAL_ALLOW="ip:port,ip:port"` (entrée invalide = tout est ignoré). Le port 0 n'existe que dans le code (tests).
- `ValidateHost` : à l'enregistrement (`SMTPSettings.Validate`, `IMAPSettings.Validate`, `SparkPostSettings.Validate`), une IP littérale interdite est refusée avec sa raison (`host 127.0.0.1 targets a disallowed address: ...`). Un nom n'est pas résolu là (périmé à l'envoi) : la connexion tranche, message `refusing to connect to <ip:port>: internal/private/link-local/metadata addresses are not permitted (not in the relay allowlist)`.
- SparkPost : `NewGuardedSparkPostService` (client `NewTenantOutboundClient`, HTTPS seul, garde IP, redirections revalidées) pour `EmailService` et `app.go`. `Validate` exige https, un hôte, pas d'identifiants, pas d'IP interne.

## Piège : l'exception ne couvre pas SMTP

Les relais en Tailscale ne sont autorisés qu'en IMAP 993. Un profil SMTP vers un relais en `100.x` (cas du `skip_tls_verify` sur réseau privé) demande `NOTIFUSE_OUTBOUND_INTERNAL_ALLOW=100.92.215.42:587` dans le job Nomad. Aucun profil actuel de `robertbrunon` n'en a besoin (leurs hôtes SMTP sont publics, mesuré le 11/10).

## Hors périmètre, à traiter

- `llm_provider_openai.BaseURL` (client OpenAI, URL choisie par le locataire, `http://localhost:11434` Ollama prévu par les tests) : même classe, non traité ici.
- `http://` reste accepté par `validateWebhookURL` des abonnements webhook (fiche 61 l'a fermé pour le nœud, pas pour les abonnements).

## Tests (chacun échoue contre l'ancien code)

`pkg/netguard/netguard_test.go` (plages, exception exacte, `Control` sur un vrai dialer avec un nom qui résout en loopback, zéro connexion reçue), `internal/service/smtp_netguard_test.go`, `internal/service/queue/veridian_imap_netguard_test.go`, `internal/service/outbound_guard_test.go::TestSparkPost_CustomEndpointCannotReachInternal` (service direct ET celui de l'EmailService), `internal/service/task_service_tls_test.go`, `Validate_Refuses*` dans `internal/domain`. Chaque suite a son contrôle positif (l'exception exacte joint le faux serveur, un autre port non). Les tests historiques SMTP qui parlent à `127.0.0.1` autorisent explicitement leur faux serveur via le seam `smtpDialAllowlist` (`netguard_testsetup_test.go`).

Épreuve par mutation (11/10) : client SMTP nu, dialer IMAP nu, `ValidateHost` neutralisé, exception qui ignore le port, `InsecureSkipVerify` remis, client SparkPost nu, `IsLoopback` retiré, CGNAT retiré : chacun fait échouer au moins un test.

## Hygiène des workspaces jetables (même jour)

Voir `acquisition-emailing/monitoring/jetables.py` (contrôle quotidien dans `acquisition status`, purge réverifiée) et `tests/e2e-veridian/prod-residue.ts` (le run `e2e-prod` compare les tenants avant/après, wipe un tenant au motif de test, signale tout le reste, ne casse jamais le run).
