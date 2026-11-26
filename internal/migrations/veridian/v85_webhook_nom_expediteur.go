package veridian

import "github.com/Notifuse/notifuse/internal/veridian/mailbox/dedupsql"

// V85 (26/11/2026, VER-247) : le webhook message.received porte from_name, le nom affiché de l'expéditeur. Sans lui, la fiche CRM qu'une
// réponse humaine crée reste sans nom. Remplace la fonction du déclencheur (V77) : un champ de plus, additif pour les lecteurs.
func init() {
	Register(Spec{Number: 85, Lot: "L1c", Name: "webhook_nom_expediteur", Workspace: []string{dedupsql.WebhookTriggerFunction}})
}
