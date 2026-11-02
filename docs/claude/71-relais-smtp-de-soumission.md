# 71. Relais SMTP de soumission (design phase 1)

Date : 02/11/2026. Base : `origin/veridian` au commit `4258a3b0`. Demande de Robert (02/11) : « faire de Notifuse un relais SMTP pour tous les workspaces si les gens veulent s'y brancher ». Objet : une application cliente (Supabase, WordPress, CRM Twenty, script) parle SMTP standard à Notifuse ; Notifuse envoie le mail par le profil transactionnel réservé du workspace, avec désinscription, suppression et réputation.

## Ce qui existe déjà (mesuré)

- `pkg/smtp_bridge/` (serveur go-smtp v0.21.0, AUTH PLAIN, TLS `off|starttls|implicit`, 10 Mo, 50 RCPT) et `internal/service/smtp_bridge_handler.go`. Le bridge ne relaie RIEN aujourd'hui : `HandleMessage` exige un corps JSON `{workspace_id, notification}` et appelle `SendNotification` (modèle de template requis). Une appli standard ne peut donc pas s'y brancher.
- Clé API : `notifuse-admin keys:mint <ws>` produit une clé **scopée à UN workspace** (skill notifuse-cli). Le handler d'auth résout l'utilisateur-clé ; le workspace est donc déductible : `GetUserWorkspaces(userID)` doit renvoyer exactement un workspace.
- Profil transactionnel réservé : `Workspace.VeridianReservedTransactionalProfileID()` (fiche 56). Sans profil réservé, le transactionnel part par `TransactionalSettings`. Le profil réservé n'a PAS de plafond ni de chauffe (lot 4, choix délibéré).
- Un envoi transactionnel accepté produit une ligne `message_history` via `messageHistoryRepo.Create` (fait par l'appelant, pas par `EmailService.SendEmail`). `template_id` est `NOT NULL` sans clé étrangère : un identifiant sentinelle suffit, aucune migration.
- Le corps de mail reste recomposé par `SMTPService.SendEmail` (go-mail) : pixel et suivi ne sont injectés que si `TrackingSettings.EnableTracking` est vrai.

## Décisions (tranchées ici)

| Sujet | Décision | Pourquoi |
|---|---|---|
| **Mode** | Relais si le corps n'est PAS un objet JSON (ni une partie multipart JSON), la même règle que `extractJSONPayload`. Corps JSON : chemin legacy inchangé. | Les deux usages cohabitent sans changer l'existant. |
| **Activation** | `VERIDIAN_SMTP_RELAY_ENABLED` (défaut `false`). Désactivé = message non-JSON refusé comme aujourd'hui. Relais activé avec `SMTP_BRIDGE_TLS=off` : refus au démarrage. | Fusible et retour arrière immédiat. Pas d'AUTH en clair. |
| **Workspace** | Déduit de la clé : `GetUserWorkspaces(userID)` = exactement 1, sinon refus 550 5.7.1. | Une clé multi-workspace ne doit pas choisir au hasard. |
| **Profil d'envoi** | Profil transactionnel réservé du workspace. Pas de réservé : refus 554 5.7.1 (pas de repli silencieux sur le commercial). | Séparation transactionnel / commercial (fiche 56). |
| **From** | L'en-tête From ET l'enveloppe MAIL FROM (si non vide) doivent correspondre à un `Senders[].Email` du profil réservé, casse ignorée. Sinon 550 5.7.1. Pas de notion « vérifié » dans le modèle : la liste des expéditeurs du profil est la seule source. | Pas d'usurpation d'un expéditeur du workspace. |
| **Destinataires** | Refus PAR DESTINATAIRE au stade RCPT TO : un destinataire en suppression (liste `bounced` ou `complained`) reçoit 550 5.7.1 ; les autres RCPT passent. Aucun RCPT valide : DATA échoue en 554 5.5.0. Le client voit l'erreur, aucun envoi partiel silencieux. `unsubscribed` (désinscription d'une liste marketing) NE bloque PAS le transactionnel (reset de mot de passe, facture). | Sémantique SMTP standard ; le transactionnel n'est pas bloqué par le marketing. |
| **Limites** | Taille 10 Mo (bridge, re-contrôlé) → 552 5.3.4. Plus de 50 RCPT → 552 5.5.3. Débit par clé : `ratelimiter` namespace `smtp_relay`, 60 messages/min → 451 4.7.1 (nouvel essai côté client). | Réutilise le limiteur existant. |
| **Envoi** | Synchrone, comme le transactionnel : le 250 n'est renvoyé qu'après acceptation par le profil. Pas de file : une file répondrait 250 avant envoi. Un message = un `SendEmail` par destinataire d'enveloppe. | Le client doit savoir. |
| **Erreur d'envoi** | `SendEmail` en erreur → 451 4.4.1 (le client réessaie). | Pas de perte silencieuse. |
| **Message-ID** | Celui du client (sans chevrons) pour le premier destinataire ; un ID généré pour les suivants. Idempotence : un Message-ID déjà relayé dans les 24 h (mémoire du processus) reçoit 250 sans renvoi. | Un retry client ne double pas le mail. Perdu au redémarrage : limite assumée. |
| **Recomposition** | Le MIME est recomposé à partir de : From, Subject, Reply-To, In-Reply-To, References, List-Unsubscribe (URL http(s)), parties texte et HTML, pièces jointes (base64). **En-têtes libres, Cc et Bcc d'en-tête non relayés en phase 1.** | Historique, webhooks et réputation communs avec le reste de Notifuse. Le « tel quel octet pour octet » est un choix à trancher (voir plus bas). |
| **Suivi** | `TrackingSettings.EnableTracking = false` : ni pixel, ni réécriture de liens. | Pas de pixel sur du transactionnel relayé (règle de la phase 1). |
| **DKIM / SPF** | Délégués au fournisseur du profil réservé (le relais SMTP signe). Notifuse ne signe rien. | Un seul point de signature. |
| **Historique** | `message_history` : `template_id = "relay"`, `template_version = 0`, `channel = email`, `external_id` = Message-ID du client, `veridian_message_type = transactional`. Écriture ratée = log seulement (le mail est parti). | Compteurs commerciaux épargnés (fiche 56). |
| **Journaux** | user_id, workspace_id, Message-ID, nombre de destinataires, issue. Jamais le corps, jamais la clé, jamais les adresses en clair. | Rien de secret dans les logs. |
| **Codes SMTP** | 250 accepté · 550 5.7.1 refus de politique (From, suppression, clé non scopée) · 552 limites · 554 config absente · 451 débit ou envoi en erreur (réessayer). | Un code par cause, aucune issue muette. |

