package domain

import (
	"context"
	"time"
)

// Veridian fork (fiche 62, lot 1) : contacts PARQUES en envoi (status 'sending') dont
// l'entree de file a disparu. Etat incoherent, jamais voulu : le contact attend un
// callback qui ne viendra plus. Cause mesuree (89 contacts de ecomdevenir, 29/09) :
// la garde finale du worker supprimait la ligne de file d'une automation en pause sans
// prevenir l'executeur. La classe est supprimee a la source (le worker notifie ou
// reporte) ; la reconciliation ci-dessous remet en etat coherent ce qui reste.

// VeridianParkedContact decrit un orphelin et ce que l'on sait de son dernier mail.
type VeridianParkedContact struct {
	ContactAutomationID string
	AutomationID        string
	ContactEmail        string
	NodeID              string
	// MessageID : identifiant du mail mis en file par le nœud (sortie « queued »).
	MessageID string
	// Etat de ce mail dans message_history.
	MessageFound  bool
	MessageSent   bool
	MessageFailed bool
	// AlreadyContacted : ce contact a deja recu un mail d'une AUTRE source (broadcast ou
	// autre automation). Interdit de lui renvoyer un « premier mail ».
	AlreadyContacted bool
}

// VeridianOrphanParkedRepository liste les orphelins d'un workspace. Interface
// OPTIONNELLE du depot d'automations (l'executeur la detecte par assertion de type).
type VeridianOrphanParkedRepository interface {
	ListOrphanParked(ctx context.Context, workspaceID string, grace time.Duration, limit int) ([]VeridianParkedContact, error)
}
