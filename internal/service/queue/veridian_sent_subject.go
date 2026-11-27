package queue

import (
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// veridianRecordSentSubject consigne l'objet réellement envoyé dans message_history.message_data.metadata (clé
// veridianMetadataSubjectKey), pour TOUTES les sources d'envoi.
//
// Historique : depuis le 12/10/2026, l'objet n'était consigné que pour les automations (relances « Re: <objet> »).
// Le 27/11/2026, la preuve prod de l'activité Mail vers le Hub (CONTRAT-HUB §5.25) a montré que message.sent d'un envoi
// transactionnel partait sans subject : la ligne du CRM s'affichait « Untitled ». L'outbox (internal/veridian/activite)
// lit cette clé ; les campagnes et les mails de la boîte la reçoivent désormais aussi (le transactionnel direct :
// veridian_history_subject.go dans internal/service). Fusion, jamais écrasement : une
// métadonnée déjà posée sur le message reste.
func veridianRecordSentSubject(message *domain.MessageHistory, entry *domain.EmailQueueEntry, sendErr error) {
	if message == nil || entry == nil || sendErr != nil {
		return
	}
	subject := strings.TrimSpace(entry.Payload.Subject)
	if subject == "" {
		return
	}
	if message.MessageData.Metadata == nil {
		message.MessageData.Metadata = map[string]interface{}{}
	}
	message.MessageData.Metadata[veridianMetadataSubjectKey] = entry.Payload.Subject
}
