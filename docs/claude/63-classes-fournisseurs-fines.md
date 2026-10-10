# 63. Classes fournisseurs fines (10/10/2026)

`other_hoster` pesait 47 % du stock de prospection (12 721 contacts sur 26 937) : une réputation chez Infomaniak ne dit rien de celle chez Gandi, et un seul débit pour tout ne se règle pas. Sept classes sorties d'`other_hoster` : `infomaniak`, `gandi`, `hostinger` (Titan inclus), `o2switch`, `lws`, `scaleway` (Online, BookMyName), `website_builder` (Webador, Jimdo, Webmo, Wix...). `other_hoster` reste la classe de la longue traîne.

## Débit par défaut sûr
Une classe absente de la table de débits ou de plafonds d'un profil est NON bridée. Sans garde, les sept nouvelles classes seraient donc partis sans frein sur tous les profils existants. `VeridianParentClass` : une classe fine sans entrée propre **hérite** du débit, du plafond journalier et de l'exclusion d'`other_hoster` (`VeridianRateForClass`, `VeridianCapForClass`, `VeridianResolveExcludedClasses`). Une entrée propre l'emporte. Le total reste borné par le plafond de profil et la chauffe. Le fusible proportionné (÷2, ÷4) tient par couple émetteur x classe : chaque classe fine démarre à ÷1.

## Une table MX, deux langages
La table MX -> classe vit dans `veridian_provider_class_mx.go`. L'acquisition (Python) part du groupe fournisseur ODH (dérivé des MX). Parité contrôlée par `internal/domain/testdata/veridian_mx_class_parity.json` : le test Go la vérifie, `batch/test_provider_class.py` (acquisition) la rejoue via ODH `classify_host` puis `map_class`, et compare la copie au fichier du fork.

Aussi : passerelles anti-spam supplémentaires reconnues par MX (ppe-hosted, iphmx, spamexperts, mailanyone...). Tout le détail d'exploitation du stock : dépôt `acquisition-emailing`, `batch/relabel_stock_classes.py`.
