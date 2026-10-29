# Dossiers natifs (lot L2, migration V65, 29/10/2026)

Segments, listes et modèles se rangent dans un arbre de dossiers propre à chaque genre. Avant, le rangement
reposait sur une convention de nom `Dossier / Sous-dossier / Nom` : invisible pour l'API, ordre de tri non maîtrisé.

## Modèle de données (base workspace, tables neuves, aucune table upstream modifiée)

- `veridian_folders(id, kind, parent_id, name, position)` : l'arbre. Unicité des frères insensible à la casse
  (`kind, parent_key, name_key`, contrainte DIFFÉRABLE : une opération qui déplace plusieurs dossiers ne passe par
  un état invalide qu'en transit).
- `veridian_folder_items(kind, item_id, folder_id, position)` : l'appartenance. Pas de ligne = à la racine, sans rang.
- Pas de clé étrangère vers segments / lists / templates (tables upstream) : une ligne orpheline (objet supprimé)
  est retirée à la lecture suivante.

## Le nom stocké reste le chemin complet (compatibilité)

`segments.name`, `lists.name`, `templates.name` (dernière version) gardent la forme `A / B / C`. Le serveur la réécrit
à chaque déplacement d'objet, renommage ou déplacement de dossier : tout client qui lit encore le nom reste juste.
`label` = dernier élément (le nom court). La vérité est l'arbre, le nom en est le reflet lisible.
Un nom de dossier ou de libellé ne peut pas contenir ` / ` (`AC/DC` passe : le séparateur est « espace slash espace »).

## Adoption (V65 et garde-fou continu)

`State.Adopt` convertit les noms à chemin en arbre : sans perte, sans modifier un nom, idempotent, ordre naturel
(P2 avant P10, accents et casse ignorés, comme l'ancienne console). V65 le joue sur chaque workspace ; `folders.list`
le rejoue (une fois, avec un verrou) si un client crée encore un objet `X / Y` à l'ancienne.
Cas connu : « 5 Qualité / Champs corrompus (cart_ / liens) » contient un `/` accidentel, donc un faux dossier
« Champs corrompus (cart_ ». L'ancienne console faisait pareil ; on le corrige par `segments:move` + `--name`.

## API (JWT ou clé scopée ; droits : segments = contacts, listes = lists, modèles = templates)

`GET|POST /api/veridian/folders.list`, `POST folders.create | rename | move | delete | reorder`,
`POST /api/veridian/{segments,lists,templates}.move`. OpenAPI : `openapi/paths/veridian-folders.yaml`.
CLI : `folders:list|create|rename|move|delete|reorder`, `segments:move`, `lists:move`, `templates:move`.
Ordre explicite : `position` (indice) ou `before_id` ; `folders.reorder` fixe l'ordre complet d'un niveau.
Suppression d'un dossier : refusée s'il n'est pas vide, sauf `contents=to_parent` ; jamais d'objet supprimé.

## Code

`internal/veridian/folders` (cœur pur + SQL + service), `folders/api` (HTTP + capacité `platform`),
`internal/migrations/veridian/v65_dossiers.go`, console `components/common/veridian_folder_*`.
Tests : cœur pur (jeu réel animalsd, golden généré par le module TypeScript de l'ancienne console), SQL sur vrai
PostgreSQL (`VERIDIAN_TEST_POSTGRES_DSN`), HTTP, migration V65.
