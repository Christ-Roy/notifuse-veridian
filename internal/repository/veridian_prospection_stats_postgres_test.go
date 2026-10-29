package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianProspectionStatsRepository_GetProspectionRaw(t *testing.T) {
	db, mock, cleanup := setupMockDB(t)
	defer cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil)

	since := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)FROM automations\s+WHERE deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status", "list_id", "root", "nodes"}).
			AddRow("seq1", "Sequence", "live", "list1", "split", []byte(`[{"id":"j0","type":"email","config":{},"next_node_id":"wait"},{"id":"wait","type":"delay","config":{"unit":"days","duration":4},"next_node_id":"j4"},{"id":"j4","type":"email","config":{}}]`)))
	// Seuls les contact_automations d'une sequence VIVANTE sont lus ; le dernier mail
	// mis en file vient des executions terminees.
	mock.ExpectQuery(`(?s)FROM contact_automations ca\s+JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL\s+LEFT JOIN LATERAL.*e.node_type = 'email' AND e.action = 'completed'`).
		WillReturnRows(sqlmock.NewRows([]string{"a", "status", "reason", "current", "last", "mail_left", "n"}).
			AddRow("seq1", "sending", "", "j0", "j0", nil, 5).
			AddRow("seq1", "exited", "replied", "", "j0", true, 2))
	// Reponses : fenetre [since, until[, humaines et automatiques separees.
	mock.ExpectQuery(`(?s)FROM veridian_contact_reply r\s+JOIN contact_automations ca ON lower\(ca.contact_email\) = r.contact_email.*r.replied_at >= \$1 AND r.replied_at < \$2`).
		WithArgs(since, until).
		WillReturnRows(sqlmock.NewRows([]string{"a", "human", "auto"}).AddRow("seq1", 2, 1))
	// Reponses humaines par etape : jointure au dernier mail email termine du contact dans la
	// sequence (INNER LATERAL : pas de mail parti, pas de ligne), meme fenetre [since, until[.
	mock.ExpectQuery(`(?s)FROM veridian_contact_reply r\s+JOIN contact_automations ca ON lower\(ca.contact_email\) = r.contact_email\s+JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL\s+JOIN LATERAL \(.*e.node_type = 'email' AND e.action = 'completed'.*\) le ON TRUE\s+WHERE r.reply_type = 'human' AND r.replied_at >= \$1 AND r.replied_at < \$2\s+GROUP BY 1, 2`).
		WithArgs(since, until).
		WillReturnRows(sqlmock.NewRows([]string{"a", "node", "human"}).AddRow("seq1", "j0", 1).AddRow("seq1", "j4", 1))
	// Envois de la fenetre : le transactionnel est ecarte.
	mock.ExpectQuery(`(?s)FROM message_history\s+WHERE automation_id IS NOT NULL AND sent_at >= \$1 AND sent_at < \$2 AND veridian_message_type IS DISTINCT FROM 'transactional' AND transactional_notification_id IS NULL`).
		WithArgs(since, until).
		WillReturnRows(sqlmock.NewRows([]string{"a", "n"}).AddRow("seq1", 40))
	mock.ExpectQuery(`(?s)NOT EXISTS.*FROM lists l\s+LEFT JOIN contact_lists cl ON cl.list_id = l.id AND cl.deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "active", "bounced", "unsub", "complained", "never"}).AddRow("list1", "Liste", 100, 3, 4, 0, 60))
	mock.ExpectQuery(`(?s)SELECT COUNT\(DISTINCT lower\(cl.email\)\)\s+FROM contact_lists cl`).
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(58))
	mock.ExpectQuery(`(?s)FROM veridian_contact_reply r\s+JOIN contact_lists cl ON cl.email = r.contact_email`).
		WithArgs(since, until).
		WillReturnRows(sqlmock.NewRows([]string{"l", "human", "auto"}).AddRow("list1", 2, 0))
	mock.ExpectQuery(`(?s)FROM message_history mh\s+JOIN contact_lists cl ON cl.email = mh.contact_email`).
		WithArgs(since, until).
		WillReturnRows(sqlmock.NewRows([]string{"l", "n"}).AddRow("list1", 40))
	// Dernieres reponses humaines : plus recentes d'abord, societe SIRENE et sequence.
	replied := time.Date(2026, 10, 8, 14, 30, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)FROM veridian_contact_reply r\s+LEFT JOIN contacts c ON lower\(c.email\) = r.contact_email\s+WHERE r.reply_type = 'human' AND r.replied_at >= \$1 AND r.replied_at < \$2\s+ORDER BY r.replied_at DESC\s+LIMIT 10`).
		WithArgs(since, until).
		WillReturnRows(sqlmock.NewRows([]string{"email", "company", "at", "seq"}).AddRow("a@x.fr", "ACME SARL", replied, "Sequence"))
	mock.ExpectRollback() // lecture seule : jamais de commit

	repo := NewVeridianProspectionStatsRepository(workspaceRepo)
	raw, err := repo.GetProspectionRaw(context.Background(), "ws1", since, until)
	require.NoError(t, err)
	require.Len(t, raw.Automations, 1)
	assert.Equal(t, "seq1", raw.Automations[0].ID)
	require.Len(t, raw.Automations[0].Nodes, 3)
	assert.Equal(t, domain.NodeTypeEmail, raw.Automations[0].Nodes[0].Type)
	require.Len(t, raw.Progress, 2)
	assert.Equal(t, "j0", raw.Progress[0].LastEmailNodeID)
	assert.Nil(t, raw.Progress[0].MailLeft, "non mesuré pour un contact non sorti")
	require.NotNil(t, raw.Progress[1].MailLeft)
	assert.True(t, *raw.Progress[1].MailLeft)
	assert.Equal(t, domain.VeridianProspectionReplyRow{Key: "seq1", Human: 2, Auto: 1}, raw.AutomationReplies[0])
	require.Len(t, raw.AutomationStepReplies, 2)
	assert.Equal(t, domain.VeridianProspectionStepReplyRow{Key: "seq1", NodeID: "j0", Human: 1}, raw.AutomationStepReplies[0])
	assert.Equal(t, domain.VeridianProspectionStepReplyRow{Key: "seq1", NodeID: "j4", Human: 1}, raw.AutomationStepReplies[1])
	assert.Equal(t, 40, raw.AutomationSent[0].Count)
	assert.Equal(t, 60, raw.Lists[0].NeverContacted)
	assert.Equal(t, "list1", raw.ListReplies[0].Key)
	assert.Equal(t, 40, raw.ListSent[0].Count)
	require.Len(t, raw.RecentReplies, 1)
	assert.Equal(t, domain.VeridianRecentReply{ContactEmail: "a@x.fr", Company: "ACME SARL", RepliedAt: replied, Sequence: "Sequence"}, raw.RecentReplies[0])
	assert.NoError(t, mock.ExpectationsWereMet())

	// Le resultat se transforme sans I/O supplementaire.
	stats := domain.VeridianBuildProspectionStats(raw, since, until, until)
	require.Len(t, stats.Sequences, 1)
	assert.Equal(t, 5, stats.Sequences[0].Stages[0].Queued)
	assert.Equal(t, 2, stats.Sequences[0].Exits.Replied)
	assert.Equal(t, 2, stats.Sequences[0].Stages[0].RepliesHuman, "cumul : J0 + J+4")
	assert.Equal(t, 1, stats.Sequences[0].Stages[1].RepliesHuman)
	assert.Equal(t, 2, stats.Sequences[0].Stages[0].ExitedReplied)
	require.Len(t, stats.RecentHumanReplies, 1)
	assert.Equal(t, "ACME SARL", stats.RecentHumanReplies[0].Company)
}

