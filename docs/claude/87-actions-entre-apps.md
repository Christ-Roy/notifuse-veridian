# 87 — Actions demandées par une autre app (24/11/2026, INTEGRATIONS-NATIVES §2.2 et 2.3, F2)

`POST /api/veridian/actions/{action}` (`internal/veridian/actions/`, migration V78). Une app (Prospection, CRM) demande une action au nom
d'un utilisateur ; le Hub signe un jeton d'action de 120 s. Premier vrai jeton du Hub joué en staging le 24/11/2026 (cas 25 du Hub, écritures relues ici).

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

## Les actions (apps `app:prospection`, `app:crm` ; portée `notifuse:contacts.ecrire` du registre du Hub, sauf mention)
Réponse : `{action, rejoue, simulation, resultat}`. Aucun champ inconnu n'est accepté, l'espace vient du jeton, **aucune action n'envoie**.
- `contacts.importer` `{list_ids, contacts (1 à 1000), dry_run}` : passe par `contacts.intake` (fiche 85) avec `allow_triggers=false` et
  `existing=attach` : fiche existante (même adresse) complétée sur ses seuls champs vides, téléphone, site, tout champ simple ou clé de
  `custom_json_1` absente, jamais une valeur présente écrasée (`updated` = fiches complétées ; 27/11/2026, le téléphone de Prospection
  n'atteignait pas une fiche déjà là). Si une séquence active démarre sur `contact.updated`, rien n'est complété (attache seule) ; désinscrits, rebonds et plaintes exclus, `custom_json_1.veridian_source` = l'app du jeton. Tout
  champ inconnu d'un contact va dans `custom_json_1`. `resultat` : `dry_run, received, created, updated, existing, subscribed, skipped {motif: n}, details,
  triggers`. Une liste qui démarre une séquence : 409 `refus_de_l_app`, `detail.code = would_trigger`, rien d'écrit (en simulation : 200 et `triggers`).
- `listes.lister` `{}` : `resultat.lists [{id, name, starts_sequence}]` (200 au plus). Lecture, sans `Idempotency-Key`.
- `listes.creer` `{id (1 à 32 lettres ou chiffres), name}` : privée, sans double opt-in. `resultat {id, name, existait}` ; même id et même nom = rendue ;
  même id, autre nom = 409.
- `contacts.lire` (portée `notifuse:contacts.lire`) `{emails (1 à 50)}` : `resultat.contacts [{email, existe, listes [{id, name, status}], bloque
  (desinscrit, rebond, plainte ou null), a_repondu, sequences_en_cours [{id, name}], dernier_envoi}]`, dans l'ordre demandé. Lecture ; droits contacts,
  listes, séquences et historique de l'utilisateur. Route `POST /api/veridian/contacts.state`, CLI `contacts:state`.
- `sequences.lister` `{}` : `resultat.sequences [{id, name, status, list_id, inscriptible, raison (brouillon, en_pause, inactive)}]`. Lecture.
- `sequences.inscrire` (portée à elle : **`notifuse:sequences.inscrire`**, car elle fait partir du courrier) `{sequence_id, contacts (1 à 50)
  [{email, first_name, last_name, phone, job_title, ne_plus_contacter}], inclure_deja_contactes, dry_run}`. Fiche d'abord (`contacts.intake`, `attach`,
  sans liste ni déclencheur), puis `reservoir.activate` sous le lot dérivé de (app, clé). `resultat` : `{dry_run, received, inscrits,
  skipped {motif: n}, details [{email, motif}], note}`, un motif par personne : `desinscrit`, `rebond`, `plainte`, `a_deja_repondu`,
  `deja_dans_la_sequence`, `dans_une_autre_sequence`, `deja_contacte`, `ne_plus_contacter` (dit par l'app, la personne ne quitte pas la porte),
  `adresse_invalide`, `fiche_refusee`, `non_inscrit`. Les six premiers sont ceux de la fiche 85 : aucune règle n'est recopiée.
  **Plafond : 200 personnes par jour, par utilisateur et par espace**, toutes apps (429 `plafond_du_jour`, clé rendue ; un geste en cours compte
  pour 50, sinon des appels parallèles passeraient dessous). Séquence inconnue, en pause, hors plafond ou qui en démarrerait une autre : `refus_de_l_app`.
  Rien ne part tout de suite : fenêtre, plafonds, chauffe, exclusions et relais propre décident à l'envoi, comme pour tout autre contact.

## Écrire depuis une autre app
Aucune action n'envoie. Le lien `/console/workspace/<espace>/inbox?ecrire=<adresse>` (par le rebond du Hub) ouvre « Nouveau message » avec ce
destinataire : la personne choisit le profil, écrit, confirme. Une seule adresse, sans rien d'autre, sinon le paramètre est ignoré.

## Épreuves
Refus et idempotence : `internal/veridian/actions/*_test.go`. Application entière (25 contacts, rejeu 0, désinscrit exclu, file vide, membre
retiré refusé, deux inscrits sur quatre à une séquence, rejeu et désinscrit compris) : `INTEGRATION_TESTS=true TEST_DB_HOST=… TEST_DB_PORT=… go test -run TestVeridianActions ./tests/integration`.
Une action neuve = une entrée du catalogue, une portée, et `TestCatalogue_AucuneActionNEnvoie` relu.
