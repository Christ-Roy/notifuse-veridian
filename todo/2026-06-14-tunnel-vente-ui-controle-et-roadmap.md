# [NOTIFUSE] Tunnel de vente — bug UI bloquant + roadmap contrôle/data (audit Robert 2026-06-14)

> **Sévérité** : 🔴 P0 (bug UI labels) + 🟡 P1 (roadmap data/contrôle)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-14 par Robert (audit UI réel `/console` prod)
> **Contexte** : audit visuel prod par Robert. Le team-lead pilotait le backend
> SANS contrôler l'UI réelle → a validé une UI cassée sur un rapport DOM d'agent
> qui comptait les éléments présents sans vérifier le CONTENU des labels. Faute
> de process : toute validation UI doit passer par un rendu réel dans Chrome, pas
> un comptage d'éléments. Robert (verbatim) : *"j'aimerais que tu aies toujours
> une idée de ce qu'il y a sur l'UI en plus du backend et de savoir ce qui marche
> et ce qui est bien fait"* + *"arrête de te chier dessus vous êtes trop timide"*.

---

## 🔴 BUG P0 — labels de classe VIDES dans Settings → Cold outreach (prod)

### Symptôme (vu en prod 2026-06-14, SHA 56b5acf0)
Les 5 cartes de config par classe de provider s'affichent **sans aucun nom de
classe**. L'utilisateur voit 5 cartes identiques anonymes ("Rate (emails/min)" +
"Open pixel" + un badge orange "pixel OFF by default" sur 2 d'entre elles) sans
savoir laquelle est Google, Microsoft, Yahoo, etc. **Inutilisable.**

Vérifié DOM : `<Text strong>` de chaque carte = chaîne VIDE (`""`).

### Cause racine (certaine)
`veridian_cold_outreach_settings.tsx:241` rend `<Text strong>{classLabel(c, t)}</Text>`.
La fonction `classLabel(c, t)` (lignes 45-58) utilise `` t`Google (Gmail / Workspace)` ``
etc., MAIS `t` est passé en **paramètre** d'une fonction utilitaire HORS composant
React. **L'extracteur statique Lingui ne capte pas** les template literals quand `t`
n'est pas le `t` direct d'un `useLingui()` dans le scope du composant. Résultat :
- les 5 clés (`Google (Gmail / Workspace)`, `Microsoft (Outlook / Microsoft 365)`,
  `Yahoo / AOL`, `French ISPs (Orange, SFR, Free…)`, `Corporate (any other domain)`)
  sont **ABSENTES du catalogue** (vérifié : 0 occurrence dans `en.po`).
- donc `t` renvoie **vide** en runtime → labels invisibles.

### Fix (au choix, le plus simple = option A)
- **A (recommandé)** : ne PAS faire passer `t` à `classLabel`. Soit définir les
  labels avec le macro `msg\`...\`` (Lingui `defineMessage`) extractible statiquement
  + `i18n._(msg)` au point de rendu, soit inliner un `switch` directement dans le
  `.map()` du composant (chaque `` t`...` `` dans le scope du `useLingui()`).
  Vérifier que les 5 clés apparaissent dans `en.po` après `npm run lingui:extract`.
- **B** : garder `classLabel` mais retourner des chaînes NON-traduites (les noms de
  provider sont des noms propres — "Google", "Microsoft" n'ont pas besoin de
  traduction). Supprime la dépendance i18n sur ces labels = zéro risque de vide.
  → option B la plus robuste : un nom de provider ne se traduit pas.
- ⚠️ Vérifier qu'il n'y a PAS d'AUTRES `t` passés en paramètre ailleurs dans le
  code Veridian (même piège) : `grep -rn "t: ReturnType<typeof useLingui>" console/src`.

### DoD bug
- [ ] Les 5 cartes affichent le nom de classe (Google/Microsoft/Yahoo-AOL/FAI FR/Corporate)
- [ ] Validé par RENDU RÉEL dans Chrome (screenshot), pas un comptage DOM
- [ ] `npm run lingui:extract` + `lingui:compile` → clés présentes (si option A)
- [ ] tsc vert, push, re-valider sur staging avec ?cachebust=, PUIS prod

---

## 🔴 BUG P0 #2 — "Dashboard" du menu workspace → page "Not Found" (prod)

### Symptôme (vu en prod 2026-06-14)
En entrant dans un workspace, le 1er item du menu latéral "Dashboard" est actif et
affiche **"Not Found"** (page blanche). C'est le PREMIER écran que voit un user dans
son workspace → impression d'app cassée. Les autres pages (Contacts, Templates,
Broadcasts, Logs, Settings…) fonctionnent.

### Cause racine (certaine)
`console/src/router.tsx` : la route `workspaceRoute` (`/console/workspace/$workspaceId`)
a un **index route** (`path: '/'`, ligne ~174) + des enfants (`/broadcasts`,
`/automations`, `/lists`, `/file-manager`, `/transactional`…) **MAIS AUCUNE route
`/dashboard`**. Or le menu latéral pointe le lien "Dashboard" vers
`/console/workspace/$id/dashboard` → route inexistante → catch-all "Not Found".

