package veridian

import "github.com/Notifuse/notifuse/internal/veridian/mailbox/dedupsql"

// V77 (24/11/2026, VER-247) : le webhook message.received ne part plus deux fois pour un mail lu par deux boîtes, et dit ce qui
// rattache le mail à nos envois (match_type) et son Message-ID (rfc_message_id). Remplace une fonction : additive pour les lecteurs.
func init() {
	Register(Spec{Number: 77, Lot: "L1c", Name: "webhook_message_recu", Workspace: []string{dedupsql.WebhookTriggerFunction}})
}
