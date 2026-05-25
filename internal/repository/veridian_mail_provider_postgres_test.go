package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SQL litteraux pour matcher avec QueryMatcherEqual (newMockSystemDB).

const mailProviderGetSQL = `SELECT mail_provider_choice FROM workspaces WHERE id = $1`
const mailProviderSetSQL = `UPDATE workspaces SET mail_provider_choice = $1 WHERE id = $2`

func TestNewVeridianMailProviderRepository_Constructor(t *testing.T) {
	db, _ := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)
	require.NotNil(t, repo)
	var _ domain.VeridianMailProviderRepository = repo
}

func TestGetMailProviderChoice_SMTPGeneric(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)

	mock.ExpectQuery(mailProviderGetSQL).
		WithArgs("ws-1").
		WillReturnRows(sqlmock.NewRows([]string{"mail_provider_choice"}).AddRow("smtp_generic"))

	got, err := repo.GetMailProviderChoice(ctx, "ws-1")
	require.NoError(t, err)
	assert.Equal(t, domain.MailProviderSMTPGeneric, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMailProviderChoice_HubGmail(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)

	mock.ExpectQuery(mailProviderGetSQL).
		WithArgs("ws-2").
		WillReturnRows(sqlmock.NewRows([]string{"mail_provider_choice"}).AddRow("hub_gmail"))

	got, err := repo.GetMailProviderChoice(ctx, "ws-2")
	require.NoError(t, err)
	assert.Equal(t, domain.MailProviderHubGmail, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMailProviderChoice_WorkspaceMissing_FallbackDefault(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)

	// sql.ErrNoRows → fallback safe sur smtp_generic, pas d'erreur propagee.
	mock.ExpectQuery(mailProviderGetSQL).
		WithArgs("missing-ws").
		WillReturnRows(sqlmock.NewRows([]string{"mail_provider_choice"})) // 0 rows

	got, err := repo.GetMailProviderChoice(ctx, "missing-ws")
	require.NoError(t, err, "workspace absent doit retomber sur defaut safe, pas planter")
	assert.Equal(t, domain.MailProviderSMTPGeneric, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMailProviderChoice_EmptyID_FallbackDefault(t *testing.T) {
	ctx := context.Background()
	db, _ := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)

	// Pas de roundtrip DB attendu sur empty string — guard cote repo.
	got, err := repo.GetMailProviderChoice(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, domain.MailProviderSMTPGeneric, got)
}

func TestGetMailProviderChoice_DBError(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)

	mock.ExpectQuery(mailProviderGetSQL).
		WithArgs("ws-3").
		WillReturnError(errors.New("connection refused"))

	_, err := repo.GetMailProviderChoice(ctx, "ws-3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get mail_provider_choice")
}

func TestSetMailProviderChoice_Success(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)

	mock.ExpectExec(mailProviderSetSQL).
		WithArgs("hub_gmail", "ws-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.SetMailProviderChoice(ctx, "ws-1", domain.MailProviderHubGmail)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetMailProviderChoice_BackToSMTPGeneric(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)

	// Re-set vers defaut (cas "deconnecter mon Gmail").
	mock.ExpectExec(mailProviderSetSQL).
		WithArgs("smtp_generic", "ws-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.SetMailProviderChoice(ctx, "ws-1", domain.MailProviderSMTPGeneric)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetMailProviderChoice_WorkspaceNotFound(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)

	mock.ExpectExec(mailProviderSetSQL).
		WithArgs("hub_gmail", "missing").
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 rows affected

	err := repo.SetMailProviderChoice(ctx, "missing", domain.MailProviderHubGmail)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorkspaceNotFoundForMailProvider)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetMailProviderChoice_EmptyWorkspaceID(t *testing.T) {
	ctx := context.Background()
	db, _ := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)

	err := repo.SetMailProviderChoice(ctx, "", domain.MailProviderHubGmail)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_id required")
}

func TestSetMailProviderChoice_DBError(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianMailProviderRepository(db)

	mock.ExpectExec(mailProviderSetSQL).
		WithArgs("hub_gmail", "ws-1").
		WillReturnError(errors.New("connection refused"))

	err := repo.SetMailProviderChoice(ctx, "ws-1", domain.MailProviderHubGmail)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "set mail_provider_choice")
}
