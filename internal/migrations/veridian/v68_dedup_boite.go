package veridian

import "github.com/Notifuse/notifuse/internal/veridian/mailbox/dedupsql"

// V68 (lot L1c, 01/11/2026) : déduplication de la boîte unifiée. Le filtre Sieve d'un relais copie
// chaque vraie réponse vers la boîte Infomaniak de Hugo, que Notifuse lit aussi : le même mail
// (même Message-ID) était rangé deux fois. Colonnes duplicate_of (lien vers l'original) et
// dedup_key (clé de regroupement), webhook qui ne signale pas la copie, puis rattrapage des
// doublons existants. Additive et idempotente : le tag précédent ignore les colonnes.
// Aucune ligne supprimée, aucun message sortant touché. Contrat et classement : paquet dedupsql.
func init() {
	Register(Spec{Number: 68, Lot: "L1c", Name: "dedup_boite", Workspace: dedupsql.Statements})
}
