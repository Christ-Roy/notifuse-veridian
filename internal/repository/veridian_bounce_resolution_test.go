package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Constat 06/10/2026 : le NDR cite `uuid@domaine`, message_history.id vaut
// `robertbrunon_<uuid>`. Le rapprochement exact ne trouvait rien et bounced_at restait
// vide sur les 4 rejets reels (cotentin, gizeh, abh, dallmayr).
func TestMessageHistoryRepository_ResolveBounceTargetMessageID(t *testing.T) {
	ctx := context.Background()
	const workspaceID = "robertbrunon"
	const bare = "5b2f6c1e-1d0a-4c1b-9a77-0c8d2f4e9a10"
	const stored = workspaceID + "_" + bare

	t.Run("raw uuid@domain resolves to the workspace-prefixed id", func(t *testing.T) {
		mockWorkspaceRepo, repo, mock, db, cleanup := setupMessageHistoryTest(t)
		defer cleanup()

		mockWorkspaceRepo.EXPECT().GetConnection(gomock.Any(), workspaceID).Return(db, nil)
		mock.ExpectQuery(`SELECT id FROM message_history WHERE id = \$1 OR id = \$2`).
			WithArgs(bare, stored).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(stored))

		id, found, err := repo.ResolveBounceTargetMessageID(ctx, workspaceID, "<"+bare+"@agence-veridian.fr>", "cotentin@x.fr")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, stored, id)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty Message-ID falls back to the latest unbounced send to the recipient", func(t *testing.T) {
		mockWorkspaceRepo, repo, mock, db, cleanup := setupMessageHistoryTest(t)
		defer cleanup()

		mockWorkspaceRepo.EXPECT().GetConnection(gomock.Any(), workspaceID).Return(db, nil)
		// Pas de requete par id : Message-ID vide (cas dallmayr).
		mock.ExpectQuery(`SELECT id FROM message_history WHERE lower\(contact_email\) = \$1 AND bounced_at IS NULL AND sent_at IS NOT NULL ORDER BY sent_at DESC LIMIT 1`).
			WithArgs("dallmayr@x.fr").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(stored))

		id, found, err := repo.ResolveBounceTargetMessageID(ctx, workspaceID, "", " Dallmayr@X.fr ")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, stored, id)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unknown id then recipient fallback", func(t *testing.T) {
		mockWorkspaceRepo, repo, mock, db, cleanup := setupMessageHistoryTest(t)
		defer cleanup()

		mockWorkspaceRepo.EXPECT().GetConnection(gomock.Any(), workspaceID).Return(db, nil)
		mock.ExpectQuery(`SELECT id FROM message_history WHERE id = \$1 OR id = \$2`).
			WithArgs(bare, stored).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(`FROM message_history WHERE lower\(contact_email\) = \$1`).
			WithArgs("a@b.fr").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("other-id"))

		id, found, err := repo.ResolveBounceTargetMessageID(ctx, workspaceID, bare+"@d.fr", "a@b.fr")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "other-id", id)
	})

	t.Run("nothing matches: found=false, no error", func(t *testing.T) {
		mockWorkspaceRepo, repo, mock, db, cleanup := setupMessageHistoryTest(t)
		defer cleanup()

		mockWorkspaceRepo.EXPECT().GetConnection(gomock.Any(), workspaceID).Return(db, nil)
		mock.ExpectQuery(`SELECT id FROM message_history WHERE id = \$1 OR id = \$2`).
			WithArgs(bare, stored).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(`FROM message_history WHERE lower\(contact_email\)`).
			WillReturnError(sql.ErrNoRows)

		id, found, err := repo.ResolveBounceTargetMessageID(ctx, workspaceID, bare, "a@b.fr")
		require.NoError(t, err)
		assert.False(t, found)
		assert.Empty(t, id)
	})

	t.Run("no Message-ID and no recipient: no query beyond the connection", func(t *testing.T) {
		mockWorkspaceRepo, repo, mock, db, cleanup := setupMessageHistoryTest(t)
		defer cleanup()

		mockWorkspaceRepo.EXPECT().GetConnection(gomock.Any(), workspaceID).Return(db, nil)
		id, found, err := repo.ResolveBounceTargetMessageID(ctx, workspaceID, "", "")
		require.NoError(t, err)
		assert.False(t, found)
		assert.Empty(t, id)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error surfaced", func(t *testing.T) {
		mockWorkspaceRepo, repo, mock, db, cleanup := setupMessageHistoryTest(t)
		defer cleanup()

		mockWorkspaceRepo.EXPECT().GetConnection(gomock.Any(), workspaceID).Return(db, nil)
		mock.ExpectQuery(`SELECT id FROM message_history`).WillReturnError(errors.New("db down"))

		_, found, err := repo.ResolveBounceTargetMessageID(ctx, workspaceID, bare, "a@b.fr")
		require.Error(t, err)
		assert.False(t, found)
	})

	t.Run("connection error surfaced", func(t *testing.T) {
		mockWorkspaceRepo, repo, _, _, cleanup := setupMessageHistoryTest(t)
		defer cleanup()

		mockWorkspaceRepo.EXPECT().GetConnection(gomock.Any(), workspaceID).Return(nil, errors.New("no conn"))
		_, _, err := repo.ResolveBounceTargetMessageID(ctx, workspaceID, bare, "a@b.fr")
		require.Error(t, err)
	})
}

// 5.7.x : le refus de politique est inscrit sur l'envoi (bounce_type PolicyBounce) mais
// NE pose PAS bounced_at, donc le trigger message_history ne supprime pas le contact.
func TestMessageHistoryRepository_SetStatusesIfNotSet_PolicyRefusal(t *testing.T) {
	ctx := context.Background()
	const workspaceID = "ws1"

	mockWorkspaceRepo, repo, mock, db, cleanup := setupMessageHistoryTest(t)
	defer cleanup()

	mockWorkspaceRepo.EXPECT().GetConnection(gomock.Any(), workspaceID).Return(db, nil)
	mock.ExpectExec(`UPDATE message_history SET bounce_type = updates\.bounce_type, status_info = COALESCE\(LEFT\(updates\.status_info, 255\), message_history\.status_info\), updated_at = \$1::TIMESTAMP WITH TIME ZONE FROM \(VALUES \(\$2, \$3, \$4\)\) AS updates\(id, status_info, bounce_type\) WHERE message_history\.id = updates\.id AND message_history\.bounced_at IS NULL AND message_history\.bounce_type IS NULL`).
		WithArgs(sqlmock.AnyArg(), "msg-1", "policy 5.7.133", "PolicyBounce").
		WillReturnResult(sqlmock.NewResult(0, 1))

	reason := "policy 5.7.133"
	bounceType := domain.VeridianBounceTypePolicy
	err := repo.SetStatusesIfNotSet(ctx, workspaceID, []domain.MessageEventUpdate{{
		ID: "msg-1", Event: domain.MessageEventPolicyRefused, Timestamp: time.Now(),
		StatusInfo: &reason, BounceType: &bounceType,
	}})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
