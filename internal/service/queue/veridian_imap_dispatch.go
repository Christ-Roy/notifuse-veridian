package queue

import (
	"github.com/Notifuse/notifuse/internal/domain"
)

// DispatchMessage remet un mail reçu hors IMAP (récepteur de courrier, internal/veridian/mxgate) aux mêmes consommateurs qu'un
// mail lu en IMAP : arrêt sur réponse, copies au client. Rend vrai si tous ont réussi. Les consommateurs sont idempotents.
func (s *VeridianIMAPPollerService) DispatchMessage(msg *domain.VeridianIMAPMessage) bool {
	if msg == nil {
		return false
	}
	return s.dispatch(s.snapshotConsumers(), msg, s.logger.WithFields(map[string]interface{}{
		"workspace_id": msg.WorkspaceID, "integration_id": msg.IntegrationID, "folder": msg.Folder,
	}))
}
