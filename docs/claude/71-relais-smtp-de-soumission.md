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

## Réglages système : état d'implémentation (04/11/2026)

**Lecture (paquet `internal/veridian/relaysettings`, source unique des clés).**
- Interrupteur `veridian_smtp_relay_enabled` : lu à CHAQUE message (`ReadEnabled`) et à chaque RCPT. Une variable `VERIDIAN_SMTP_RELAY_ENABLED` non vide prime ; sinon le réglage. Effet immédiat.
- Certificat géré : `veridian_relay_cert_managed` (variable `VERIDIAN_RELAY_CERT_MANAGED` prioritaire), lu au démarrage pour la configuration TLS du bridge (`config.RelayCertManaged`), puis à chaque décision du gestionnaire. Contact ACME, annuaire (`production`|`test`, aucune URL libre) et jeton chiffré (`encrypted_veridian_relay_cf_token`, déchiffré avec `SECRET_KEY`) sont relus à chaque décision. Quand le réglage est vide, la variable (`VERIDIAN_RELAY_ACME_EMAIL`, `VERIDIAN_RELAY_ACME_DIRECTORY`, `CLOUDFLARE_DNS_API_TOKEN`) sert de repli d'amorçage uniquement.
- Précédence : certificat fourni (`SMTP_BRIDGE_TLS_CERT_BASE64`/`KEY_BASE64`) > certificat géré (`pkg/smtp_bridge.SetupTLS`).

