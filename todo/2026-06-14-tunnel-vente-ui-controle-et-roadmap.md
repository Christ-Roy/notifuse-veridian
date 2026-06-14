# [NOTIFUSE] Tunnel de vente — bugs UI P0 + roadmap contrôle/data (audit Robert 2026-06-14)

> **Sévérité** : 🔴 P0 (2 bugs UI) + 🟡 P1 (roadmap data/contrôle)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-14 par Robert (audit UI réel `/console` prod)
> **Contexte** : le team-lead pilotait le backend SANS contrôler l'UI réelle →
> a validé une UI cassée sur un rapport DOM d'agent qui comptait les éléments
> présents sans vérifier le CONTENU des labels. Faute de process. Robert (verbatim) :
> *"j'aimerais que tu aies toujours une idée de ce qu'il y a sur l'UI en plus du
> backend et de savoir ce qui marche et ce qui est bien fait"* + *"arrête de te
> chier dessus vous êtes trop timide"*.

---

## 🔴 BUG P0 #1 — labels de classe VIDES dans Settings → Cold outreach  [FIX APPLIQUÉ, à valider]

### Symptôme (prod 2026-06-14, SHA 56b5acf0)
Les 5 cartes de config par classe s'affichaient SANS nom (le `<Text strong>` = ""),
5 cartes anonymes identiques → inutilisable.

### Cause racine
`veridian_cold_outreach_settings.tsx` : `classLabel(c, t)` utilisait `` t`Google…` ``
avec `t` passé en PARAMÈTRE d'une fonction HORS composant → l'extracteur statique
Lingui ne capte pas ces clés → absentes du catalogue (0 dans `en.po`) → `t` rend vide.

### Fix appliqué (option B — littéraux)
`classLabel(c)` retourne des chaînes LITTÉRALES (noms propres de providers, ne se
traduisent pas). Plus de `t` en paramètre → zéro dépendance i18n → zéro risque de vide.
Les 2 call-sites mis à jour (`classLabel(c)`). À VALIDER par rendu réel Chrome.

## 🔴 BUG P0 #2 — menu "Dashboard" workspace → "Not Found"  [À FIXER]

### Symptôme (prod 2026-06-14)
En entrant dans un workspace, l'item de menu "Dashboard" affiche "Not Found"
(premier écran vu = cassé). Autres pages OK.

### Cause racine
`router.tsx` : `workspaceRoute` (`/console/workspace/$workspaceId`) a un index route
(`path:'/'`) + enfants (`/broadcasts`, `/automations`, `/lists`, `/file-manager`,
`/transactional`…) mais PAS de `/dashboard`. Le menu sidebar pointe "Dashboard" vers
`/console/workspace/$id/dashboard` → route inexistante → catch-all "Not Found".

### Fix (au choix)
- A : pointer le lien "Dashboard" du menu vers l'index workspace `/console/workspace/$id`.
- B : créer une vraie route+page `/dashboard` (home workspace : stats d'envoi, raccourcis).
Investiguer ce que l'index route rend + où le menu construit le lien. Trancher proprement.

---

## ✅ CE QUI EXISTE DÉJÀ (inventaire backend, ne pas reconstruire)

- **API config par classe** : `WorkspaceSettings.VeridianProviderClassRates`
  (`map[classe]float64`) + `VeridianOpenPixelByClass` (`map[classe]bool`), persistés
  via `POST /api/workspaces.update`. ➜ **régler les paramètres du tunnel via API = OUI
  déjà possible** (workspace global + override par broadcast.metadata).
- **Custom fields contact (vanilla)** : `custom_string_1..5`, `custom_number_1..5`,
  `custom_datetime_1..5`, `custom_json_1..5` (`internal/domain/contact.go:48-70`). Le
  tunnel utilise `custom_string_5` = classe de provider. ➜ il RESTE 4 string + 5 number
  + 5 datetime + 5 JSON libres pour enrichir la data contact. PAS besoin de migration.
  Réponse à Robert : oui ce sont les custom fields vanilla, rien à éteindre, il faut les
  EXPLOITER (mapping à documenter — cf R4).
- **Classification provider** : `internal/domain/veridian_provider_class.go` (suffixe
  email + override `custom_string_5`).

## ⏳ ROADMAP demandée par Robert

### R1 — Data "nombre de contacts par fournisseur"  [EN COURS — agent databack]
Endpoint breakdown contacts par classe (`google/microsoft/yahoo_aol/freemail_fr/
corporate`) → affiché en UI à côté de chaque carte de classe pour dimensionner le
throttle (ex : 5000 contacts Google à 1/min = X jours). Réutilise
`ClassifyProviderClass` (suffixe + override custom_string_5). Spec : voir brief databack.

### R2 — Paramètres par infra mail d'envoi (domaine + IP)
Aujourd'hui throttle par WORKSPACE (global) + override broadcast. Robert veut cadrer
les débits **par infra d'envoi** (domaine + IP/relai ont leur propre capacité de warm-up).
Modèle cible : une "sending infra" = {domaine, IP/relai SMTP, état warm-up, capacité
max/jour}, rates par classe relatifs à l'infra (IP fraîche = rates bas). Lien skill
`postfix` (warm-up cold). Archi à trancher : table `sending_infra` dédiée vs métadonnée
sur l'intégration SMTP existante (`workspaces.integrations`).

### R3 — Paramètres par utilisateur (seat)
Préciser le besoin avec Robert : par membre qui envoie ? ou par boîte d'envoi connectée
(recouvre R2) ? La V48 a une colonne morte `mail_provider_choice` (INERTE) qui anticipait
du per-user — réveiller ou fusionner avec R2.

### R4 — Convention data contact complémentaire
DOCUMENTER le mapping custom_field → sémantique tunnel (fournisseur, domaine cible,
score…). Sans convention écrite = data perdue.

---

## Process à graver (team-lead)
- **Toute UI livrée se valide par un RENDU RÉEL dans Chrome** (screenshot + lecture du
  CONTENU des labels via innerText), JAMAIS un comptage d'éléments DOM. "5 Card présentes"
  ≠ "5 Card qui affichent quelque chose d'utile". "Vert au CI" ≠ "beau et utilisable".
- Le team-lead garde une vue de l'UI réelle en plus du backend, à chaque livraison.

## Priorisation
1. 🔴 Bug #1 labels (FIX appliqué, valider rendu réel) + Bug #2 dashboard (à fixer) — débloquent l'usage immédiat
2. 🟡 R1 data contacts par provider (donne le contexte pour régler le throttle) — en cours
3. 🟡 R2 infra domaine+IP (lié warm-up, arbitrage archi Robert)
4. 🟢 R3 per-user + R4 convention custom fields (à spécifier avec Robert)
