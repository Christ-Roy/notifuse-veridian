# 75 : lot L4, qualité et sécurité (29/10/2026)

Périmètre : S2 aperçu e-mail, Liquid dans les données de contact, rétro-remplissage de `message_data`,
tests portés de l'upstream, caractérisation du noyau Veridian, flakiness vitest.

## S2 : aperçu e-mail sans bac à sable (checklist sécurité S2)

- `console/src/components/email_builder/panels/Preview.tsx` : l'`<iframe srcDoc>` n'avait pas
  d'attribut `sandbox`. Le HTML rendu contient le contenu du modèle et les données de contact : un
  `<img onerror>` s'exécutait dans l'origine de la console, où vit le jeton (localStorage). Corrigé :
  `sandbox=""`.
- Même classe, corrigée : `MjTextBlock` et `MjButtonBlock` montaient le contenu du modèle par
  `dangerouslySetInnerHTML` sans assainissement (GHSA-pff3-q54v-9xw9). Ils passent par
  `console/src/lib/veridian_sanitize_html.ts` (DOMPurify, dépendance directe, version du lockfile).
- Garde de classe : `console/src/__tests__/veridian_iframe_sandbox_guard.test.ts` échoue si une
  `<iframe srcDoc>` n'a pas de `sandbox`, si elle porte `allow-scripts`, ou si un
  `dangerouslySetInnerHTML` n'est pas assaini. Elle s'autoteste (elle doit voir au moins 5 iframes
  et traverser un `>` dans une expression JSX).
- Exception assumée : `MjRawBlock` garde `allow-same-origin` sans `allow-scripts` (il mesure la hauteur
  du contenu). Sans script, `allow-same-origin` n'ouvre rien.

## Liquid dans les données de contact (correctif de la classe v42, réécrit)

