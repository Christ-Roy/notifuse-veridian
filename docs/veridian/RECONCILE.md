# Réconciliateur (lot L1b, 29/10/2026)

Le logiciel recoupe en continu ce qu'il croit (statuts de contacts, compteurs, classes, file, stock) avec ce qui s'est réellement passé, signale chaque écart avec sa cause en français et propose une réparation exécutable par API.

Code : `internal/veridian/reconcile/` (capacité `reconcile`, câblée par `internal/app/veridian_mod_reconcile.go`). Table `veridian_reconcile_finding` : migration **V64** (`internal/migrations/veridian/v64_reconcile.go`, DDL dans `reconcile/schema`). Aucun daemon, aucune tâche planifiée : le passage se lance à la demande.

## Trois états, jamais deux

Un contrôle rend `ok`, `ecarts` ou `non_abouti`. Un contrôle qui n'a pas pu conclure (erreur, table absente, panique) est **non abouti** : il ne ferme et n'ouvre aucun écart, la CLI sort en 2, la console l'affiche en toutes lettres. Codes de sortie de `notifuse reconcile:run` : 0 tout concorde, 1 des écarts, 2 non abouti.

## Interface

| Surface | Appel |
|---|---|
| API | `POST /api/veridian/reconcile.run`, `GET\|POST /api/veridian/reconcile.report`, `POST /api/veridian/reconcile.resolve` (`apply`, `dry_run`, `dismiss`) |
| CLI | `notifuse reconcile:run <ws> [--check C]`, `reconcile:report <ws> [--all]`, `reconcile:resolve <ws> --id F [--dry-run \| --dismiss] [--yes]` |
| Console | page « Écarts » (`/reconcile`) : cause, attendu, constaté, preuve, bouton de réparation avec confirmation, simulation, « écarter 7 jours » |

Droits : lecture de `contacts`, `message_history`, `automations` pour tout ; `resolve` exige en plus l'écriture de `contacts` (reclassement) ou d'`automations` (sorties, file, compteurs).

## Les contrôles (un fichier chacun)

| N° | Code | Fichier | Ce qu'il vérifie | Réparation |
|---|---|---|---|---|
| 3 | `statut_coherent` | `inv_statut.go` | un contact qui a répondu / rebondi / s'est désinscrit / est dans une liste « exclu » n'est plus inscrit ni en file ; aucun mail après réponse ou désinscription | sortir les contacts, retirer les envois en file (journalisé) |
| 4 | `compteurs` | `inv_compteurs.go` | compteurs stockés = inscriptions ; « envoyés à l'étape » servi = mails partis ; sorties pour réponse = réponses humaines ; partition de J0 ; raisons de sortie rangées | recalculer `automations.stats` |
| 6 | `classes` | `inv_classes.go` | `custom_string_5` = classe du domaine quand elle est certaine | reclasser (`freemail_fr` vers orange, free...) |
| 7 | `stock` | `inv_stock.go` | aucun envoi en file pour un mail déjà parti ; stock annoncé = recompte indépendant | retirer le doublon de la file |

Les contrôles 1, 2 et 5 (envoyé = livré, réponse rattachée, copies aux humains) lisent `veridian_mail_message` (lot L1).

Chaque grandeur comptée a UNE définition écrite (`definitions.go`, rendue dans le rapport). Les chiffres servis sont reconstruits avec le même code que `prospection.stats`, puis recoupés avec une requête indépendante.

## Garde-fous

- Une réparation relit ses cibles au moment d'appliquer (jamais la liste vue à la détection), tout dans une transaction ; un échec ne modifie rien.
- Après `apply`, le contrôle est rejoué : `verified` dit si l'écart a disparu, sinon `remaining` donne le résidu.
- Les compteurs bougent pendant qu'on les lit : un écart de compteur n'est retenu que s'il est identique à deux lectures espacées de 1,5 s.
- Délai de grâce de 15 minutes sur les statuts (la relève des boîtes est asynchrone). Une séquence qui a un nœud « branche réponse » traite la réponse elle-même et n'est pas jugée.
- Un écart écarté à la main ne revient pas avant 7 jours.
- La convention « liste d'exclusion » est un nom de liste contenant « exclu » (ex. « Clients ERP exclus de la prospection »).