NB : `DashboardPage.tsx` existe mais c'est le composant de SÉLECTION de workspace
(monté sur `/console/`), pas une page interne au workspace. Le commentaire router.tsx
ligne 172 dit "redirects to analytics/dashboard" → il y a une confusion/régression
sur ce que "dashboard" désigne.

### Fix (au choix)
- **A** : faire pointer le lien "Dashboard" du menu vers l'index workspace
  (`/console/workspace/$id` racine) au lieu de `/dashboard`. Le plus simple si la
  racine affiche déjà quelque chose d'utile.
- **B** : créer une vraie route+page `/console/workspace/$id/dashboard` (vue d'accueil
  workspace : stats d'envoi, raccourcis). Plus de travail mais comble un vrai manque
  (un workspace mail mérite une home avec des chiffres).
- Vérifier ce que l'index route `workspace/$id` (`path:'/'`) rend actuellement, et où
  le menu sidebar construit le lien "Dashboard" (chercher dans le composant de nav).

### DoD bug #2
- [ ] Le lien "Dashboard" mène à une page qui s'affiche (pas "Not Found")
- [ ] Validé par RENDU RÉEL Chrome (screenshot)
- [ ] Cohérence : soit on supprime l'item "Dashboard" si redondant avec l'index, soit
      il a une vraie page

---

## ✅ CE QUI EXISTE DÉJÀ (inventaire backend, ne pas reconstruire)

- **API config par classe** : `WorkspaceSettings.VeridianProviderClassRates`
  (`map[classe]float64`) + `VeridianOpenPixelByClass` (`map[classe]bool`), persistés
  via `POST /api/workspaces.update`. **Donc OUI : on peut régler les paramètres du
  tunnel via API.** Override par broadcast via `broadcast.metadata`. ➜ répond à la
  question de Robert "régler les paramètres via API" = OUI déjà possible (workspace
  global + override broadcast).
- **Custom fields contact (Notifuse vanilla)** : `custom_string_1..5`,
  `custom_number_1..5`, `custom_datetime_1..5`, `custom_json_1..5` (cf
  `internal/domain/contact.go:48-70`). Le tunnel utilise `custom_string_5` pour
  tagger la classe de provider. ➜ il RESTE 4 string + 5 number + 5 datetime + 5 JSON
  libres pour la data contact complémentaire. **Pas besoin de migration** pour
  enrichir la data contact. Réponse à Robert : oui ce sont les custom fields vanilla,
  rien à "éteindre", il faut les EXPLOITER (mapping documenté à définir).
- **Classification provider** : `internal/domain/veridian_provider_class.go`
  (classe dérivée du suffixe email OU forcée par `custom_string_5`).

## ⏳ CE QUI N'EXISTE PAS ENCORE (roadmap demandée par Robert)

### R1 — Data "nombre de contacts par fournisseur"
Aucun agrégat aujourd'hui. À construire : endpoint
`GET /api/veridian/contacts.providerBreakdown?list_id=...` qui fait un GROUP BY sur
la classe de provider (dérivée du suffixe email + override `custom_string_5`) et
retourne `{google: N, microsoft: N, yahoo_aol: N, freemail_fr: N, corporate: N}`.
Affiché dans l'UI Cold outreach (à côté de chaque carte de classe : "X contacts")
et/ou sur la page Contacts/Lists. ➜ permet de dimensionner le throttle en connaissance
de cause (ex : 5000 contacts Google à 1/min = combien de jours).

#### ✅ R1 BACKEND LIVRÉ (agent databack, 2026-06-14) — reste l'UI

**Endpoint** : `POST` **et** `GET` `/api/veridian/contacts.providerBreakdown`
(les deux méthodes routées explicitement — piège catchall `root_handler.go`).

**Auth** : JWT console (`RequireAuth`) + gardien `AuthenticateUserForWorkspace`
+ permission `contacts:read` côté service (un user non membre du workspace → 403,
exactement comme `/api/contacts.list`). Ce n'est PAS du HMAC Hub : c'est un
endpoint console interne, consommé par l'UI Cold outreach.

**Paramètres** :
- `workspace_id` (requis)
- `list_id` (optionnel — restreint aux contacts membres de cette liste, hors
  entrées soft-deleted ; même EXISTS subquery que `contacts.list`)

**Réponse 200** (les 5 classes canoniques toujours présentes, 0 si vide) :
```json
{
  "breakdown": {
    "google": 1240, "microsoft": 830, "yahoo_aol": 95,
    "freemail_fr": 410, "corporate": 1502
  },
  "total": 4077
}
```

**Exemple curl** (JWT console dans le header) :
```bash
curl -s -X POST https://notifuse.staging.veridian.site/api/veridian/contacts.providerBreakdown \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"workspace_id":"<ws>","list_id":"<optional>"}'
# équivalent GET :
curl -s "https://notifuse.staging.veridian.site/api/veridian/contacts.providerBreakdown?workspace_id=<ws>&list_id=<optional>" \
  -H "Authorization: Bearer $JWT"
```

**Classification** : réutilise STRICTEMENT `veridian_provider_class.go` —
override `custom_string_5` prime, sinon suffixe de domaine, sinon `corporate`.
Pas de CASE SQL (zéro duplication de la table de domaines) : le repo projette
`(email, custom_string_5)` et la classification se fait en Go
(`VeridianAggregateProviderBreakdown`). Coût = SELECT de 2 colonnes texte + scan
O(n) ; négligeable sur le volume cold outreach (dizaines de k de contacts/ws).
Index PK sur `email` existant ; pas de migration nécessaire.

**Fichiers** (convention `veridian_*.go` flat, zéro patch upstream) :
- `internal/domain/veridian_provider_breakdown.go` (+ test) — types, interfaces,
  agrégation pure
- `internal/repository/veridian_contact_breakdown_postgres.go` (+ test) — SELECT
  Squirrel + filtre liste EXISTS
- `internal/service/veridian_contact_breakdown_service.go` (+ test) — auth +
  permission contacts:read
- `internal/http/veridian_contact_breakdown_handler.go` (+ test) — POST+GET,
  403 perm / 401 auth / 500 sinon
- câblage `internal/app/app.go` (bloc "R1 breakdown contacts")
- mocks générés : `mock_veridian_contact_breakdown_{repository,service}.go`

**⏳ TODO UI (agent uifix)** : afficher le compte par classe à côté de chaque
carte dans `console/src/components/settings/veridian_cold_outreach_settings.tsx`.
Appeler l'endpoint au mount (TanStack Query), passer `workspace_id` (déjà dans le
contexte de la page Settings). `list_id` non pertinent au niveau workspace
Settings (laisser vide = tout le workspace) ; il deviendra utile si on affiche le
breakdown dans le drawer broadcast (où une liste est sélectionnée). Mapper les 5
clés vers les libellés des cartes (mêmes clés canoniques que le throttle). Ajouter
la constante de route dans `console/src/services/api/` à côté des endpoints
contacts. ⚠️ Piège SW cache : valider staging avec `?cachebust=`.