`CompileTemplate` passait le MJML par Liquid par bloc, puis sur la chaîne entière (blocs `mj-liquid`),
puis par le spintax. Une donnée « {{ contact.email }} » ou « {% for %} » (un nom d'entreprise scrapé)
était évaluée à la passe suivante. `pkg/notifuse_mjml/veridian_liquid_data.go` neutralise les
accolades des valeurs texte avant tout rendu (caractères d'usage privé U+E000/U+E001) et les remet
avant la compilation MJML. Entrée jamais modifiée. Le Liquid du modèle (texte, `mj-liquid`) reste évalué.
Réécrit à partir de la description publique de l'advisory : aucun code BSL (v40+) n'est repris.

## Rétro-remplissage de `message_data` en clair

Défaut : l'ancien `Update` de `message_history` réécrivait `message_data` en clair après envoi.
Corrigé en amont (commit `4714ca67`). Reste les lignes écrites avant le correctif : 91 dans
`notifuse_ws_animalsd` entre le 11 et le 25/10 (mesure lot 0, `upstream/fork-analysis/lot0/mesure.tsv`).

Outil : `scripts/backfill/messagedata` (dry-run par défaut, `-apply` écrit). Chaque ligne : chiffrement,
aller-retour déchiffré comparé à l'original, `UPDATE ... WHERE message_data::text = <valeur lue>`
(une ligne réécrite entre-temps n'est pas écrasée). Journal : identifiants et compteurs seulement.
Codes de sortie 0 / 1 / 2 (2 = une ligne n'a pas passé l'aller-retour, arrêt immédiat).

Testé sur PostgreSQL jetable (`integration_test.go`, `VERIDIAN_TEST_POSTGRES_DSN`) : le format relu par
le vrai `MessageHistoryRepository.Get`, dry-run sans écriture, idempotence, lignes chiffrées / vides /
sans `data` inchangées octet pour octet, écriture concurrente préservée, arrêt sur échec.
**Non exécuté en production.**

### Avant d'exécuter (sauvegarde restaurable à vérifier)

La base de prod est le job `notifuse` (stanza pgBackRest `notifuse`, dépôt R2 chiffré, WAL archivés
toutes les 5 min). Les anciennes valeurs en clair ne sont conservées nulle part par l'outil (c'est
le but) : le retour arrière passe par une restauration à l'instant T0. À vérifier, dans l'ordre :

1. Restaurabilité prouvée, moins de 24 h :
   `~/nomad-veridian/scripts/pgbackrest-restore-drill-clients.sh notifuse` doit rendre
   `PGBACKREST_DRILL stanza=notifuse result=ok` (le verdict est dans
   `~/all-cron/_logs/backups/pgbackrest-restore-drill-clients.verdict`).
2. Archivage continu sain : `pgbackrest --stanza=notifuse info` (dans la tâche `notifuse-db`)
   montre une sauvegarde complète récente et un dernier WAL de moins de 10 min.
3. Noter T0 (`SELECT now()`) juste avant `-apply`. Restauration éventuelle :
   `--type=time --target=T0` sur une copie jetable (comme le drill), puis recopier les `message_data`
   des identifiants du journal. Jamais restaurer sur la base vivante.
4. Compter avant : la requête de `mesure-chiffrement.sql` doit redonner 91 en clair sur animalsd.
5. Dry-run (91 attendus), `-apply`, relancer le dry-run (0 attendu), relire une ligne par l'API.

Exécution : binaire statique construit dans `golang:1.26.10-alpine`
(`CGO_ENABLED=0 go build -o messagedata ./scripts/backfill/messagedata`), copié dans l'allocation
`notifuse` (la base est en 127.0.0.1:5432 dans le groupe bridge), `SECRET_KEY` déjà dans son environnement.
Décision d'exécuter : Robert.

## Tests portés de l'upstream (v39.1, dernière version AGPL)

Source : `upstream/notifuse-upstream.git`, tag v39.1 (jamais v40+, BSL). Méthode : pour les paquets
communs (broadcasts, file, templates, segments, contacts, listes, webhooks), les fonctions de test absentes
du fork (1 106 par nom) sont extraites par `port.py` (déclarations absentes du paquet seulement), compilées, et
celles qui ne compilent pas sont retirées (fonctionnalité absente : ~970 déclarations, surtout v33 à v39 :
analytique web, permissions, OIDC, SES). Restent **101 tests** (30 fichiers `*_upstream39_test.go`) :
**38 actifs et verts**, 63 ignorés par `t.Skip` avec la raison en clair.

Défauts du fork révélés (le test passe sur l'upstream, échoue chez nous) :

| Id | Défaut | État |
|---|---|---|
| D1 | `lists.update` enregistre `is_double_optin` / `is_public` omis comme `false` | non corrigé : changement de contrat API (refuser l'omission), décision Robert |
| D2 | `broadcasts.update` écrase les champs absents (segments : l'envoi part sur toute la liste ; test A/B ; planification ; UTM) | non corrigé, **le plus grave** : un `broadcasts:update` partiel de la CLI est dangereux tant que ce n'est pas porté |
| D3 | la réécriture UTM perdait la requête d'un lien (`;`, `%` nu) et réordonnait les paramètres (signatures) | **corrigé** (port v39.1 : chirurgie sur la requête brute) |
| D4 | la cellule du pixel d'ouverture réservait une ligne de texte vide sous chaque e-mail | **corrigé** (markup v39.1) |
| D5 | `email_options.subject_preview` (API) : `&`, `<` en mode code, rendu Liquid doublé | non corrigé (API transactionnelle, pas la prospection) |
| D6 | curseur de pagination des événements perd les sous-secondes | non corrigé |
| D7 | `mj-title` / `mj-preview` : une donnée contenant `<` cassait la compilation | **corrigé** (entités numériques, comme l'upstream) |

Non analysés (A1 file de segments, A2 filtres de segment, 18 divers) et fonctionnalités absentes (permissions
par ressource v39.0 : 12 tests, effacement par e-mail : 3) : `grep -rn 't.Skip("' --include=*upstream39_test.go`.
Les quatre advisories v42 ne sont pas portées en code (BSL) : les tests de comportement équivalents sont écrits
pour le fork (Liquid des données, bac à sable), SSRF et `SECRET_KEY` restent à traiter (lot 0).

## Noyau Veridian : mutation plutôt que couverture

Couverture par instruction des fichiers `veridian_*` mesurée avant : **81,9 %** (9 380 / 11 449 instructions,
205 fichiers, `go test -cover ./internal/... ./pkg/...`, 29/10). Elle est déjà haute et ne dit pas si les tests
mordent : l'épreuve est donc faite par mutation (`mutbatch.py` : opérateurs de comparaison, logiques, bornes,
retours booléens ; un mutant est tué si un test échoue). Mutants générés et résultat :

| Fichier | Mutants | Tués avant | Tués après |
|---|---|---|---|
| `pkg/veridian_spintax` (2 fichiers) | 32 | 18 | 23 |
| `pkg/veridian_ndr/parser.go` | 28 | 11 | 27 |
| `pkg/veridian_deliverability` | 24 | 13 | 23 |
| `domain/veridian_fair_queue.go` | 14 | 10 | 10 |
| `domain/veridian_warmup.go` | 11 | 7 | 9 |
| `domain/veridian_sending_window.go` | 14 | 8 | 14 |
| `queue/circuit_breaker.go` | 13 | 10 | 10 |
| `queue/veridian_reputation_gate.go`, `veridian_effective_plan.go`, `veridian_plan_capacity.go` | 34 | 18 | non remesuré |
| **Total** | **170** | **95 (56 %)** | **134 + queue non remesuré** |

Les survivants restants sont équivalents ou sans effet (tri à égalité, `j >= 0` quand j ne vaut jamais 0,
journalisation, `score > 10` borné) ; la liste est dans le rapport L4. Tests ajoutés : `parser_characterization_test.go`,
`veridian_spintax_characterization_test.go`, `veridian_deliverability_characterization_test.go`,
`internal/domain/veridian_characterization_test.go`, `internal/service/queue/veridian_characterization_test.go`
(ce dernier écrit sur les survivants, vu vert, mutants de la file à remesurer : `specC2` non exécutée).
Trouvailles : `extractAllTextInto` lit le corps brut d'une part imbriquée avant de récurser, donc la limite de
profondeur est inopérante (comportement figé par un test) ; une donnée ou un modèle `mj-liquid` n'est plus un vecteur
d'injection.

Pas fait (consigne de resserrage du 29/10) : mesure de couverture « après » sur tout le dépôt, remesure des mutants
de la file, D1/D2/D5/D6, exécution du rétro-remplissage en production.

## Flakiness vitest (les 6 tests à 30 s du 28/10)

Mesure (rapports JSON des runs CI du 28/10, `vitest-console-report`) : 265 fichiers, 725 tests,
mur de 378 à 562 s. **Un seul fichier**, `veridian_cold_outreach_settings.test.tsx` (44 tests), pesait 322
à 502 s, soit 60 à 90 % du temps cumulé de la suite : médiane 5 s par test, maxima de 28 à 48 s. Cause :
un rendu de la page (23 classes, 2 400 nœuds antd) coûte 4 à 7 s en jsdom, chaque changement d'état à la
racine un re-rendu de 2,5 s, et la charge du runner multiplie le tout par 3 à 6. Le timeout n'était pas
en cause : le fichier avait déjà été relevé de 5 à 30 s puis 90 s (commit `a21fe55e`), ce qui masque la
dérive au lieu de la traiter.

Correction durable (aucun timeout allongé) :
1. Partition du fichier en quatre par thème (`.limits`, `.imap-window`, `.infra`, `.presets` : 8, 8, 10,
   18 tests), exécutés en parallèle ; timeout par test ramené de 90 à 30 s.
2. `maxWorkers: '50%'` en CI (`console/vitest.config.ts`) : le runner est partagé, un worker par cœur
   faisait ramer les rendus lourds.
3. Étape CI « Budget de lenteur vitest » : liste les 5 fichiers les plus lents, avertit au-delà de 20 s
   par test, échoue au-delà de 240 s pour un fichier. Éprouvée sur les rapports du 28/10 : elle échoue
   sur le fichier unique (484 s), passe sur la partition.

Après (bastion à charge 12 à 13, plus sévère que le runner) : fichiers de 117, 81, 63 et 62 s (chemin critique
117 s contre 322 à 502 s), test le plus lent 21 s (le premier de son fichier, qui paie le premier rendu
antd). Piste non élucidée : la première requête `getByLabelText` ou `getByRole(..., { name })` d'un test
coûte 8 à 10 s alors que la suivante coûte 40 ms ; un remplacement de `element.labels` n'y change rien
(essayé, retiré). Ne pas réunifier les fichiers sans refaire la mesure.
