// Package veridian est le registre de migrations propre au code Veridian (lot L0, 29/10/2026).
//
// Pourquoi un registre séparé : le gestionnaire upstream (internal/migrations) suit UN scalaire
// (settings.db_version) et ne rejoue jamais un numéro inférieur. Quand plusieurs lots livrent des
// migrations dans le désordre (V65 avant V63), les plus bas seraient sautées en prod. Ici chaque
// migration est suivie PAR NUMÉRO, dans la table veridian_migrations de chaque base (système et
// workspace) : le runner joue au démarrage les numéros manquants, dans l'ordre, sans dépendre de
// config.VERSION ni de db_version. Un nouveau workspace reçoit toutes les migrations à sa création.
//
// Règles (détail dans docs/veridian/MODULARITE.md) :
//   - un fichier par migration, vNN_<nom>.go, qui appelle Register dans init() ;
//   - numéros réservés par lot (Reserved) : V63 L1, V64 L1b, V65 L2, V66 L3, V67 L4 ; au-delà, main
//     attribue le numéro en l'ajoutant à Reserved ;
//   - EXPAND d'abord : instructions additives et idempotentes (CheckExpandOnly le vérifie à
//     l'enregistrement, un échec est un panic au démarrage et en test) ; le CONTRACT (suppression,
//     NOT NULL, renommage) est une migration à part, d'un numéro attribué par main, livrée après
//     que le tag précédent n'est plus en service (Spec.Contract + Spec.ContractOf) ;
//   - pas de CREATE INDEX CONCURRENTLY (le runner joue chaque migration dans une transaction).
package veridian
