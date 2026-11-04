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

## État au 03/11/2026 (preuves, corrections, limites restantes)

**Preuves vivantes** (client smtplib, STARTTLS vérifié contre `relais-smtp.veridian.site`, destinataires internes `agences-veridian.fr` seulement) :
- staging (flag ON, commit `0caae84a`) : 7 épreuves vertes, plus 535 sur mauvais mot de passe ; message livré, DKIM `pass` (dkimproxy-verify), un seul `@domaine` dans le Message-ID, `List-Unsubscribe` du client transmis ; `message_history` : 6 lignes.
- prod (`0bbdb5e3`, connexion directe à `100.88.202.29:587`) : 7 épreuves vertes ; positif livré, DKIM `pass`, Message-ID à un seul domaine.
- Workspaces de test wipés (staging et prod), boîte `relais-test@` supprimée sur node02, clés de test retirées du coffre.

**Défauts trouvés pendant la preuve et corrigés :**
- Message-ID doublé (`uuid@domaine@domaine`) : le relais passait l'identifiant complet à l'envoi, qui rajoute le domaine. Corrigé : partie locale seulement (`wireLocalPart`), test ajouté.
- Mauvais mot de passe refusé en 454 (temporaire) : un client réessaie en boucle. Corrigé : 535 5.7.8 permanent ; 454 seulement à la limite de débit (`ErrTemporaryAuth`). Test de bout en bout dans `internal/service/smtp_bridge_handler_test.go`.
- Profil de test d'abord pointé sur l'IP : le certificat est émis pour le nom (`smtp.agences-veridian.fr`). Leçon : un profil SMTP se déclare par le nom du certificat, pas par l'IP.

