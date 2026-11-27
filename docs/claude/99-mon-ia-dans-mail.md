# 99 — « Mon IA » dans Mail (VER-203, 26/11/2026)

Contrat : `veridian-hub/docs/CONTRAT-HUB.md` §5.24 (fils) et §5.24.1 (quand l'app montre la fenêtre, marques communes). Code : `internal/veridian/iafils/`,
migration V87 (`veridian_ia_fils`), console `components/veridian_mon_ia.tsx` + `lib/veridian_mon_ia.ts`. Référence côté Academy : `ia-fils-contrat.test.ts`.

## Les fils (§5.24) : `GET|PUT /api/hub/ia/fils/{session}`
- Serveur à serveur, signature Hub **v2** (`VeridianHMACMiddleware` : méthode, chemin, query, empreinte du corps, usage unique). Non signé : 401, rejeu : 401,
  sans `HUB_API_SECRET` : 503. **Toute origine navigateur est refusée même signée** (403 `browser_origin_refused` : `Origin` ou `Sec-Fetch-*` présents).
- `session`, `hub_user_id`, `tenant_id` sont des UUID. `tenant_id` = l'organisation du Hub ; l'espace est celui de `veridian_plan.hub_tenant_id`
  (jamais l'inverse). `PUT` sur une organisation sans espace : 404 `espace_non_relie`.
- `GET` : 200 `{messages}` pour CE triplet, sinon 404 `fil_inconnu` (absent, autre personne, autre organisation, personne retirée de l'espace, espace supprimé :
  même réponse). `PUT` : 200 `{}` ; autre triplet : 409 `fil_autre_auteur` sans rien écrire (l'auteur est lu AVANT la place de l'appelant, et l'écriture est
  une seule instruction : `ON CONFLICT … WHERE` même triplet, donc deux auteurs en course n'écrasent pas) ; personne plus membre : 403 `member_removed`.
- Bornes : 1 000 000 octets de corps (413 `fil_trop_grand`, au-delà de 1 MiB c'est la porte de signature qui rend 413), 200 messages (413 `fil_trop_long`).
  Chaque message doit être un objet JSON ; il est stocké tel quel (colonne `JSON`, texte exact, pas `JSONB` qui réordonne les clés).
- **Le fil suit le sort** : suppression de l'organisation = `SoftDelete` (`DeleteWorkspace`, et la cascade FK à la purge) ; retrait d'un membre =
  `RemoveMember` du Hub (`DeleteMember`, rejoué aussi quand la personne n'était déjà plus membre). Câblage : `ConfigureIAFils` (`veridian_wiring.go`),
  même gabarit que la coupure des accès de boîte (fiche 91). Retrait fait par la console sans le Hub : la lecture relit la place à chaque appel, le fil n'est plus
  servi (le nettoyage attend le prochain retrait signé ou la suppression de l'organisation).
- CLI : `hub:ia-fil-get`, `hub:ia-fil-put` (HMAC), `ia:etat` (owner). Épreuves : `iafils/fils_test.go` (401, rejeu, origine, 409, 404, bornes), `store_pg_test.go`
  (vrai PostgreSQL : triplet, course, suppression par membre et par organisation, cascade), `internal/service/veridian_ia_fils_test.go`.

## L'affichage (§5.24.1)
- Serveur : `POST {HUB}/api/ia/etat` signé **à nonce** (`SignEtat`) : HMAC-SHA256 hex du secret de l'app sur `ts.nonce.corps`, `X-Veridian-App: notifuse`, ts en
  ms, nonce `[A-Za-z0-9_-]{16,128}`. **Ce n'est pas la v2** de `SignVeridianHubRequest` (celle-ci lie méthode, chemin, query). Vecteur de test tiré du code du Hub
  (`lib/delegation/jeton.ts`, `verifierAppel`). Secret = `HUB_API_SECRET` (celui de l'app, comme `/api/organisations/lire`).
- Cache 60 s par (personne, organisation), 404 compris ; 401, 429, 5xx, réseau, illisible : jamais gardés et « indisponible ».
- Console : `GET /api/veridian/ia/etat?workspace_id=` (session, membre de l'espace) rend `{etat, hub_url, tenant_id}` ; `hub_url` et `tenant_id` seulement pour
  `prete` et `sans_moteur`. Hub : `HUB_BASE_URL`, défaut `https://app.veridian.site` ; la CSP (`frame-src https://app.veridian.site`, fiche 96) l'autorise.
- Marques communes (le cas 56 de la batterie du Hub les lit) : `[data-mon-ia="prete"|"sans_moteur"]` ; `prete` : bouton `Mon IA`, dialogue « Mon IA », cadre
  `{HUB}/ia/chat?app=notifuse&tenant=<tenant_id>` inséré seulement à l'ouverture ; `sans_moteur` : bouton `Relier mon IA`, `[data-carte="relier-mon-ia"]`
  avec le lien `Relier mon IA` (`{HUB}/dashboard/ia`, `target=_blank`), jamais un cadre ; `eteinte`, 404, Hub muet : aucune marque. Console ouverte dans un cadre : rien.
- Le texte de la carte est celui du contrat, tel quel (il dit « sans clé » : on ne propose jamais de champ ni de saisie de clé).
- Épreuves : `veridian_mon_ia.test.tsx` (sélecteurs exacts ; vu échouer : cadre forcé en `sans_moteur`, cadre inséré avant le clic).
- Staging : le cadre vers le Hub de prod serait bloqué par la CSP si `HUB_BASE_URL` pointe ailleurs ; la fenêtre n'est éprouvée qu'en prod.

## Un seul point d'entrée, l'installation du connecteur dans la carte (27/11)

Un seul point d'entrée IA dans l'en-tête : la marque `[data-mon-ia]`. L'ancien bouton « Brancher mon IA » (`VeridianConnectAI`) est supprimé de
`WorkspaceLayout.tsx` ; son contenu (installation par assistant, adresse MCP, commande Claude Code, révocation) est dans la carte `sans_moteur`, sous le
lien « Relier mon IA » (fiche `83`). Garde : `veridian_layout_memo.test.tsx` échoue si l'en-tête le rend de nouveau.


**Origine navigateur (27/11)** : les fils refusent `Origin`, `Sec-Fetch-Site` et `Sec-Fetch-Dest`, jamais `Sec-Fetch-Mode` seul. Le fetch de
Node (undici), dont se sert le Hub, l'ajoute de lui-même : le cas 56 du Hub a pris 403 sur son PUT à 00:59:44Z. Test
`TestFils_HubAvecSecFetchModeSeul_Passe`, vu échouer avec l'ancienne garde.
