# 69 Plafonds et débits PAR DÉFAUT pour toutes les classes (19/10/2026)

Décision Robert (19/10) : Notifuse est l'outil de prédilection pour charger les segments
complets d'ODH. Chaque classe de fournisseur destinataire est donc bridée dès la création
d'un workspace, sans réglage. Le réglage workspace / profil / broadcast reste une surcharge.

## Ce qui a changé
- `internal/domain/veridian_class_defaults.go` : plafond journalier et débit/minute par défaut
  de chacune des 20 classes (par couple domaine émetteur x classe, comme les surcharges).
  Un test échoue si une classe canonique n'a pas de valeur.
- `VeridianCapForClass` / `VeridianRateForClass` : surcharge propre, sinon surcharge de la classe
  parente (`other_hoster`), sinon DÉFAUT serveur. Toujours `ok=true`, toujours > 0. Un 0 explicite
  vaut défaut (aucune classe sans frein). Pour lever un plafond : poser une grande valeur.
  `VeridianConfigured*ForClass` donne la surcharge seule.
- Portes du worker (`veridian_gate_limits.go`, `veridian_provider_throttle.go`) : plus de no-op
  quand la table est vide. EffectivePlan / overview affichent `class_caps_source = default`.
- Exemption inchangée : un transactionnel avec profil réservé ne passe aucune porte commerciale.
- Le cap journalier compte la classe MATÉRIALISÉE sur `message_history.veridian_provider_class`
  (V55, déjà en place depuis le correctif du 28/09 : `CountSentSinceForClass*`). Pas de migration
  nouvelle, `config.VERSION` inchangé.
- Tests historiques du worker : `TestMain` (`veridian_main_test.go`) coupe les défauts
  (`veridianClassDefaultsEnabled=false`) ; les tests du chemin par défaut les rallument avec
  `enableVeridianClassDefaults(t)`. Ne jamais couper cette variable en production.

## Vocabulaire et classification
- 2 classes de plus : `zoho`, `proton` (parent `other_hoster`). Total 20.
- Table MX étendue sur la mesure de ~3 550 domaines d'alliancequalipro (passerelles anti-spam FR,
  1and1.fr, hébergeurs de la longue traîne, FAI, builders).
  Mesure : hors auto-hébergement réel, inconnus tiers 176 (5,0 %) -> 72 (2,0 %).
- Étiquette `custom_string_5` non canonique (`godaddy`, `Hostinger `...) : ignorée par
  `VeridianContactProviderClass` / `veridianClassifyRecipient`, le MX décide. Casse et espaces
  sont normalisés. Choix : ignorer plutôt que mapper des alias (un alias faux gèle un domaine
  dans la mauvaise classe).
- Échec DNS transitoire : n'est plus figé 7 jours en `corporate_selfhost` (TTL 5 min, compteur
  `TransientFailures`). NXDOMAIN / pas de MX / MX inconnu restent définitifs (7 jours).
- Classifieur MX absent du worker : repli suffixe VISIBLE (log d'erreur une fois +
  `mxFallbackCount`). Le constructeur de prod l'installe toujours (`mx_classifier` dans le log
  de démarrage du worker).

## Source unique du vocabulaire (ODH)
- Source : `veridianMXPatternTable` (Go). Export généré : `internal/domain/testdata/
  veridian_mx_table.json` (`UPDATE_MX_TABLE=1 go test ./internal/domain -run TestMXTableExport`),
  le test échoue si la table bouge sans régénération.
- ODH (`acquisition-emailing/batch/provider_class.py`) lit `mx_table.json` (copie octet pour octet,
  contrôlée par `test_provider_class.py` contre le fork) et l'applique EN PREMIER dans `classify_mx`.
  154 règles Go, 0 divergence ODH. Recopier le fichier à chaque changement de table.