## Risque d'open relay (phase 2, non déclenchée)

Phase 1 : tailnet seul, AUTH obligatoire (TLS), aucun port public. Risques à traiter AVANT toute exposition publique :

1. **Open relay** : un workspace dont le profil accepte n'importe quel expéditeur. Parade : la liste des expéditeurs est la seule autorisée (déjà en phase 1), plus un plafond de débit par workspace.
2. **Clé volée** : une clé scopée envoie au nom d'un workspace. Parade : rotation (`keys:revoke`), plafond journalier par clé (à ajouter), alerte sur pic.
3. **Réputation d'IP partagée** : un abus d'un client pénalise tous les autres via le même profil. Parade : profil transactionnel dédié par client ou plafond par clé avant mise en public.
4. **Pas de plafond journalier du profil réservé** (choix du lot 4) : un client peut saturer le profil. À traiter avant phase 2.

## Ce qui n'est PAS dans la phase 1 (écarts assumés)

- Pas de mise en file ni de reprise sur panne : un échec du profil est rendu au client en 451.
- Pas de plafond journalier propre au relais.
- Cc / Bcc d'en-tête et en-têtes libres non relayés.
- Idempotence limitée au processus (24 h, perdue au redémarrage).
- Refus au RCPT : le hook `RcptChecker` de `pkg/smtp_bridge` n'est posé que lorsque le relais est activé. Il s'applique alors aussi aux RCPT des messages JSON legacy (un contact `bounced` ou `complained` est refusé au RCPT, comme pour un relais standard).

## Arbitrages tranchés (main, validé par Robert le 02/11)

1. Recomposition MIME en phase 1 ; transport brut octet pour octet reste une piste de phase 2.
2. Suppression : seuls `bounced` et `complained` bloquent. `unsubscribed` ne bloque pas le transactionnel.
3. Refus par destinataire au RCPT TO, pas par message entier.

## Vérification

Chaque épreuve négative (mauvais mot de passe, From non autorisé, destinataire supprimé en RCPT, deux RCPT dont un supprimé : le premier refusé, le second accepté, message trop gros, trop de RCPT, relais sans TLS) doit être vue échouer avant d'être crue. Le mode JSON existant doit rester vert. Preuves de bout en bout : fiche à compléter après staging.

## Doc d'usage client (paramètres de connexion)

Obtenir les paramètres : `notifuse-admin --env staging relay:info <workspace>` (n'affiche jamais la clé). La clé API se crée à part (`keys:mint`) et se donne UNE fois au client.

| Paramètre | Valeur |
|---|---|
| Hôte | `relais-smtp.veridian.site` (staging à ce jour ; tailnet seul, ne pas exposer sans la phase 2) |
| Port | `587` |
| Sécurité | STARTTLS obligatoire (AUTH refusé en clair) |
| Utilisateur | l'email de la clé API (`<prefixe>@api.<workspace>`) |
| Mot de passe | la clé API |
| Expéditeur (From) | une adresse du profil transactionnel réservé du workspace |

**Supabase** (Auth > SMTP settings) : Host `relais-smtp.veridian.site`, Port `587`, Username = email de la clé, Password = clé API, Sender email = un expéditeur autorisé, Sender name au choix.

**WordPress** (extension SMTP type WP Mail SMTP / PHPMailer) : SMTP host `relais-smtp.veridian.site`, port `587`, chiffrement STARTTLS, authentification activée, utilisateur = email de la clé, mot de passe = clé API, adresse d'expédition autorisée.

**Test à la main** : `swaks --to <vous>@<domaine> --from <expéditeur autorisé> --server relais-smtp.veridian.site --port 587 -tls --auth PLAIN --auth-user <email> --auth-password '<clé>'` (le bridge n'annonce que AUTH PLAIN : un client qui ne sait que LOGIN ne passera pas).

Codes de retour à connaître : 250 accepté · 550 5.7.1 expéditeur non autorisé, destinataire en suppression, clé sans workspace unique · 552 message trop gros ou trop de destinataires · 554 aucun profil transactionnel réservé · 451 débit dépassé ou profil en erreur (réessayer).
