package veridian

import "github.com/Notifuse/notifuse/internal/veridian/activite/activitesql"

// V86 (26/11/2026, CONTRAT-HUB §5.25) : la file de l'activité Mail vers le Hub (envoyé, remis, ouvert, cliqué, rejeté, désinscrit,
// reçu) et ses deux déclencheurs. Additive : une table, ses index, deux fonctions et deux déclencheurs neufs.
func init() {
	Register(Spec{Number: 86, Lot: "Activite", Name: "activite_mail", Workspace: activitesql.Statements})
}
