package smtprelay

import (
	"github.com/Notifuse/notifuse/internal/domain"
)

// BuildRequest compose la requête d'envoi d'une soumission (même recomposition que le relais), pour les autres portes SMTP
// de Notifuse (boîtes louées, internal/veridian/boxsend).
func BuildRequest(workspaceID, profileID string, provider *domain.EmailProvider, sub *Submission, rcpt, wireID string) domain.SendEmailProviderRequest {
	return buildRequest(workspaceID, profileID, provider, sub, rcpt, wireID)
}

// WireLocalPart rend l'identifiant d'envoi tiré d'un Message-ID client (partie locale).
func WireLocalPart(messageID string) string { return wireLocalPart(messageID) }