### R2 — Paramètres par infra mail d'envoi (domaine + IP)
Aujourd'hui le throttle est par WORKSPACE (global) + override broadcast. Robert veut
pouvoir cadrer les débits **par infra d'envoi** (un domaine d'envoi + son IP/relai ont
leur propre capacité de warm-up). Modèle cible à spécifier :
- une "sending infra" = {domaine, IP/relai SMTP, état de warm-up, capacité max/jour}
- les rates par classe pourraient être **relatifs à l'infra** (une IP fraîche en
  warm-up = rates plus bas que matures). Lien avec le skill `postfix` (warm-up cold).
- Décision archi : table dédiée `sending_infra` ? ou métadonnée sur l'intégration SMTP
  existante (`workspaces.integrations`) ? À trancher.

### R3 — Paramètres par utilisateur (seat)
Robert veut aussi du réglage "par utilisateur". Préciser le besoin métier avec lui :
- est-ce "par membre du workspace qui envoie" (chaque commercial a son débit) ?
- ou "par boîte d'envoi connectée" (recouvre R2) ?
- la V48 avait une colonne morte `mail_provider_choice` (INERTE) qui anticipait du
  per-user — voir si on la réveille ou si R2/R3 fusionnent.

### R4 — Data contact complémentaire (enrichissement)
Exploiter les custom fields libres pour stocker : fournisseur identifié (déjà
`custom_string_5`), domaine d'envoi cible, score d'engagement, etc. Définir et
DOCUMENTER le mapping custom_field → sémantique tunnel (sinon dette : un champ custom
sans convention = data perdue). ➜ ticket de convention à écrire.

---

## Process à graver (pour le team-lead)
- **Toute UI livrée se valide par un RENDU RÉEL dans Chrome** (screenshot + lecture
  du CONTENU des labels), jamais un comptage d'éléments DOM. Un agent qui rapporte
  "5 Card présentes" n'a PAS validé que les Card affichent quelque chose d'utile.
- Le team-lead garde une vue de l'UI réelle en plus du backend. "Vert au CI" ≠
  "beau et utilisable".

## Priorisation suggérée
1. 🔴 Fix bug labels (P0, ~30 min, débloque l'usage immédiat du tunnel)
2. 🟡 R1 data contacts par provider (P1, donne le contexte pour régler le throttle)
3. 🟡 R2 infra d'envoi domaine+IP (P1, lié warm-up — arbitrage archi avec Robert)
4. 🟢 R3 per-user + R4 convention custom fields (P2, à spécifier avec Robert)
