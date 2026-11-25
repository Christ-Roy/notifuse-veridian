# 94. Un import dit ce qui est vraiment dans l'espace (25/11/2026)

**Origine** : session ODH, espace ilumphotos. `notifuse contacts:import` a annoncé 806 réussites, 806 contacts étaient refusés. Trois causes : le CLI
prenait l'absence d'un champ pour un succès ; il envoyait les nombres d'un CSV en texte, que l'API refuse ; l'API répond 200 avec une erreur par
contact, remplace la vraie raison par « email is required », tait l'échec d'inscription à une liste et inscrit même à une liste inexistante.
**Portée** : tout appelant de `POST /api/contacts.import` (API, console, CLI). `contacts.intake` (fiche 85) compte déjà ce qu'il écrit.

- `internal/veridian/contactimport` enveloppe la route (motif « POST /chemin », l'amont est intact) et ajoute `report` à la réponse, RELU dans l'espace :
  `state`, `received, created, updated, failed, duplicates`, `verified` (fiches retrouvées), `lists {id: {subscribed, other, absent, unknown}}`.
- `state` : `complet`, `partiel`, `echec` (rien n'est entré), `non_verifie` (relecture en panne : rien n'est affirmé). Un désinscrit gardé (`other`) n'est
  pas un manque. Le code HTTP ne change pas.
- Un contact illisible garde son adresse et sa vraie raison (`custom_number_1 …`).
- CLI : `state` en tête, sortie 0 complet, 1 incomplet, **2 non abouti** ; sans `report`, jamais « complet ». Colonnes `custom_number_*` d'un CSV
  converties (virgule, espace fine) ou refusées avant l'envoi, avec la ligne ; lignes sans email comptées.

Épreuves : `contactimport_test.go` (base réelle), `TestVeridianContactImport` (application entière), `test_workspace_default_and_csv_import.py`.