**Décisions du gestionnaire (`relaycert`).** Première décision 1 min après le démarrage (pas d'émission en rafale au démarrage). Renouvellement à moins de 30 j, ou si l'annuaire courant diffère de celui de l'émission (un certificat de test n'est jamais servi comme certificat de production ; le compte ACME n'est pas réutilisé d'un annuaire à l'autre). Backoff exponentiel plafonné à 6 h après échec. Sans contact, sans jeton ou désactivé : aucun appel réseau.

**État publié (lecture seule côté API).** `veridian_relay_cert_not_after`, `_serial`, `_last_renewed`, `_last_error` sont écrits après chaque décision et exposés par `settings.get`. `last_error` est tronqué à 300 caractères et ne contient ni le jeton ni le chiffré.

**Console et CLI.** `SystemSettingsDrawer` : section « Relais SMTP de soumission », section « Certificat géré » (contact, annuaire, jeton en écriture seule, état). Le formulaire envoie les valeurs courantes ; le jeton n'est envoyé que s'il est saisi. CLI : `system:settings-get --workspace WS [--key K]` et `system:settings-update --workspace WS [--relay true|false] [--cert-managed true|false] [--acme-email E] [--acme-directory production|test] (--cf-token-stdin | --cf-token-file F) [--data JSON]`. Le jeton n'entre jamais en argument ; `--data` le refuse. La mise à jour part des valeurs courantes (GET puis POST intégral), sinon les réglages non passés seraient vidés. Le serveur redémarre après chaque enregistrement.

**Staging (ce push).** Le job `deploy/notifuse-staging.nomad.hcl` ne copie plus le certificat fixe. Il pose `SMTP_BRIDGE_TLS=starttls` et `VERIDIAN_RELAY_CERT_MANAGED=true` (amorçage : le bridge démarre sans certificat, aucun AUTH annoncé sans TLS), et ne pose plus `VERIDIAN_SMTP_RELAY_ENABLED` (le réglage s'applique). La variable Nomad `nomad/jobs/notifuse-staging/stack/notifuse-staging` reste en place comme retour arrière, retirée par l'opérateur à la fin.

**Retour arrière (staging comme prod).** Remettre dans le job le bloc `nomadVar "…/stack/…"` qui pose `SMTP_BRIDGE_TLS=starttls` et les deux variables de certificat, et `VERIDIAN_SMTP_RELAY_ENABLED=true` ; le réglage `veridian_relay_cert_managed` à `false` coupe le gestionnaire. Le relais revient au certificat copié.

**Compose.** `compose.yaml` ne pose jamais `VERIDIAN_SMTP_RELAY_ENABLED=false` par défaut : une valeur par défaut prime sur le réglage à chaque démarrage. Les variables relais y sont des passe-plat vides (vide = non posée).

**Pas encore vérifié à cette date** (à compléter dans la section trace après la bascule) : émission réelle sur l'annuaire de test, rechargement à chaud sur le port 587, alerte d'échec vue, épreuve SMTP positive et négatives, bascule prod.

## Dépendances : alertes trivy du 04/11/2026 (analyse)

- **go-jose v4.1.4 → v4.1.6** (CVE-2026-116587, DoS par paramètres RSA forgés dans un JWK, corrigé en 4.1.6) : bump direct, pas d'appel contrôlé par l'entrée externe dans notre code, mise à jour faite.
- **lego v4.25.2** (CVE-2026-40611 = GO-2026-5593, écriture de fichier arbitraire par traversée de chemin dans le **provider webroot HTTP-01**, corrigé en v4.34.0). Notre émetteur n'utilise que le client ACME et le défi DNS-01 avec notre propre client Cloudflare (`internal/veridian/relaycert`) ; aucun provider webroot, aucun serveur HTTP-01 ou TLS-ALPN. `govulncheck` (base Go, v1.1.4) sur `internal/veridian/relaycert`, `internal/veridian/relaysettings` et `internal/app` : **0 symbole appelé**.
- **Décision : lego passe à v4.34.0** (pic mémoire mesuré à froid, cache de build vide, `memory.peak` du conteneur : 1,74 GiB sur `internal/app` et `internal/http`, rc 0 ; à chaud 525 MiB). Le seuil de 3 GiB est respecté. Le bump remonte des modules indirects (`go mod tidy`). Vérifié : vet et tests `relaycert`, `relaysettings`, `config`, `internal/http`, `internal/service`, vet `internal/app`, `go mod verify`.
- **Gitleaks** : la ligne `cfg.Certificate.KeyType = certcrypto.EC256` déclenchait generic-api-key (faux positif). Reformulée via `ecdsaP256Type()`. Mesure (gitleaks 8.24.3, diff de commits, `.gitleaks.toml` du dépôt) : ancienne ligne 1 finding, nouvelle 0, contrôle positif (faux secret ajouté volontairement) 2 findings.
- **Trivy** : gate bloquant (`--scanners vuln`, `CRITICAL,HIGH`, `.trivyignore.yaml`) : 0 finding sur l'arbre candidat, mesuré localement (trivy 0.70.0, CI épinglée sur trivy-action v0.36.0). Le seul résultat du scan complet (secret, non bloquant) est la clé de test `console/certificates/key.pem`, fixture upstream déjà exclue par `.gitleaks.toml`.
- **Gitleaks et historique** : gitleaks scanne chaque commit de la plage poussée. La ligne fautive reste dans l'historique du commit 5a62bf40 : la correction du code ne suffit pas à passer la CI sans réécrire l'historique (interdit sans force push). Une seule empreinte est donc exemptée dans `.gitleaksignore` : `5a62bf40…:internal/veridian/relaycert/issuer_lego.go:generic-api-key:56`. Faux positif : constante de type de clé lego EC256, aucun secret. La date 2027-02-01 est un **rappel de revue**, pas une expiration (gitleaksignore n'expire pas seul). Le fichier `.gitleaksignore` lui-même est exempté par chemin dans `.gitleaks.toml` (il contient des empreintes de commits). Toute nouvelle occurrence reste détectée (contrôle positif mesuré : 1 finding).

## Preuve staging du certificat géré (04/11/2026)

- **Annuaire test Let's Encrypt injoignable le 04/11** depuis nos trois egress (bastion, node02, staging : reset TCP en IPv4 et IPv6). Preuve faite sur l'annuaire **production**, autorisée par main, **une seule émission** :
  - Échec d'abord avec un jeton invalide : rejet Cloudflare au lookup de zone (HTTP 400), donc avant tout dépôt de TXT et toute validation Let's Encrypt. Dernière erreur publiée dans les réglages.
  - Vrai jeton (stdin) : émission réelle, série `06E0C343E011DC29A836C32937B27D8A0300`, échéance 2027-02-02T01:39:10Z, renouvellement 2026-11-04T02:37:29Z. Émetteur « Let's Encrypt YE1 ». `openssl s_client -starttls smtp -servername relais-smtp.veridian.site` : `Verify return code: 0 (ok)`. Le certificat a été servi à chaud, sans redémarrage après l'émission.
- **Quota Let's Encrypt « duplicate certificate » pour relais-smtp.veridian.site** : 1 émission staging le 04/11 (pour la limite de 5 par semaine), plus la prod à venir.
- **SMTP** : STARTTLS vérifié (TLS 1.3, chaîne et nom contrôlés), AUTH annoncé seulement après TLS, sans TLS aucun login accepté (refus côté client), mauvais identifiants refusés en `535 5.7.8`. **Épreuve positive (envoi réel) non faite** : elle exige un workspace jetable à profil transactionnel réservé et une clé.
- **Renouvellement à 30 jours** : non rejoué en réel (échéance 2027). Couvert par les tests unitaires de décision (`internal/veridian/relaycert`) et par l'état publié (`last_error`, `not_after`) ; l'alerte de renouvellement est le journal ERROR et la ligne de réglage.
- **Relais au démarrage** : après l'activation `veridian_smtp_relay_enabled=true`, le port 587 a mis environ une minute à répondre (démarrage du bridge), sans cause déterminée à cette heure.

## Plan PROD (préparé le 04/11/2026, RIEN exécuté en prod)

Aucune action prod avant le feu vert explicite de main, par temps. Chaque temps a son retour arrière.

**Prérequis code (vérifié dans le code, à confirmer par test avant T2).** La précédence « certificat fixe > géré » ne doit gouverner que ce qui est SERVI, jamais l'ÉMISSION : le gestionnaire doit émettre et stocker même si un certificat fixe est présent. Le gestionnaire est lancé dès que le bridge est actif ; la source à chaud n'est branchée que sans fixe. Test à ajouter avant T2 si le câblage ne le prouve pas.

**Relevé avant T2 (valeurs non secrètes, à coller ici au moment venu).** `notifuse-admin --env prod system:settings-get` : relais, certificat géré, annuaire, état, `has_veridian_relay_cf_token`.

**T1 — promouvoir le code sans changer le job.** Un seul `workflow_dispatch` (deploy_prod=true, staging=false) sur le SHA final, après feu vert. Job prod INCHANGÉ (certificat copié présent, relais par variable d'environnement) : comportement identique, c'est un déploiement de code. Vérifier `/api/version` prod, et que le relais prod répond encore : sonde directe sur 100.88.202.29:587 (STARTTLS vérifié, AUTH annoncé après TLS) avec attente healthz 200 puis réessais TCP jusqu'à 60 s. Rollback : redéploiement du SHA précédent.

**T2 — réglages prod par root-signin prod.** Garde-fou CLI : refus sauf variable `NOTIFUSE_PROD_ROOT=1` posée explicitement par l'appelant ; `get` avant chaque `update` ; secrets lus à l'appel dans `nomad/jobs/notifuse` (items NOTIFUSE_ROOT_EMAIL, NOTIFUSE_SECRET_KEY), en mémoire seule. Valeurs : contact ACME, annuaire production, jeton Cloudflare par stdin (jamais en argument), `veridian_relay_cert_managed=true`, `veridian_smtp_relay_enabled=true`. Le serveur prod redémarre (environ 25 à 60 s d'indisponibilité de Notifuse prod) : prévenir main avant. Le job garde son certificat copié : le géré est ÉMIS et STOCKÉ mais pas servi (précédence). Émission prod : 2e émission sur ce nom (compteur 2/5), une seule. Rollback T2 : `--relay false --cert-managed false` par la même CLI.

**T3 — job prod sans certificat copié.** Job sans certificat copié ni bloc nomadVar cert ; `VERIDIAN_RELAY_CERT_MANAGED=true` en amorçage ; `SMTP_BRIDGE_TLS=starttls`. Push avec `[risk:low]` en PREMIÈRE ligne SEULEMENT après feu vert main : la promotion auto déploie la prod. Vérifications : STARTTLS sur 100.88.202.29:587, série servie = série publiée dans les réglages ; SMTP positif et négatifs sur un workspace prod jetable (wipé ensuite) ; pas d'envoi externe. Puis main supprime les variables Nomad du certificat et commite le runbook. Rollback T3 : remettre le bloc nomadVar cert (une ligne `{{ with nomadVar "nomad/jobs/notifuse/stack/notifuse" }}…`) : le job reprend le certificat copié.

**Garde-fous généraux.** Aucune boucle d'émission : sur échec Let's Encrypt, arrêt et compte-rendu, pas de retry. Aucun secret affiché, aucune valeur dans un fichier hors scratch 0600 shreddé. Pas de push sans accord.

## Trace prod (04/11/2026) : T1 déployé, T2 appliqué

- **T1** : run 41762995856 (fca41c35), `Deploy prod` et `E2E prod` verts ; job prod inchangé (cert copié, relais par variable). Référence avant : `/api/version` = 0bbdb5e3 ; 587 servait le cert copié, série 06FA12158E82065DDBC5DF18301452960088, notAfter 2027-02-01. Après : `/api/version` = fca41c35, 587 identique (Verify 0).
- **T2** : réglages prod avant → après. Relais 0 → 1 (sans effet : `VERIDIAN_SMTP_RELAY_ENABLED=true` en env prod). Certificat géré 0 → 1. Contact ACME vide → robert@veridian.site. Annuaire vide (production) → production. Jeton DNS absent → présent (écriture seule). Série vide → 699D55E04B42C3E88F5DB9E8728DC160CBE ; échéance 2027-02-02T02:23:27Z ; renouvellement 2026-11-04T03:21:46Z ; dernière erreur vide.
- **Émission prod** : 1, la 2e sur ce nom (compteur Let's Encrypt 2/5). Aucune retentative.
- **Cert servi après T2** : toujours le fixe 06FA12158E82065DDBC5DF18301452960088 (précédence respectée), Verify 0.
- **Rollback T2** : `NOTIFUSE_PROD_ROOT=1 notifuse-admin --env prod system:settings-update --cert-managed false --relay false`. Le certificat géré reste stocké, non servi.

## T3 (04/11/2026) : job prod sans certificat copié

- **Diff du job prod** (`deploy/notifuse.nomad.hcl`, bloc relais) : retrait du bloc `nomadVar "nomad/jobs/notifuse/stack/notifuse"` et des variables `SMTP_BRIDGE_TLS_CERT_BASE64` / `SMTP_BRIDGE_TLS_KEY_BASE64`. Le bridge est désormais toujours démarré (`SMTP_BRIDGE_ENABLED=true`, `SMTP_BRIDGE_TLS=starttls`). Amorçage ajouté : `VERIDIAN_RELAY_CERT_MANAGED=true`. `VERIDIAN_SMTP_RELAY_ENABLED=true` est conservé (demande main : rien d'autre) ; à retirer plus tard pour que le réglage gouverne seul.
- **Rechargement depuis le stockage au démarrage, sans nouvelle émission** : `Manager.Start` appelle `Load` de façon synchrone avant toute décision : le bundle chiffré est relu, vérifié (paire, nom), posé dans le holder. La première décision (une minute plus tard) ne réémet que si le certificat est absent, à moins de 30 jours de l'échéance, ou émis sous un autre annuaire. Tests : `TestManager_Load` (le holder reçoit le certificat du stockage), `TestManager_Tick_NoRenewalWhenFresh` (aucune émission), `TestSettingsStore_RoundTripEncrypted`. Preuve vivante staging : après `--relay true`, le certificat 06E0… a été servi sans seconde émission (quota resté à 1/5).
- **Précédence** : `TestManager_Tick_IssuesAndStoresWhenHolderAlreadyServesFixedCertificate` (mutant bloquant : rouge) établit que l'émission et le stockage ne dépendent pas du certificat fixe servi.
- **Rollback T3** : remettre le bloc `nomadVar` du certificat copié (une ligne) et retirer `SMTP_BRIDGE_ENABLED=true` / `VERIDIAN_RELAY_CERT_MANAGED=true` ; ou `nomad job revert` à la version précédente ; ou `git revert` du commit job et push `[risk:low]`.

## Preuve prod T3 (04/11/2026)

- **Run** 41767123291 (push 3a920f9e, `[risk:low]` en tête) : Deploy prod et E2E prod verts. Alloc prod 4b94f787 depuis 03:56:50Z, `/api/version` = 3a920f9e, démarrage bridge starttls 0.0.0.0:587, « SMTP relay prêt », « relaycert : gestionnaire démarré », aucune ligne « émis » au démarrage (certificat relu depuis le stockage), zéro ERROR relaycert.
- **587 direct** 100.88.202.29 : certificat géré servi, série 0699D55E04B42C3E88F5DB9E8728DC160CBE, notAfter 2027-02-02, `Verify return code: 0`. Aucun AUTH annoncé avant TLS, STARTTLS vérifié (TLS 1.3), mauvais identifiants refusés `535 5.7.8`.
- **Épreuve positive prod** : workspace jetable `relaisprod1107`, profil SMTP vers la boîte interne `relais-test@agences-veridian.fr` sur le relais node02 (runbook), profil transactionnel. Relais : DATA accepté (250). Message reçu dans la boîte interne, `dkimproxy-verify` : `verify result: pass`. Historique : ligne `relay` / `email`, envoi 2026-11-04T03:58:39Z.
- **Négatif prod** : expéditeur non autorisé → `550 5.7.1`.
- **Nettoyage** : wipe de `relaisprod1107` (rc 0), boîte de test supprimée et répertoire de messages absent, fichiers de clé et de mot de passe shreddés.
- **Émission Let's Encrypt prod** : 2 au total sur ce nom (staging 04/11, prod T2 04/11), compteur 2/5, aucune retentative.
- **Reste** : retrait des variables Nomad du certificat copié (main) ; `VERIDIAN_SMTP_RELAY_ENABLED=true` reste en variable d'environnement du job prod (le réglage ne le gouverne pas tant qu'il est là : à retirer quand le relais est piloté par le réglage seul).

## Fin de chantier (04/11/2026) : réglage seul, CLI admin seule

- **Variable d'environnement du relais retirée** des jobs : `VERIDIAN_SMTP_RELAY_ENABLED=true` a été retiré du job prod (le job staging ne la portait plus depuis 11cdc736). Avant le retrait, `veridian_smtp_relay_enabled` valait `True` sur prod et sur staging (lu par `system:settings-get`). Désormais, le réglage gouverne seul. Restent en amorçage (infra) : `SMTP_BRIDGE_ENABLED=true`, `SMTP_BRIDGE_TLS=starttls`, `SMTP_BRIDGE_PORT`, `SMTP_BRIDGE_DOMAIN`, `VERIDIAN_RELAY_CERT_MANAGED=true`.
- **Variables Nomad du certificat copié purgées** (main, 04/11) : les jobs notifuse et notifuse-staging ne référencent plus ces chemins.
- **Commandes de réglage du relais absentes de la CLI distribuée** : `system:settings-get` et `system:settings-update` ne sont plus dans la copie embarquée (`internal/http/agentcli`) : elles ne vivent que dans `notifuse-admin` (module `notifuse_root.py`, non distribué). La couverture des routes `/api/settings.*` est donc tenue par deux exemptions motivées dans `scripts/veridian/cli-route-exemptions.txt`. Le root-signin ne signe qu'en staging et en prod sous garde explicite (`NOTIFUSE_PROD_ROOT=1`), secrets lus à l'appel dans Nomad, en mémoire seule.

## AUTH : une clé API révoquée est refusée dès l'AUTH (08/11/2026)
Le JWT d'une clé API reste valide après `removeMember` (signature et expiration). `SMTPBridgeHandlerService.Authenticate`
vérifie désormais que la clé a encore au moins un workspace : sinon 535 5.7.8 (permanent) dès l'AUTH, au lieu du 550 5.7.1
à l'envoi. Une panne de lecture des workspaces est un refus temporaire (454 4.7.0), jamais une révocation.
Tests : `TestSMTPBridgeHandlerService_Authenticate_*` (dont `RevokedKeyRefusedAt535`, à travers le bridge réel).
