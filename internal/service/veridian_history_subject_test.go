package service

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

// L'objet rendu est consigné sur l'historique d'un envoi direct ; la métadonnée de l'appelant reste, sa map n'est pas mutée.
func TestVeridianRecordHistorySubject(t *testing.T) {
	appelant := map[string]interface{}{"commande": "A-7"}
	m := &domain.MessageHistory{MessageData: domain.MessageData{Metadata: appelant}}
	veridianRecordHistorySubject(m, "Votre reçu n° 42")
	assert.Equal(t, "Votre reçu n° 42", m.MessageData.Metadata[veridianHistorySubjectKey])
	assert.Equal(t, "A-7", m.MessageData.Metadata["commande"])
	_, mute := appelant[veridianHistorySubjectKey]
	assert.False(t, mute, "la map de l'appelant ne doit pas être modifiée")

	vide := &domain.MessageHistory{}
	veridianRecordHistorySubject(vide, "  ")
	assert.Nil(t, vide.MessageData.Metadata)
	// Même clé que le worker de file : l'outbox de l'activité lit les deux par le même nom.
	assert.Equal(t, "veridian_subject", veridianHistorySubjectKey)
}
