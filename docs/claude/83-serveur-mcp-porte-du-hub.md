# 83 — Serveur MCP de Notifuse derrière la porte du Hub

Contrat : `veridian-hub/docs/CONTRAT-MCP.md` (09/11/2026). Code : `internal/veridian/mcp/`, câblé par
`internal/app/veridian_mod_mcp.go`. Route : `POST /api/mcp` (JSON-RPC 2.0, Streamable HTTP en réponse JSON).

## Ce que fait l'app
1. **Authentifie** le JWT du Hub : ES256, clé `kid` du JWKS `https://app.veridian.site/.well-known/jwks.json`
   (cache 1 h, un `kid` inconnu ne recharge pas plus d'une fois par 30 s), `iss`, `aud` = URL MCP inscrite au registre,
   `exp` (refus si > 10 min), `email_verified`, `sub` obligatoire. HS256 et autres algos refusés.
2. **Retrouve l'utilisateur par `hub_user_id` (= `sub`)** : inconnu ou supprimé au Hub = 401, jamais de création.
3. **Déduit l'espace de l'utilisateur** (propriétaire d'abord, le plus ancien ensuite, jamais un espace supprimé). Un
   `workspace_id` fourni par l'IA est ignoré : il ne figure dans aucun schéma d'outil.
4. **`tools/list` filtré** par les droits de l'utilisateur (même calcul que l'upstream : `UserWorkspace.HasPermission`),
   `readOnlyHint` / `destructiveHint` renseignés.
5. **`tools/call`** rejoue la requête sur le routeur de l'app avec une session de 2 min supprimée aussitôt : les droits,
   validations et gardes sont ceux de l'API, pas une copie. Réponse tronquée à 30 000 caractères, listes bornées (25 par
   défaut, 100 max, `limit`/`offset`/`cursor`). L'app journalise l'issue (outil, client, durée), jamais les arguments.

## Règles
- **Tout envoi de mail ou suppression est `destructiveHint`** : un test balaie le catalogue (`transactional_send`,
  `contacts_delete`). Aucun envoi pendant les tests.
- Ajouter un outil = une ligne dans `catalog` (`tools.go`) + son test ; le Hub relit `tools/list` (cache 5 min), aucun
  déploiement du Hub.
- Un `sub` inconnu : 401 dont le corps contient `hub_user_unknown` (le Hub crée alors le compte et réessaie une fois). Claim facultatif `tenant_id` (id de tenant du Hub, vérifié côté Hub) : il choisit l espace, par slug ou par `veridian_plan.hub_tenant_id` (V73, appris par `update-plan` avec le champ `hub_tenant_id`) ; appartenance revérifiée ici (403 si non membre, 404 avec message clair si aucun espace n'est relié à cet id).
- Config (env, défauts publics, aucun secret) : `VERIDIAN_HUB_JWKS_URL`, `VERIDIAN_HUB_ISSUER`, `VERIDIAN_MCP_AUDIENCE`
  (défaut `https://notifuse.app.veridian.site/api/mcp`).
- Pas de commande CLI : la route est exemptée (`scripts/veridian/cli-route-exemptions.txt`), un JWT du Hub ne s'émet pas
  depuis une CLI. Registre : `hub mcp-app add notifuse <url> --acces tous`.
