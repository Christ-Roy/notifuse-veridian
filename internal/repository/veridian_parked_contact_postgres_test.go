package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s lot1Seed) nodeExecQueued(t *testing.T, id, caID, node, messageID string, ago time.Duration) {
	_, err := s.db.Exec(`INSERT INTO automation_node_executions (id, contact_automation_id, automation_id, node_id, node_type, action, entered_at, output)
		VALUES ($1,$2,'autoA',$3,'email','completed',$4, jsonb_build_object('queued', true, 'message_id', $5::text))`,
		id, caID, node, time.Now().Add(-ago), messageID)
	require.NoError(t, err)
}

func (s lot1Seed) message(t *testing.T, id, email, automation string, sent, failed bool) {
	var a interface{}
	if automation != "" {
		a = automation
	}
	_, err := s.db.Exec(`INSERT INTO message_history (id, contact_email, automation_id, template_id, template_version, channel, message_data, sent_at, failed_at, created_at, updated_at)
		VALUES ($1,$2,$3,'tpl',1,'email','{}', CASE WHEN $4 THEN NOW() END, CASE WHEN $5 THEN NOW() END, NOW(), NOW())`,
		id, email, a, sent, failed)
	require.NoError(t, err)
}

func TestListOrphanParked_ClassifiesEachOrphanWithoutGuessing(t *testing.T) {
	db := lot1TestDB(t)
	s := lot1Seed{db}
	s.automation(t, "autoA", "Sequence A")
	s.automation(t, "autoB", "Autre")

	// 1. mail parti, callback perdu
	s.contactAutomation(t, "c1", "autoA", "sent@x.fr", "j0a", "sending", 3*time.Hour)
	s.nodeExecQueued(t, "n1", "c1", "j0a", "m-sent", 3*time.Hour)
	s.message(t, "m-sent", "sent@x.fr", "autoA", true, false)
	// 2. echec definitif, callback perdu
	s.contactAutomation(t, "c2", "autoA", "failed@x.fr", "j0a", "sending", 3*time.Hour)
	s.nodeExecQueued(t, "n2", "c2", "j0a", "m-failed", 3*time.Hour)
	s.message(t, "m-failed", "failed@x.fr", "autoA", false, true)
	// 3. ligne de file supprimee sans rien envoyer (cas des 89)
	s.contactAutomation(t, "c3", "autoA", "vanished@x.fr", "j0a", "sending", 3*time.Hour)
	s.nodeExecQueued(t, "n3", "c3", "j0a", "m-vanished", 3*time.Hour)
	// 4. idem mais deja contacte par une AUTRE source
	s.contactAutomation(t, "c4", "autoA", "known@x.fr", "j0a", "sending", 3*time.Hour)
	s.nodeExecQueued(t, "n4", "c4", "j0a", "m-known", 3*time.Hour)
	s.message(t, "older", "known@x.fr", "autoB", true, false)
	// 5. dans la grace : un envoi peut etre en train de se terminer
	s.contactAutomation(t, "c5", "autoA", "fresh@x.fr", "j0a", "sending", 3*time.Hour)
	s.nodeExecQueued(t, "n5", "c5", "j0a", "m-fresh", time.Minute)
	// 6. a encore une entree de file : pas un orphelin
	s.contactAutomation(t, "c6", "autoA", "queued@x.fr", "j0a", "sending", 3*time.Hour)
	s.nodeExecQueued(t, "n6", "c6", "j0a", "m-queued", 3*time.Hour)
	s.queue(t, lot1Queue{id: "q6", sourceID: "autoA", email: "queued@x.fr"})
	// 7. pas « sending » : hors sujet
	s.contactAutomation(t, "c7", "autoA", "active@x.fr", "j0a", "active", 3*time.Hour)
	// 8. message_history sans etat (ni envoye ni echoue)
	s.contactAutomation(t, "c8", "autoA", "odd@x.fr", "j0a", "sending", 3*time.Hour)
	s.nodeExecQueued(t, "n8", "c8", "j0a", "m-odd", 3*time.Hour)
	s.message(t, "m-odd", "odd@x.fr", "autoA", false, false)

	got, err := (&AutomationRepository{db: db}).ListOrphanParked(context.Background(), "ws", 15*time.Minute, 100)
	require.NoError(t, err)

	by := map[string]string{}
	for _, p := range got {
		by[p.ContactEmail] = p.ContactAutomationID
	}
	assert.Equal(t, map[string]string{
		"sent@x.fr": "c1", "failed@x.fr": "c2", "vanished@x.fr": "c3", "known@x.fr": "c4", "odd@x.fr": "c8",
	}, by, "ni la grace, ni l'entree de file, ni le statut actif ne produisent d'orphelin")

	for _, p := range got {
		switch p.ContactEmail {
		case "sent@x.fr":
			assert.True(t, p.MessageFound && p.MessageSent && !p.MessageFailed)
			assert.Equal(t, "m-sent", p.MessageID)
		case "failed@x.fr":
			assert.True(t, p.MessageFound && p.MessageFailed && !p.MessageSent)
		case "vanished@x.fr":
			assert.False(t, p.MessageFound)
			assert.False(t, p.AlreadyContacted)
			assert.Equal(t, "j0a", p.NodeID)
		case "known@x.fr":
			assert.False(t, p.MessageFound)
			assert.True(t, p.AlreadyContacted, "deja contacte par une autre source : jamais de « premier mail »")
		case "odd@x.fr":
			assert.True(t, p.MessageFound && !p.MessageSent && !p.MessageFailed)
		}
	}

	limited, err := (&AutomationRepository{db: db}).ListOrphanParked(context.Background(), "ws", 15*time.Minute, 2)
	require.NoError(t, err)
	assert.Len(t, limited, 2, "borne")
}

func TestListOrphanParked_ErrorOnClosedDB(t *testing.T) {
	db := lot1TestDB(t)
	repo := &AutomationRepository{db: db}
	require.NoError(t, db.Close())
	_, err := repo.ListOrphanParked(context.Background(), "ws", time.Minute, 1)
	assert.Error(t, err)
	var _ *sql.DB = db
}
