
### Codes de connexion du Hub (10/11/2026)

Le provisionnement idempotent du Hub (`/api/tenants/provision`, une vingtaine d appels en quelques secondes à chaque ouverture de Notifuse depuis le Hub) génère un code de connexion à chaque appel et consommait le quota `signin` (5 / 5 min) de l utilisateur : sa propre connexion et la vérification de son code étaient refusées (« too many magic-code requests », 29 refus en 5 min pour arnaud.capitaine07). Les codes du Hub ont maintenant leur namespace (`veridian-magic`, 60 / 5 min) : le quota humain n est plus touché. Cause amont à corriger côté Hub : `resolve-autologin` appelle `provisionWorkspace` à chaque résolution.
