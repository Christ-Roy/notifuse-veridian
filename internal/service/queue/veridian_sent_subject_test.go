package queue

import (
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

func veridianEntree(source domain.EmailQueueSourceType, subject string) *domain.EmailQueueEntry {
	return &domain.EmailQueueEntry{SourceType: source, Payload: domain.EmailQueuePayload{Subject: subject}}
}

// Toutes les sources de la file consignent l'objet envoyé (27/11 : message.sent partait sans subject vers le Hub, ligne
// « Untitled » au CRM). Avant, seules les automations l'avaient.
func TestVeridianRecordSentSubject_ToutesLesSources(t *testing.T) {
	for _, src := range []domain.EmailQueueSourceType{domain.EmailQueueSourceAutomation, domain.EmailQueueSourceBroadcast, domain.EmailQueueSourceMail} {
		m := &domain.MessageHistory{}
		veridianRecordSentSubject(m, veridianEntree(src, "Votre devis n° 42"), nil)
		assert.Equal(t, "Votre devis n° 42", m.MessageData.Metadata[veridianMetadataSubjectKey], string(src))
	}
}

// Fusion, jamais écrasement ; rien sur un envoi en échec ni sans objet.
func TestVeridianRecordSentSubject_FusionEtCasVides(t *testing.T) {
	m := &domain.MessageHistory{MessageData: domain.MessageData{Metadata: map[string]interface{}{"commande": "A-7"}}}
	veridianRecordSentSubject(m, veridianEntree(domain.EmailQueueSourceMail, "Reçu"), nil)
	assert.Equal(t, "A-7", m.MessageData.Metadata["commande"])
	assert.Equal(t, "Reçu", m.MessageData.Metadata[veridianMetadataSubjectKey])

	echec := &domain.MessageHistory{}
	veridianRecordSentSubject(echec, veridianEntree(domain.EmailQueueSourceBroadcast, "X"), errors.New("smtp 451"))
	assert.Nil(t, echec.MessageData.Metadata)

	vide := &domain.MessageHistory{}
	veridianRecordSentSubject(vide, veridianEntree(domain.EmailQueueSourceBroadcast, "   "), nil)
	assert.Nil(t, vide.MessageData.Metadata)
}
