# 61 — Nœud webhook des automations : garde SSRF, secret chiffré, signature HMAC (lot 0)

Livré le 10/10/2026, avant toute location de Notifuse à des clients. Branche `veridian`.

## Ce qui n'allait pas

- `WebhookNodeExecutor` appelait l'URL du locataire avec un `http.Client` nu : 127.0.0.1, métadonnées cloud, RFC1918, CGNAT/Tailscale (la flotte), IPv6 ULA, redirections, tout passait.
- Le secret du nœud vivait EN CLAIR dans le JSON `automations.nodes` et ressortait tel quel de `automations.get/list`. Il voyageait aussi en clair dans `Authorization: Bearer`.
- Mesure en prod (10/10, lecture seule, 97 bases `notifuse_ws_*`, requête SQL sans afficher de secret, contrôle positif sur les nœuds `email`) : **0 nœud webhook**, donc rien à migrer. Le code garde malgré tout une migration paresseuse (voir plus bas).

## Garde SSRF (`internal/service/outbound_guard.go`, `webhook_ssrf_guard.go`)

Réutilise `ssrfSafeDialContext` / `isBlockedWebhookIP` des abonnements webhook et les complète.

- `NewTenantOutboundClient(timeout)` : HTTPS seul (`httpsOnlyTransport`, aussi sur chaque redirection), aucun proxy d'environnement, 3 redirections au plus, délai 10 s pour le nœud (`TenantOutboundTimeout`), poignée de main TLS 5 s.
- Résolution UNE fois par connexion, refus si plage interne, connexion épinglée sur l'IP vérifiée (pas de rebinding). Réponse DNS mixte public + interne : seule l'IP publique sert.
- Chaque redirection ouvre une nouvelle connexion, donc repasse par la garde.
- Plages refusées : loopback, RFC1918, link-local (169.254/16 dont 169.254.169.254, fe80::/10), CGNAT 100.64/10, 0/8, multicast, ULA fc00::/7, et (ajout lot 0) 192.0.0/24, TEST-NET 1/2/3, 198.18/15, 240/4 (broadcast), ::/96, fec0::/10, 100::/64, 2001:db8::/32, plus l'IPv4 cachée dans NAT64 64:ff9b::/96 et 6to4 2002::/16, plus IPv4-mappé.
- `ValidateTenantOutboundURL` à l'enregistrement : https, hôte, pas d'identifiants dans l'URL, IP littérale interne refusée. Pas de DNS à l'enregistrement (périmé à l'envoi) : la connexion tranche.
- Réponse lue sur 10 Ko au plus (`webhookNodeMaxResponseBytes`).

### Autres appels sortants pilotés par un locataire (trouvés au passage)

| Appel | Avant | Maintenant |
|---|---|---|
| Flux de données des diffusions (`broadcast/data_feed.go`, URL + en-têtes choisis par le locataire, réponse injectée dans les modèles) | client nu | `NewDataFeedFetcherWithClient` + `NewTenantOutboundClient(30s)` (câblage `app.go`). `NewDataFeedFetcher` (client nu) ne sert plus qu'aux tests. |
| Firecrawl, `BaseURL` personnalisé de l'intégration | client nu | client gardé par défaut |
| Confirmation SNS (`SubscribeURL` du corps d'un webhook entrant, `http.Get`) | aucune vérification | `confirmSNSSubscription` : https, hôte `*.amazonaws.com(.cn)`, client gardé |

Signalés, NON traités (hors lot) :
- SMTP / IMAP : l'hôte est choisi par le locataire (intégration) et la connexion est un TCP brut. Le relais interne de la flotte est légitimement privé, donc une garde exige une liste blanche. À traiter avant d'ouvrir la création d'intégrations SMTP aux clients.
- Endpoints personnalisés des fournisseurs d'envoi (ex. `CustomEndpointURL` workspace) : à passer au client gardé.
- `task_service` envoie vers `API_ENDPOINT` avec `InsecureSkipVerify` : cible serveur, pas locataire, mais le drapeau mérite un ticket.

## Secret

- Au repos : `config.secret_encrypted` (`pkg/crypto`, même passphrase serveur `SECRET_KEY` que les intégrations). Jamais de `secret` persisté.
- API : ni `secret` ni `secret_encrypted` en sortie (create, update, get, list), seulement `has_secret` (bool). Fait dans `AutomationService` (`domain.RedactWebhookNodeSecretsForAPI`, sur copie de config).
- Entrée : `secret` (nouveau, en clair) ; absent, vide ou égal à `********` = on garde l'ancien ; `clear_secret: true` = on supprime ; tout `secret_encrypted` venu du client est ignoré.
- Pas de passphrase serveur : refus plutôt que stockage en clair.
- Migration : aucun nœud en prod (voir plus haut). Un nœud ancien (secret en clair) est chiffré à son prochain enregistrement, et reste lisible à l'exécution en attendant (`ResolveWebhookNodeSecret`). Pas de migration V## (évite le conflit de numéro avec V62 de la fiche 59).

## Signature (format Standard Webhooks, comme les abonnements)

En-têtes envoyés quand le nœud a un secret : `webhook-id` (`msg_<hex>`), `webhook-timestamp` (secondes Unix), `webhook-signature` = `v1,` + base64(HMAC-SHA256(clé, `id.timestamp.corps`)). La clé est le secret du nœud, octets UTF-8 tels quels (pas de préfixe `whsec_`). L'ancien `Authorization: Bearer` n'est PLUS envoyé (il exposait le secret).

Vérification côté receveur (Python) :

```python
import hmac, hashlib, base64, time
def verify(secret: str, headers, body: bytes, tolerance=300) -> bool:
    mid, ts, sig = headers["webhook-id"], headers["webhook-timestamp"], headers["webhook-signature"]
    if abs(time.time() - int(ts)) > tolerance:
        return False  # rejeu
    mac = hmac.new(secret.encode(), f"{mid}.{ts}.".encode() + body, hashlib.sha256).digest()
    expected = "v1," + base64.b64encode(mac).decode()
    return any(hmac.compare_digest(expected, s) for s in sig.split())
```

Vérifier sur le corps BRUT reçu, avant tout re-parsing JSON. Dédupliquer sur `webhook-id`.

## Console

`WebhookConfigForm` : HTTPS seul, champ « Secret de signature » jamais prérempli, indication « Secret enregistré (masqué) » si `has_secret`, bouton « Supprimer le secret » (`clear_secret`). Test : `WebhookConfigForm.test.tsx`. `tsc` : 325 erreurs, identique à la base.

## Tests (chacun échoue contre l'ancien code)

`internal/service/outbound_guard_test.go` : plages (dont NAT64/6to4), cibles littérales internes (serveur loopback réellement joignable, zéro requête reçue), noms résolvant vers l'interne, contrôle positif (public accepté, connexion épinglée), HTTP refusé, redirection vers interne / métadonnée / tailnet / ULA / http, boucle de redirections, rebinding (une résolution, bascule vers loopback refusée, réponse mixte), taille et délai bornés, signature vérifiable avec un receveur indépendant (mauvais secret, corps modifié, rejeu refusés ; pas de `Authorization`), secret chiffré au repos et absent de toutes les réponses, conservation / remplacement / suppression, URL refusées à l'enregistrement, flux de données, Firecrawl, SNS. `internal/domain/automation_webhook_secret_test.go` : chiffrement et rédaction. Épreuve par mutation faite le 10/10 : client nu dans le nœud, garde IP désactivée, service sans chiffrement ni rédaction font tous échouer les tests.

Seams de test : `ssrfFinalDial` et `outboundTLSConfig` (la garde tourne en vrai, seule la connexion finale est redirigée vers le serveur local), `lookupIPAddrFn`. Les tests de comportement du nœud injectent `server.Client()` (`trustWebhookTestServer`).

## Pièges

- Un client HTTP nu pour une URL choisie par un tiers est une faille : toute nouvelle URL locataire passe par `NewTenantOutboundClient`.
- `httpClient` injecté = garde contournée : réservé aux tests.
- `http://` est désormais refusé (nœud webhook, flux de données, Firecrawl).
