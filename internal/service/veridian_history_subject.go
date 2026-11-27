package service

import (
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// veridianHistorySubjectKey : clé de message_history.message_data.metadata qui porte l'objet réellement envoyé. Même
// nom que veridianMetadataSubjectKey du worker de file (internal/service/queue) : l'outbox de l'activité Mail
// (internal/veridian/activite, CONTRAT-HUB §5.25) la lit pour le champ subject de message.sent.
const veridianHistorySubjectKey = "veridian_subject"

// veridianRecordHistorySubject consigne l'objet rendu d'un envoi direct (transactionnel, API d'envoi) sur sa ligne
// d'historique. Mesuré le 27/11/2026 : message.sent d'un envoi transactionnel partait sans subject vers le Hub, et la
// ligne du CRM s'affichait « Untitled ». Copie la métadonnée de l'appelant (jamais de mutation de sa map), ne l'écrase pas.
func veridianRecordHistorySubject(message *domain.MessageHistory, subject string) {
	if message == nil || strings.TrimSpace(subject) == "" {
		return
	}
	md := make(map[string]interface{}, len(message.MessageData.Metadata)+1)
	for k, v := range message.MessageData.Metadata {
		md[k] = v
	}
	md[veridianHistorySubjectKey] = subject
	message.MessageData.Metadata = md
}