**Limites restantes (assumées, à traiter avant la phase 2) :**
- Idempotence en mémoire du processus (24 h) : perdue au redémarrage ; un retry après redémarrage peut dupliquer le message.
- Cc et Bcc d'en-tête non relayés, en-têtes libres non relayés (le MIME est recomposé, pas renvoyé tel quel).
- Plus de plafond journalier propre au relais (le profil réservé n'est pas plafonné, lot 4).
- Échec partiel sur plusieurs destinataires : les premiers peuvent partir, le client réessaie tout (doublons possibles).
- Le certificat relais est une copie dans une variable Nomad (tâche `notifuse-staging` et `notifuse`), émise hors application. Échéance `relais-smtp.veridian.site` : 1er février 2027. Renouvellement in-app à construire (voir ci-dessous).

**Certificat in-app (non fait) :** la cible est une émission et un renouvellement DNS-01 Cloudflare dans l'application (bibliothèque ACME, jeton lu en environnement), stockage chiffré par `SECRET_KEY` (`pkg/crypto`) dans les réglages système, renouvellement par le planificateur interne (`internal/service/task_scheduler.go`), rechargement à chaud du `tls.Config` du bridge (`GetCertificate`), alerte avant échéance. Un certificat fourni par fichier ou par variable (`SMTP_BRIDGE_TLS_CERT_BASE64`) prime. Ce chantier ajoute une dépendance ACME au `go.mod` et touche la configuration du bridge (`config/config.go`, `internal/app/app.go` gelé) : à valider avant d'être codé.

**DNS déclaré :** `iac/dns/veridian-site.yaml` (source de vérité du dépôt), contrôle lecture seule `scripts/iac/dns-plan.sh` (0 conforme, 1 dérive, 2 non abouti).

**Compose :** `compose.yaml` porte les variables du relais, désactivé par défaut (`SMTP_BRIDGE_TLS=off`, sinon le démarrage échouerait sans certificat), port 587 publié sur `127.0.0.1` par défaut (`SMTP_BRIDGE_BIND` pour l'élargir, phase 2).

## Design des réglages (cap du 04/11/2026 : pas d'env en source de vérité)

**Principe.** La configuration du relais et du certificat vit dans les réglages système (table des réglages, chiffrée par `SECRET_KEY` pour les secrets). Les variables d'environnement ne sont qu'une surcharge d'amorçage, affichée comme telle (`isOverridden`, comme `root_email` ou `api_endpoint` dans `SystemSettingsDrawer.tsx`). L'environnement d'un job Nomad ne porte plus que ce qui est propre à l'infra (tailnet, ingress, sauvegardes, volumes).

**Clés déjà présentes en amont (réutilisées) :** `smtp_bridge_enabled`, `smtp_bridge_domain`, `smtp_bridge_port`, `smtp_bridge_tls_mode`, `encrypted_smtp_bridge_tls_cert_base64`, `encrypted_smtp_bridge_tls_key_base64` (certificat fixe, qui prime sur le géré).

**Nouvelles clés :**
| Clé | Type | Rôle | Surcharge env |
|---|---|---|---|
| `veridian_smtp_relay_enabled` | bool, défaut faux | active le relais de soumission | `VERIDIAN_SMTP_RELAY_ENABLED` |
| `veridian_relay_cert_managed` | bool, défaut faux | le certificat du bridge est géré par l'app | `VERIDIAN_RELAY_CERT_MANAGED` |
| `veridian_relay_acme_email` | texte | contact du compte ACME | `VERIDIAN_RELAY_ACME_EMAIL` |
| `veridian_relay_acme_directory` | `production` ou `test` | annuaire Let's Encrypt | `VERIDIAN_RELAY_ACME_DIRECTORY` |
| `encrypted_veridian_relay_cf_token` | secret, **écriture seule** | jeton DNS Cloudflare (DNS-01) | `CLOUDFLARE_DNS_API_TOKEN` (premier démarrage compose) |

**Lecture seule, posée par le gestionnaire :** `veridian_relay_cert_not_after`, `veridian_relay_cert_serial`, `veridian_relay_cert_last_renewed`, `veridian_relay_cert_last_error`. L'API ne les accepte jamais en écriture.

**Doctrine de rédaction (fiche 10) :** l'API ne renvoie JAMAIS le jeton : elle renvoie `has_veridian_relay_cf_token: true|false`. Une écriture avec champ vide conserve le secret stocké. Le message d'erreur ne contient jamais le jeton.

**Effet immédiat ou au redémarrage :** `veridian_smtp_relay_enabled` est lu à chaque message (cache court), donc effet immédiat. `smtp_bridge_enabled` et le mode TLS restent lus au démarrage, comme aujourd'hui en amont : changement effectif au redémarrage du job. Le certificat géré est relu à chaque renouvellement et à chaque handshake (`GetCertificate`), donc sans redémarrage.

**API et CLI :** les champs passent par les routes de réglages système existantes (`settings.get` / `settings.update`), donc déjà couverts par `system:settings-get` et `system:settings-update` de la CLI. Le contrôle de couverture doit vérifier que les nouveaux champs sont acceptés par la CLI. Aucune route nouvelle.

**Console :** un switch « Relais SMTP de soumission » et un bloc « Certificat géré (Let's Encrypt, DNS Cloudflare) » dans `SystemSettingsDrawer.tsx`, à côté des réglages du bridge, en fr et en en (`lingui extract`), avec le hint « surchargé par l'environnement » quand la variable est posée.

**Migration prod et staging :** (1) poser les réglages par l'API (`notifuse-admin --env <env> system:settings-update`), (2) vérifier par `system:settings-get` et par une épreuve SMTP, (3) retirer des jobs Nomad les variables relais redondantes. Le certificat fixe actuel, copié dans des variables Nomad, est reversé dans les réglages chiffrés, puis la copie Nomad est retirée.

**Compose :** `compose.yaml` est la référence pour relever Notifuse complet (application, base, port du bridge) avec tout désactivé par défaut. La configuration réelle vit dans les réglages, pas dans le compose.

**Postfix :** hors dépôt. Le dépôt ne lit que des NDR (ce qui est normal pour Notifuse). Seul `scripts/e2e/cold-garde-fous.sh` inspecte un conteneur de relais (outil de test) : à signaler à main, non supprimé ici. Le runbook opérateur est dans `~/nomad-veridian/runbooks/relais-smtp-operateur.md` (hors dépôt).

**Limite connue, à traiter :** la dépendance lego v4 fait remonter des versions indirectes (cloud.google.com/go/auth, gax-go, go-isatty, spf13/cast, objx). Avec ces versions, la compilation du paquet `internal/http` tombe en OOM à 6 Go sur node02 ; avec le `go.mod` d'origine elle passe (rc 0). Mesure à retenir avant toute décision sur la dépendance.

## Dépendance ACME : choix de version et mesures (04/11/2026)

**Version retenue : lego v4.25.2** (MIT, `github.com/go-acme/lego`). Cœur seul importé (`lego`, `certcrypto`, `challenge`, `challenge/dns01`, `certificate`, `registration`), pas de provider officiel : le client DNS-01 Cloudflare est écrit en bibliothèque standard (`cloudflare_dns.go`), ce qui retire `cloudflare-go` du graphe.

**Pourquoi pas une version plus récente :** le bisect sur les versions de lego v4 (contrôle `go mod tidy` contre le `go.mod` d'origine) montre que v4.34.0 et v4.35.2 remontent 9 versions existantes (cloud.google.com/go/auth, gax-go, go-isatty, spf13/cast, objx, otelgrpc, x/exp, google.golang.org/api, genproto). v4.28 à v4.32 remontent spf13/cast seul. v4.25.x ne remonte aucune version existante ; elle ajoute seulement des indirectes nouvelles (cenkalti/backoff, go-jose, miekg/dns, x/mod, x/tools).

**Sécurité : govulncheck.** Sur v4.25.0, `govulncheck ./internal/veridian/relaycert/ ./pkg/smtp_bridge/` signale **GO-2025-3847** (`acme/api` n'impose pas HTTPS, corrigé en v4.25.2), atteint par `lego.NewClient`. Sur v4.25.2 : « No vulnerabilities found » pour le code appelé ; 4 vulnérabilités dans des modules requis, non appelées par notre code (informatif).

**Mémoire de compilation d'`internal/app`** (mesure côté hôte, cgroup du conteneur, sans limite, échantillonnage du `memory.current` du scope docker, 1 s) :
- go.mod d'origine : pic 1305 Mio (build rc 0) ; un second passage a été tué (exit 137, OOMKilled=false, pic échantillonné 679 Mio).
- lego v4.25.2 : pic 1915 Mio (build rc 0). Écart d'environ 600 Mio.
- Le noyau de node02 n'a journalisé aucune tuerie mémoire de nos conteneurs (les kills memcg du journal concernent `netdata` et `python3`, dernières entrées du 2 novembre). Le signal 137 frappe aussi l'arbre d'origine : sa cause n'est pas attribuée à lego, et je ne peux pas l'identifier avec les journaux disponibles. Les anciennes attributions « bruit d'hôte » ne reposaient pas sur une mesure et sont retirées.

**Paquets et tests :** `go vet` et `go test` vert sur `internal/veridian/relaycert` et `pkg/smtp_bridge` ; `internal/http` et `internal/app` compilent (rc 0) avec le go.mod final.
