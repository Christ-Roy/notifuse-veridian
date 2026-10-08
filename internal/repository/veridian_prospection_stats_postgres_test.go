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
		WillReturnRows(sqlmock.NewRows([]string{"a", "status", "reason", "current", "last", "n"}).
			AddRow("seq1", "sending", "", "j0", "j0", 5).
			AddRow("seq1", "exited", "replied", "", "j0", 2))
	// Reponses : fenetre [since, until[, humaines et automatiques separees.
	mock.ExpectQuery(`(?s)FROM veridian_contact_reply r\s+JOIN contact_automations ca ON lower\(ca.contact_email\) = r.contact_email.*r.replied_at >= \$1 AND r.replied_at < \$2`).
		WithArgs(since, until).
		WillReturnRows(sqlmock.NewRows([]string{"a", "human", "auto"}).AddRow("seq1", 2, 1))
	// Envois de la fenetre : le transactionnel est ecarte.
	mock.ExpectQuery(`(?s)FROM message_history\s+WHERE automation_id IS NOT NULL AND sent_at >= \$1 AND sent_at < \$2 AND veridian_message_type IS DISTINCT FROM 'transactional' AND transactional_notification_id IS NULL`).
		WithArgs(since, until).
		WillReturnRows(sqlmock.NewRows([]string{"a", "n"}).AddRow("seq1", 40))
	mock.ExpectQuery(`(?s)NOT EXISTS.*FROM lists l\s+LEFT JOIN contact_lists cl ON cl.list_id = l.id AND cl.deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "active", "bounced", "unsub", "complained", "never"}).AddRow("list1", "Liste", 100, 3, 4, 0, 60))
	mock.ExpectQuery(`(?s)FROM veridian_contact_reply r\s+JOIN contact_lists cl ON cl.email = r.contact_email`).
		WithArgs(since, until).
		WillReturnRows(sqlmock.NewRows([]string{"l", "human", "auto"}).AddRow("list1", 2, 0))
	mock.ExpectQuery(`(?s)FROM message_history mh\s+JOIN contact_lists cl ON cl.email = mh.contact_email`).
		WithArgs(since, until).
		WillReturnRows(sqlmock.NewRows([]string{"l", "n"}).AddRow("list1", 40))
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
	assert.Equal(t, domain.VeridianProspectionReplyRow{Key: "seq1", Human: 2, Auto: 1}, raw.AutomationReplies[0])
	assert.Equal(t, 40, raw.AutomationSent[0].Count)
	assert.Equal(t, 60, raw.Lists[0].NeverContacted)
	assert.Equal(t, "list1", raw.ListReplies[0].Key)
	assert.Equal(t, 40, raw.ListSent[0].Count)
	assert.NoError(t, mock.ExpectationsWereMet())

	// Le resultat se transforme sans I/O supplementaire.
	stats := domain.VeridianBuildProspectionStats(raw, since, until, until)
	require.Len(t, stats.Sequences, 1)
	assert.Equal(t, 5, stats.Sequences[0].Stages[0].Queued)
	assert.Equal(t, 2, stats.Sequences[0].Exits.Replied)
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
