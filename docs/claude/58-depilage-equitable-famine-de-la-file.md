# 58. Dépilage équitable de `email_queue` (famine du 10/10/2026)

Date : 10/10/2026. Base : `origin/veridian` au commit `c080e51d` (prod `v61.0-veridian.fd85ce35`). Constat de Robert (10/10) : depuis le 06/10, 100 % des envois du workspace `robertbrunon` sont des J0 `ecom-devenir-a/b` (environ 300 par jour) ; les relances J+4 et les segments vétuste et croissance ne partent jamais.

## Cause racine (mesurée)

`EmailQueueRepository.FetchPending` triait **toutes** les entrées dues par `priority ASC, created_at ASC` puis coupait à `LIMIT` (taille du lot du worker, 50 au plus, bien moins quand le débit déclaré est bas). Toutes les entrées de séquence naissent en priorité 5 : l'ordre réel était donc `created_at`, c'est-à-dire **l'âge**.

Mesure sur la base de production (lecture seule, 10/10 vers 11h40 Paris, 2 142 entrées en file) :

| Lot | Entrées | `created_at` | `next_retry_at` |
|---|---|---|---|
| J0 `ecom-devenir-a/b` | 1 581 | 30/09 09h à 20h | posé (replanifiées en boucle) pour 1 200 d'entre elles, NULL pour 375 |
| J0 `ecom-scale-a/b` | 200 | 30/09 20h40 | NULL |
| J0 `ecom-vetuste-a/b` | 172 | 30/09 20h56 | NULL |
| `ecom-relance-j4` | 189 | 04/10 au 10/10 | NULL |

Mécanisme, dans l'ordre :

1. Les plafonds, le débit par classe, la fenêtre et la réservation atomique **replanifient** une entrée refusée (`SetNextRetry`, délai court) **sans consommer de tentative**. L'entrée sort de la file due, puis y **revient à l'échéance, avec son `created_at` d'origine, donc de nouveau en tête**.
2. Les 1 575 J0 « devenir » les plus anciens repassent ainsi toujours devant tout le reste. Un lot ne contient que des entrées qu'un gate vient de refuser ou qui passent à leur débit (environ 300 par jour). Les relances (créées après), vétuste et croissance (créés après 20h30 le 30/09) ne sont jamais lues : **famine**, pas un bug de gate.
3. Les entrées `NULL` étaient bien éligibles (`next_retry_at IS NULL OR <= NOW()`) : le filtre n'était pas en cause, c'est l'ordre qui les enterrait derrière les plus vieilles.
4. Rien dans `message_history` n'a jamais porté `ecom-relance-j4` : les relances n'ont pas été envoyées une seule fois.

## Correctif de la classe

`FetchPending` ne coupe plus par âge. La politique (fonction pure `domain.VeridianSelectFairBatch`, `internal/domain/veridian_fair_queue.go`) :

1. **La priorité explicite reste la première clé.**
2. À priorité égale, les **relances** passent avant les premiers contacts. Une entrée est une relance quand le contact a déjà reçu un mail **de la même automation** (`message_history.automation_id = email_queue.source_id`, `sent_at` posé, pas en échec, hors le message de l'entrée elle-même). Le critère se lit dans la donnée existante : aucune migration, aucune réécriture de la file.
3. Dans un palier, **tourniquet entre les sources** (`source_id` : automation ou diffusion). Un tour = une entrée par source ; la source qui ouvre le tour tourne à chaque appel (compteur du dépôt), donc même un lot de 1 sert les trois automations en trois appels.
4. Dans une source, la **moins récemment examinée** d'abord : `COALESCE(next_retry_at, created_at)`. Une entrée repoussée par un gate cède la place aux entrées jamais essayées ; sans replanification, c'est le FIFO d'avant.

La requête SQL borne le lot candidat (`ROW_NUMBER() OVER (PARTITION BY priority, source_id, is_followup ORDER BY examined_at, created_at, id) <= LIMIT`), le choix final est fait en Go. Le prédicat « dû » est **inchangé** (pending sans retard ou échu, failed rejouables, processing coincés de plus de 2 minutes). Coût mesuré sur la base de production : 26 ms pour 2 142 entrées en file et 23 000 lignes de table.

Intact : plafonds, débit par classe, chauffe, fenêtre, fusible proportionné, réservation atomique du quota, rendu au dépilage, séparation transactionnelle. Le worker (`worker.go`) n'est pas modifié : toute la politique est dans le dépôt.

## Pas de reprise manuelle

Aucune écriture dans les données de `robertbrunon`. Les entrées `NULL` (375 J0 devenir, 372 vétuste et croissance) et les 189 relances partent d'elles-mêmes au premier dépilage après déploiement, aux débits et plafonds habituels (les relances étant dues, elles passent en premier ; le plafond du jour borne le total).

## Tests (échouent contre l'ancien ordre)

- `internal/domain/veridian_fair_queue_test.go` : simulation de la file du 10/10 (1 575 J0 devenir dont un gate repousse presque tout, 200 vétuste, 200 croissance, 190 relances). `TestFairQueue_LegacyOrderStarvesFollowupsAndOtherSegments` rejoue l'ancien ordre et constate 0 relance, 0 vétuste, 0 croissance sur 30 minutes ; `TestFairQueue_FairOrderServesFollowupsAndEverySegment` constate que tout part, relances au premier tick, chaque entrée au plus une fois. Plus : relances avant J0, priorité explicite conservée, tourniquet entre 3 automations (lot de 9 : 3/3/3 ; lot de 1 : les 3 en 3 appels), entrée repoussée cédant devant une entrée neuve, `NULL` éligible, pas de doublon.
- `internal/repository/email_queue_postgres_test.go` : `FetchPending` (requête équitable, relance en tête, un tour par automation).
- Épreuve de mutation faite : `VeridianSelectFairBatch` remplacée par l'ancien tri, 5 tests du domaine et le test du dépôt échouent.

## Preuve et suivi

- Avant envoi : requête de lecture seule sur `email_queue` (ordre simulé : relances, puis mélange des trois segments).
- Après 8h le lundi : `message_history` doit montrer des `template_id` `ecom-relance-j4`, `ecom-vetuste-*`, `ecom-scale-*` avec `sent_at` après le déploiement (requête dans le rendu de la session du 10/10).
- Piège voisin : l'ordre d'un dépilage se juge sur la file réelle, pas sur la lecture du code. Un lot d'une seule automation en tête de file, des entrées `NULL` anciennes et un `last_error` vide sont le signe de cette famine.
