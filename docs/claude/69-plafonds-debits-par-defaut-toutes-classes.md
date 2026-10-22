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

## Scission de `freemail_fr` en FAI (22/10/2026)
Décision Robert : `freemail_fr` regroupait Orange, SFR, Free, La Poste et Bouygues. Débit, plafond
journalier et fusible étant par couple (domaine émetteur x classe), un refus d'Orange ralentissait
tout le groupe. Chaque fournisseur a maintenant sa classe, ses défauts et son fusible.

| Classe | Domaines (suffixe d'adresse) | MX (dig du 22/10) | Plafond / jour | Débit / min |
|---|---|---|---|---|
| `orange` | orange.fr, wanadoo.fr, orange-business.com | smtp-in*.orange.fr, mx.mailbox.orange-business.com, smtp-in{a,b}.orange.com, relais-ias*.francetelecom.com | 10 | 0,1 |
| `sfr_altice` | sfr.fr, neuf.fr, numericable.fr/.com, sfr.net, altice.net, club-internet.fr, cegetel.net, noos.fr, 9online.fr | smtp-in.sfr.fr, smtp1/2.sfr.com, mx.{gva,zrh}.net.altice.net | 20 | 0,2 |
| `free` | free.fr, aliceadsl.fr | mx1/mx2.free.fr, mx-g19.proxad.net | 20 | 0,2 |
| `laposte` | laposte.net, laposte.fr | smtpz4.laposte.net, rpi0i*.laposte.fr | 20 | 0,2 |
| `bouygues` | bbox.fr | mx.bbox.fr, mx-b.bbox.fr | 20 | 0,2 |

Partis de `freemail_fr` (20 / 0,2) : aucun défaut n'augmente, Orange baisse à 10 / 0,1 (le plus sévère
sur le cold). `bouyguestelecom.fr` n'a pas de MX Bouygues (Proofpoint : `security_gateway`).

- **`freemail_fr` reste une classe valide** : classe historique et de repli. Reste dedans `gmx.fr`,
  `dartybox.com` (pas de famille FAI) et tout domaine tagué `freemail_fr` dont ni le suffixe ni le MX
  ne donne un FAI. Mêmes défauts qu'avant.
- **Comment la classe est lue** (mesuré dans le code) : le tag `custom_string_5` du contact est copié
  dans `payload.VeridianProviderClass` à l'enqueue (`VeridianApplyProviderThrottle`) et figé ; sans tag
  le worker résout suffixe puis MX en cache ; débit, plafond et fusible lisent cette classe ; la classe
  finale est écrite sur `message_history.veridian_provider_class` (V55) et les compteurs sont par
  classe. Le `breakdown` classe par tag puis suffixe, sans MX.
- **Aucun contact à migrer.** Un tag `freemail_fr` est ré-éclaté à la lecture : par domaine dans
  `VeridianContactProviderClass` (zéro I/O), par domaine puis MX dans le worker
  (`veridianRefineLegacyFreemail`, cache MX 7 jours) et avant la réservation atomique du plafond.
  Les entrées déjà en file sont ré-éclatées à l'envoi. Seule une classe de la famille FAI est retenue :
  un tag périmé ne bascule jamais un contact vers google ou microsoft. Le `breakdown` éclate aussi
  les tags `freemail_fr` par domaine.
- **Mesure animalsd avant déploiement (22/10)** : file de 1 052 entrées, toutes avec `payload.veridian_provider_class`
  vide (le chemin automation ne copie pas le tag, la classe se résout à l'envoi) ; 886 contacts tagués
  `freemail_fr` (orange.fr 528, wanadoo.fr 195, free.fr 48, laposte.net 46, sfr.fr 40, bbox.fr 12, neuf.fr 7,
  club-internet.fr 2, numericable.com 1, 7 domaines pro) ; aucun tag à réécrire.
- **Mesure animalsd avant déploiement (22/10)** : file de 1 052 entrées, toutes avec `payload.veridian_provider_class`
  vide (le chemin automation ne copie pas le tag, la classe se résout à l'envoi) ; 886 contacts tagués
  `freemail_fr` (orange.fr 528, wanadoo.fr 195, free.fr 48, laposte.net 46, sfr.fr 40, bbox.fr 12, neuf.fr 7,
  club-internet.fr 2, numericable.com 1, 7 domaines pro) ; aucun tag à réécrire.
- **Héritage** (`VeridianParentClass` : parent `freemail_fr`) : un débit, un plafond, une politique
  pixel ou une exclusion posés sur `freemail_fr` s'appliquent à chaque FAI tant qu'il n'a pas les siens.
  Exclure `freemail_fr` exclut les cinq FAI (sinon un contact tagué et re-classé passerait l'exclusion).
  Conséquence assumée : une surcharge `freemail_fr: N` vaut désormais N par FAI.
- **Compteurs** : le jour du déploiement, les envois déjà faits sont comptés sous `freemail_fr` ; les
  FAI repartent de zéro pour la journée et leur fusible de réputation (taux de rebond par classe)
  repart sans historique.
- **ODH** : `acquisition-emailing/batch/provider_class.py` produit les nouvelles classes (groupes
  orange, altice/sfr/sfr_business, iliad/free, laposte, bouygues) ; `mx_table.json` et
  `mx_class_parity.json` recopiés octet pour octet, le test ODH échoue si la copie diverge.
  Les FAI restent hors pilote (`PILOT_CLASSES`) comme `freemail_fr`.
- **Console** : libellés, listes et défauts pixel des 5 classes (`workspace.ts` et cartes).
  CLI `notifuse` : aucune liste de classes en dur, rien à changer.
