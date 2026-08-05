# P0 futur - Isolation du cache OAuth entre profils Gmail

> **Sévérité** : 🔴 P0 avant activation OAuth multi-profils

## Problème

La clé du cache token OAuth est aujourd'hui `provider + tenantID + clientID`. Elle exclut volontairement username et refresh token. Deux comptes Gmail du même workspace partageant le même OAuth client peuvent donc recevoir le token du premier compte mis en cache. L'envoi Gmail API `users/me/messages/send` part alors du mauvais compte.

Le support app-password ne déclenche pas ce bug, mais le contrat multi-profils doit rester compatible OAuth : le laisser latent transformerait une migration d'auth en fuite cross-profile.

## Statut à ne pas sur-vendre

Le refresh Google et le transport Gmail API sont des briques de code, pas une preuve qu'OAuth Gmail est opérationnel. Même après correction de ce cache, il restera à prouver le flow de consentement, les scopes, la persistance/rotation/révocation des tokens, l'identité du compte `users/me`, les erreurs Google et un envoi contrôlé depuis deux comptes de test. Ce ticket est donc un prérequis d'isolation, pas une livraison OAuth.

## Correction exigée

- Clé de cache incluant l'identité stable du profil/integration et du compte délégué.
- Si une empreinte du refresh token est nécessaire, HMAC serveur tronqué, jamais token/username brut dans la clé observable ou les logs.
- Invalidation à update/delete/rotation du refresh token et à révocation OAuth.
- Pas de partage entre workspaces, profils, comptes ou providers ; partage contrôlé possible seulement pour le même integration ID et la même génération de credential.
- Le transport Gmail API doit vérifier que le compte/token correspond au profil attendu lorsque Google expose cette information, et produire une erreur sûre sinon.

## Tests obligatoires

- Deux profils Gmail, même tenant et client ID, refresh tokens différents : deux refreshs et deux tokens distincts.
- Appels concurrents : singleflight seulement à l'intérieur d'un profil, aucune contamination croisée.
- Rotation du refresh token : ancien cache invalidé.
- Suppression profil : cache purgé.
- Logs/metrics : integration ID autorisé, aucun refresh/access token, client secret ou adresse personnelle si non nécessaire.

## Definition of Done

Une batterie avec deux faux serveurs OAuth/Gmail prouve que chaque message utilise toujours le token et le compte du profil figé dans la queue, y compris sous concurrence et après rotation de credential.