func TestVeridianProspectionBounds(t *testing.T) {
	from, to := veridianProspectionBounds(time.Time{}, time.Time{})
	assert.True(t, from.Before(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)))
	assert.True(t, to.After(time.Date(2050, 1, 1, 0, 0, 0, 0, time.UTC)))
	since := time.Date(2026, 9, 28, 2, 0, 0, 0, time.FixedZone("x", 7200))
	from, _ = veridianProspectionBounds(since, time.Time{})
	assert.Equal(t, time.UTC, from.Location())
	assert.Equal(t, 0, from.Hour())
}

func TestVeridianProspectionStatsRepository_Errors(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	t.Run("connexion impossible", func(t *testing.T) {
		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(nil, errors.New("no db"))
		_, err := NewVeridianProspectionStatsRepository(workspaceRepo).GetProspectionRaw(context.Background(), "ws1", time.Time{}, time.Time{})
		require.Error(t, err)
	})

	t.Run("une requete en echec fait echouer l'appel et annule la transaction", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()
		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil)
		mock.ExpectBegin()
		mock.ExpectQuery(`FROM automations`).WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		_, err := NewVeridianProspectionStatsRepository(workspaceRepo).GetProspectionRaw(context.Background(), "ws1", time.Time{}, time.Time{})
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("la requete des reponses par etape en echec fait echouer l'appel", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()
		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil)
		mock.ExpectBegin()
		mock.ExpectQuery(`FROM automations\s+WHERE`).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status", "list_id", "root", "nodes"}))
		mock.ExpectQuery(`FROM contact_automations ca`).WillReturnRows(sqlmock.NewRows([]string{"a", "status", "reason", "current", "last", "n"}))
		mock.ExpectQuery(`FROM veridian_contact_reply r\s+JOIN contact_automations ca ON lower\(ca.contact_email\) = r.contact_email\s+JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL\s+WHERE`).
			WillReturnRows(sqlmock.NewRows([]string{"a", "human", "auto"}))
		mock.ExpectQuery(`JOIN LATERAL`).WillReturnError(errors.New("boom"))
		mock.ExpectRollback()
		_, err := NewVeridianProspectionStatsRepository(workspaceRepo).GetProspectionRaw(context.Background(), "ws1", time.Time{}, time.Time{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "step replies")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("noeuds illisibles", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()
		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil)
		mock.ExpectBegin()
		mock.ExpectQuery(`FROM automations`).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status", "list_id", "root", "nodes"}).AddRow("a", "n", "live", "", "", []byte(`{pas du json`)))
		mock.ExpectRollback()
		_, err := NewVeridianProspectionStatsRepository(workspaceRepo).GetProspectionRaw(context.Background(), "ws1", time.Time{}, time.Time{})
		require.Error(t, err)
	})
}
