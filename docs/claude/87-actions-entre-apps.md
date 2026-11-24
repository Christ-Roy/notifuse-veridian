# 87 — Actions demandées par une autre app (24/11/2026, INTEGRATIONS-NATIVES §2.2 et 2.3, F2)

`POST /api/veridian/actions/{action}` (`internal/veridian/actions/`, migration V78). Une app (Prospection, CRM) demande une action au nom
d'un utilisateur ; le Hub signe un jeton d'action de 120 s. Le Hub ne l'émet pas encore : éprouvé avec une clé ES256 locale.

## Le jeton (401 `jeton_refuse` sinon)
ES256, `kid` au JWKS du Hub, `iss`, `aud` = `https://notifuse.app.veridian.site/api/veridian/actions` (`VERIDIAN_ACTIONS_AUDIENCE`) ou l'adresse MCP (`VERIDIAN_MCP_AUDIENCE`, ce que le Hub signe aujourd'hui), `email_verified`,
`sub` = `hub_user_id`, `act: {"sub":"app:<app>"}` sans chaîne, `tenant_id`, `jti` (8 à 128), `iat`, `exp - iat` et reste à vivre ≤ 120 s.
- `jti` : sert UNE fois, même si la demande est refusée ensuite (401 `jeton_deja_vu`) ; mémoire 10 min ; base injoignable = 503, jamais d'exécution.
- Un jeton MCP est refusé ici (sans `act`), un jeton d'action est refusé sur `/api/mcp`.
- Portée exacte par action, apps autorisées par action (403 `portee_insuffisante`, `app_non_autorisee`).
- Espace : celui relié à CE `tenant_id` (`hub_tenant_id`, jamais un slug, jamais le corps). 409 `espace_non_relie` ; 403 `utilisateur_inconnu`,
  `non_membre`, `espace_suspendu` (suspendu, ou membre gelé). Les droits fins restent ceux de l'utilisateur (action rejouée sur le routeur).

## Idempotence
`Idempotency-Key` (8 à 128) obligatoire hors simulation, par (espace, app, clé), 30 jours : un rejeu rend le premier résultat (`rejoue: true`).
Autre demande sous la même clé = 422 `cle_reutilisee` ; en cours = 409 `en_cours` ; refus ou panne = clé rendue ; geste mort repris après 2 min.
Origine (`app`, `jti`, `hub_user_id`) gardée dans `veridian_action_done` et au journal.

## Les trois actions (portée `notifuse:contacts.ecrire`, ou `notifuse:contacts.importer` ; apps `app:prospection`, `app:crm`)
Réponse : `{action, rejoue, simulation, resultat}`. Aucun champ inconnu n'est accepté, l'espace vient du jeton, **aucun envoi possible**.
- `contacts.importer` `{list_ids, contacts (1 à 1000), dry_run}` : passe par `contacts.intake` (fiche 85) avec `allow_triggers=false` et
  `existing=attach` : fiche existante intacte, désinscrits, rebonds et plaintes exclus, `custom_json_1.veridian_source` = l'app du jeton. Tout
  champ inconnu d'un contact va dans `custom_json_1`. `resultat` : `dry_run, received, created, updated, existing, subscribed, skipped {motif: n}, details,
  triggers`. Une liste qui démarre une séquence : 409 `refus_de_l_app`, `detail.code = would_trigger`, rien d'écrit (en simulation : 200 et `triggers`).
- `listes.lister` `{}` : `resultat.lists [{id, name, starts_sequence}]` (200 au plus). Lecture, sans `Idempotency-Key`.
- `listes.creer` `{id (1 à 32 lettres ou chiffres), name}` : privée, sans double opt-in. `resultat {id, name, existait}` ; même id et même nom = rendue ;
  même id, autre nom = 409.

## Épreuves
Refus et idempotence : `internal/veridian/actions/*_test.go`. Application entière (25 contacts, rejeu 0, désinscrit exclu, file vide, membre
retiré refusé) : `INTEGRATION_TESTS=true TEST_DB_HOST=… TEST_DB_PORT=… go test -run TestVeridianActions ./tests/integration`.
Une action neuve = une entrée du catalogue, une portée, et `TestCatalogue_AucuneActionNEnvoie` relu.
