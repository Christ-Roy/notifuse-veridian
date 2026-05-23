package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/repository/testutil"
)

func TestCreateUser(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)

	// Test case 1: Successful user creation
	user := &domain.User{
		ID:    uuid.New().String(),
		Email: "test@example.com",
		Name:  "Test User",
		Type:  domain.UserTypeUser,
	}

	// === Veridian patch V46 === hub_user_id ajoute en queue de l'INSERT
	// (8e parametre). User sans HubUserID -> nil binding en DB.
	mock.ExpectExec(`INSERT INTO users \(id, email, name, type, language, created_at, updated_at, hub_user_id\) VALUES \(\$1, \$2, \$3, \$4, \$5, \$6, \$7, \$8\)`).
		WithArgs(user.ID, user.Email, user.Name, user.Type, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.CreateUser(context.Background(), user)
	require.NoError(t, err)

	// Test case 2: Error during user creation
	userWithError := &domain.User{
		ID:    uuid.New().String(),
		Email: "error@example.com",
		Name:  "Error User",
		Type:  domain.UserTypeUser,
	}

	mock.ExpectExec(`INSERT INTO users \(id, email, name, type, language, created_at, updated_at, hub_user_id\) VALUES \(\$1, \$2, \$3, \$4, \$5, \$6, \$7, \$8\)`).
		WithArgs(userWithError.ID, userWithError.Email, userWithError.Name, userWithError.Type, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), nil).
		WillReturnError(errors.New("database error"))

	err = repo.CreateUser(context.Background(), userWithError)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create user")

	// Test case 3: Duplicate key constraint violation
	duplicateUser := &domain.User{
		ID:    uuid.New().String(),
		Email: "duplicate@example.com",
		Name:  "Duplicate User",
		Type:  domain.UserTypeUser,
	}

	mock.ExpectExec(`INSERT INTO users \(id, email, name, type, language, created_at, updated_at, hub_user_id\) VALUES \(\$1, \$2, \$3, \$4, \$5, \$6, \$7, \$8\)`).
		WithArgs(duplicateUser.ID, duplicateUser.Email, duplicateUser.Name, duplicateUser.Type, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), nil).
		WillReturnError(errors.New("pq: duplicate key value violates unique constraint \"users_email_key\""))

	err = repo.CreateUser(context.Background(), duplicateUser)
	require.Error(t, err)
	assert.IsType(t, &domain.ErrUserExists{}, err)
	assert.Equal(t, "user already exists", err.Error())
}

// === Veridian patch V46 === Verifie que CreateUser persiste hub_user_id
// quand le pointer struct est set (cas AttachMember nouveau membre, sinon
// /api/user.me renvoie undefined apres SELECT car la colonne reste NULL).
// CONTRAT-HUB §3.7.
func TestCreateUser_PersistsHubUserID(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	hubUUID := "11111111-2222-3333-4444-555555555555"
	user := &domain.User{
		ID:        uuid.New().String(),
		Email:     "new-member@huid.test",
		Name:      "New Member",
		Type:      domain.UserTypeUser,
		HubUserID: &hubUUID,
	}

	mock.ExpectExec(`INSERT INTO users \(id, email, name, type, language, created_at, updated_at, hub_user_id\) VALUES \(\$1, \$2, \$3, \$4, \$5, \$6, \$7, \$8\)`).
		WithArgs(user.ID, user.Email, user.Name, user.Type, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), hubUUID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.CreateUser(context.Background(), user)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// === Veridian patch V46 === Defense en profondeur : un pointer non-nil
// vers une string vide ne doit PAS etre inserer comme "" (la colonne est
// UUID strict — un INSERT "" planterait). On stocke nil interface dans
// ce cas pour matcher la semantique "pas de binding Hub connu".
func TestCreateUser_EmptyHubUserID_StoredAsNull(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	empty := ""
	user := &domain.User{
		ID:        uuid.New().String(),
		Email:     "edge@huid.test",
		Type:      domain.UserTypeUser,
		HubUserID: &empty,
	}

	mock.ExpectExec(`INSERT INTO users \(id, email, name, type, language, created_at, updated_at, hub_user_id\) VALUES \(\$1, \$2, \$3, \$4, \$5, \$6, \$7, \$8\)`).
		WithArgs(user.ID, user.Email, user.Name, user.Type, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.CreateUser(context.Background(), user)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserByEmail(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)

	// Test case 1: User found
	email := "test@example.com"
	expectedUser := &domain.User{
		ID:        "user-id-1",
		Email:     email,
		Name:      "Test User",
		Type:      domain.UserTypeUser,
		Language:  "fr",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}

	// === Veridian patch V46 === colonne hub_user_id (CONTRAT-HUB §3.7)
	// ajoutee en queue du SELECT — fixture nil pour ce cas (user pre-V46
	// ou pas encore backfille).
	rows := sqlmock.NewRows([]string{"id", "email", "name", "type", "language", "created_at", "updated_at", "veridian_managed", "hub_user_id"}).
		AddRow(expectedUser.ID, expectedUser.Email, expectedUser.Name, expectedUser.Type, expectedUser.Language, expectedUser.CreatedAt, expectedUser.UpdatedAt, false, nil)

	mock.ExpectQuery(`SELECT id, email, name, type, language, created_at, updated_at, veridian_managed, hub_user_id FROM users WHERE email = \$1`).
		WithArgs(email).
		WillReturnRows(rows)

	user, err := repo.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.Equal(t, expectedUser.ID, user.ID)
	assert.Equal(t, expectedUser.Email, user.Email)
	assert.Equal(t, expectedUser.Name, user.Name)
	assert.Equal(t, expectedUser.Type, user.Type)
	assert.Equal(t, expectedUser.Language, user.Language)
	assert.Nil(t, user.HubUserID, "hub_user_id NULL en DB → nil sur le struct")

	// Test case 2: User not found
	mock.ExpectQuery(`SELECT id, email, name, type, language, created_at, updated_at, veridian_managed, hub_user_id FROM users WHERE email = \$1`).
		WithArgs("nonexistent@example.com").
		WillReturnError(sql.ErrNoRows)

	user, err = repo.GetUserByEmail(context.Background(), "nonexistent@example.com")
	require.Error(t, err)
	assert.Nil(t, user)
	assert.IsType(t, &domain.ErrUserNotFound{}, err)

	// Test case 3: Database error
	mock.ExpectQuery(`SELECT id, email, name, type, language, created_at, updated_at, veridian_managed, hub_user_id FROM users WHERE email = \$1`).
		WithArgs("error@example.com").
		WillReturnError(errors.New("database error"))

	user, err = repo.GetUserByEmail(context.Background(), "error@example.com")
	require.Error(t, err)
	assert.Nil(t, user)
	assert.Contains(t, err.Error(), "failed to get user")
}

func TestGetUserByID(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)

	// Test case 1: User found
	userID := "user-id-1"
	expectedUser := &domain.User{
		ID:        userID,
		Email:     "test@example.com",
		Name:      "Test User",
		Type:      domain.UserTypeUser,
		Language:  "fr",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}

	// === Veridian patch V46 === colonne hub_user_id (CONTRAT-HUB §3.7)
	// ajoutee en queue du SELECT — fixture nil pour ce cas.
	rows := sqlmock.NewRows([]string{"id", "email", "name", "type", "language", "created_at", "updated_at", "veridian_managed", "hub_user_id"}).
		AddRow(expectedUser.ID, expectedUser.Email, expectedUser.Name, expectedUser.Type, expectedUser.Language, expectedUser.CreatedAt, expectedUser.UpdatedAt, false, nil)

	mock.ExpectQuery(`SELECT id, email, name, type, language, created_at, updated_at, veridian_managed, hub_user_id FROM users WHERE id = \$1`).
		WithArgs(userID).
		WillReturnRows(rows)

	user, err := repo.GetUserByID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, expectedUser.ID, user.ID)
	assert.Equal(t, expectedUser.Email, user.Email)
	assert.Equal(t, expectedUser.Name, user.Name)
	assert.Equal(t, expectedUser.Type, user.Type)
	assert.Equal(t, expectedUser.Language, user.Language)
	assert.Nil(t, user.HubUserID, "hub_user_id NULL en DB → nil sur le struct")

	// Test case 2: User not found
	mock.ExpectQuery(`SELECT id, email, name, type, language, created_at, updated_at, veridian_managed, hub_user_id FROM users WHERE id = \$1`).
		WithArgs("nonexistent-id").
		WillReturnError(sql.ErrNoRows)

	user, err = repo.GetUserByID(context.Background(), "nonexistent-id")
	require.Error(t, err)
	assert.Nil(t, user)
	assert.IsType(t, &domain.ErrUserNotFound{}, err)
}

func TestUpdateUserLanguage(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)

	t.Run("success", func(t *testing.T) {
		mock.ExpectExec(`UPDATE users SET language = \$1, updated_at = \$2 WHERE id = \$3`).
			WithArgs("fr", sqlmock.AnyArg(), "user-id-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateUserLanguage(context.Background(), "user-id-1", "fr")
		require.NoError(t, err)
	})

	t.Run("user not found", func(t *testing.T) {
		mock.ExpectExec(`UPDATE users SET language = \$1, updated_at = \$2 WHERE id = \$3`).
			WithArgs("fr", sqlmock.AnyArg(), "missing-id").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateUserLanguage(context.Background(), "missing-id", "fr")
		require.Error(t, err)
		assert.IsType(t, &domain.ErrUserNotFound{}, err)
	})

	t.Run("database error", func(t *testing.T) {
		mock.ExpectExec(`UPDATE users SET language`).
			WithArgs("fr", sqlmock.AnyArg(), "user-id-1").
			WillReturnError(errors.New("database error"))

		err := repo.UpdateUserLanguage(context.Background(), "user-id-1", "fr")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update user language")
	})
}

func TestCreateSession(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)

	userID := "user-id-1"
	sessionID := uuid.New().String()
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	magicCode := "123456"
	magicCodeExpires := time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second)

	session := &domain.Session{
		ID:               sessionID,
		UserID:           userID,
		ExpiresAt:        expiresAt,
		MagicCode:        &magicCode,
		MagicCodeExpires: &magicCodeExpires,
	}

	// Use a more permissive regex pattern that allows for whitespace variations
	mock.ExpectExec(`INSERT INTO user_sessions.*VALUES.*\$1.*\$2.*\$3.*\$4.*\$5.*\$6`).
		WithArgs(sessionID, userID, expiresAt, sqlmock.AnyArg(), &magicCode, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.CreateSession(context.Background(), session)
	require.NoError(t, err)
}

func TestGetSessionByID(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)

	// Test case 1: Session found with magic code
	sessionID := "session-id-1"
	userID := "user-id-1"
	createdAt := time.Now().UTC().Truncate(time.Second)
	expiresAt := createdAt.Add(24 * time.Hour)
	magicCode := "123456"
	magicCodeExpires := createdAt.Add(15 * time.Minute)

	rows := sqlmock.NewRows([]string{"id", "user_id", "expires_at", "created_at", "magic_code", "magic_code_expires_at"}).
		AddRow(sessionID, userID, expiresAt, createdAt, magicCode, magicCodeExpires)

	mock.ExpectQuery(`SELECT id, user_id, expires_at, created_at, magic_code, magic_code_expires_at FROM user_sessions WHERE id = \$1`).
		WithArgs(sessionID).
		WillReturnRows(rows)

	session, err := repo.GetSessionByID(context.Background(), sessionID)
	require.NoError(t, err)
	assert.Equal(t, sessionID, session.ID)
	assert.Equal(t, userID, session.UserID)
	assert.Equal(t, expiresAt.Unix(), session.ExpiresAt.Unix())
	assert.Equal(t, createdAt.Unix(), session.CreatedAt.Unix())
	require.NotNil(t, session.MagicCode)
	assert.Equal(t, magicCode, *session.MagicCode)
	require.NotNil(t, session.MagicCodeExpires)
	assert.Equal(t, magicCodeExpires.Unix(), session.MagicCodeExpires.Unix())

	// Test case 2: Session found with NULL magic code (common after migration v15)
	sessionID2 := "session-id-2"
	rows2 := sqlmock.NewRows([]string{"id", "user_id", "expires_at", "created_at", "magic_code", "magic_code_expires_at"}).
		AddRow(sessionID2, userID, expiresAt, createdAt, nil, nil)

	mock.ExpectQuery(`SELECT id, user_id, expires_at, created_at, magic_code, magic_code_expires_at FROM user_sessions WHERE id = \$1`).
		WithArgs(sessionID2).
		WillReturnRows(rows2)

	session, err = repo.GetSessionByID(context.Background(), sessionID2)
	require.NoError(t, err)
	assert.Equal(t, sessionID2, session.ID)
	assert.Equal(t, userID, session.UserID)
	assert.Nil(t, session.MagicCode, "magic_code should be nil when NULL in database")
	assert.Nil(t, session.MagicCodeExpires, "magic_code_expires_at should be nil when NULL in database")

	// Test case 3: Session not found
	mock.ExpectQuery(`SELECT id, user_id, expires_at, created_at, magic_code, magic_code_expires_at FROM user_sessions WHERE id = \$1`).
		WithArgs("nonexistent-id").
		WillReturnError(sql.ErrNoRows)

	session, err = repo.GetSessionByID(context.Background(), "nonexistent-id")
	require.Error(t, err)
	assert.Nil(t, session)
	assert.IsType(t, &domain.ErrSessionNotFound{}, err)
}

func TestDeleteSession(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)

	// Test case 1: Session deleted successfully
	sessionID := "session-id-1"

	mock.ExpectExec(`DELETE FROM user_sessions WHERE id = \$1`).
		WithArgs(sessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.DeleteSession(context.Background(), sessionID)
	require.NoError(t, err)

	// Test case 2: Session not found
	mock.ExpectExec(`DELETE FROM user_sessions WHERE id = \$1`).
		WithArgs("nonexistent-id").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.DeleteSession(context.Background(), "nonexistent-id")
	require.Error(t, err)
	assert.IsType(t, &domain.ErrSessionNotFound{}, err)
}

func TestGetSessionsByUserID(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)

	userID := "user-id-1"
	now := time.Now().UTC().Truncate(time.Second)

	// Test case 1: Multiple sessions with magic codes
	magicCode1 := "123456"
	magicCodeExpires1 := now.Add(15 * time.Minute)
	magicCode2 := "654321"
	magicCodeExpires2 := now.Add(16 * time.Minute)

	rows := sqlmock.NewRows([]string{"id", "user_id", "expires_at", "created_at", "magic_code", "magic_code_expires_at"}).
		AddRow("session-id-1", userID, now.Add(24*time.Hour), now, magicCode1, magicCodeExpires1).
		AddRow("session-id-2", userID, now.Add(48*time.Hour), now.Add(1*time.Hour), magicCode2, magicCodeExpires2)

	mock.ExpectQuery(`SELECT id, user_id, expires_at, created_at, magic_code, magic_code_expires_at FROM user_sessions WHERE user_id = \$1 ORDER BY created_at DESC`).
		WithArgs(userID).
		WillReturnRows(rows)

	sessions, err := repo.GetSessionsByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
	assert.Equal(t, "session-id-1", sessions[0].ID)
	assert.Equal(t, "session-id-2", sessions[1].ID)
	require.NotNil(t, sessions[0].MagicCode)
	assert.Equal(t, magicCode1, *sessions[0].MagicCode)
	require.NotNil(t, sessions[1].MagicCode)
	assert.Equal(t, magicCode2, *sessions[1].MagicCode)

	// Test case 2: Sessions with NULL magic codes (common after migration v15)
	rows2 := sqlmock.NewRows([]string{"id", "user_id", "expires_at", "created_at", "magic_code", "magic_code_expires_at"}).
		AddRow("session-id-3", userID, now.Add(24*time.Hour), now, nil, nil).
		AddRow("session-id-4", userID, now.Add(48*time.Hour), now.Add(1*time.Hour), nil, nil)

	mock.ExpectQuery(`SELECT id, user_id, expires_at, created_at, magic_code, magic_code_expires_at FROM user_sessions WHERE user_id = \$1 ORDER BY created_at DESC`).
		WithArgs(userID).
		WillReturnRows(rows2)

	sessions, err = repo.GetSessionsByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
	assert.Nil(t, sessions[0].MagicCode, "magic_code should be nil when NULL in database")
	assert.Nil(t, sessions[0].MagicCodeExpires, "magic_code_expires_at should be nil when NULL in database")
	assert.Nil(t, sessions[1].MagicCode)
	assert.Nil(t, sessions[1].MagicCodeExpires)
}

func TestUpdateSession(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)

	// Test case 1: Session updated successfully with magic code
	sessionID := "session-id-1"
	expiresAt := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	magicCode := "updated-code"
	magicCodeExpires := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)

	session := &domain.Session{
		ID:               sessionID,
		ExpiresAt:        expiresAt,
		MagicCode:        &magicCode,
		MagicCodeExpires: &magicCodeExpires,
	}

	mock.ExpectExec(`UPDATE user_sessions SET expires_at = \$1, magic_code = \$2, magic_code_expires_at = \$3 WHERE id = \$4`).
		WithArgs(expiresAt, &magicCode, &magicCodeExpires, sessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateSession(context.Background(), session)
	require.NoError(t, err)

	// Test case 2: Session updated with NULL magic code (clearing it)
	sessionID2 := "session-id-2"
	session2 := &domain.Session{
		ID:               sessionID2,
		ExpiresAt:        expiresAt,
		MagicCode:        nil,
		MagicCodeExpires: nil,
	}

	mock.ExpectExec(`UPDATE user_sessions SET expires_at = \$1, magic_code = \$2, magic_code_expires_at = \$3 WHERE id = \$4`).
		WithArgs(expiresAt, nil, nil, sessionID2).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.UpdateSession(context.Background(), session2)
	require.NoError(t, err)

	// Test case 3: Session not found
	mock.ExpectExec(`UPDATE user_sessions SET expires_at = \$1, magic_code = \$2, magic_code_expires_at = \$3 WHERE id = \$4`).
		WithArgs(expiresAt, &magicCode, &magicCodeExpires, "nonexistent-id").
		WillReturnResult(sqlmock.NewResult(0, 0))

	session.ID = "nonexistent-id"
	err = repo.UpdateSession(context.Background(), session)
	require.Error(t, err)
	assert.IsType(t, &domain.ErrSessionNotFound{}, err)
}

func TestDeleteAllSessionsByUserID(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)

	// Test case 1: Successfully delete multiple sessions
	userID := "user-id-1"

	mock.ExpectExec(`DELETE FROM user_sessions WHERE user_id = \$1`).
		WithArgs(userID).
		WillReturnResult(sqlmock.NewResult(0, 3)) // 3 sessions deleted

	err := repo.DeleteAllSessionsByUserID(context.Background(), userID)
	require.NoError(t, err)

	// Test case 2: Successfully delete one session
	mock.ExpectExec(`DELETE FROM user_sessions WHERE user_id = \$1`).
		WithArgs("user-id-2").
		WillReturnResult(sqlmock.NewResult(0, 1)) // 1 session deleted

	err = repo.DeleteAllSessionsByUserID(context.Background(), "user-id-2")
	require.NoError(t, err)

	// Test case 3: No sessions to delete (user already logged out or never logged in)
	mock.ExpectExec(`DELETE FROM user_sessions WHERE user_id = \$1`).
		WithArgs("user-id-no-sessions").
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 sessions deleted

	err = repo.DeleteAllSessionsByUserID(context.Background(), "user-id-no-sessions")
	require.NoError(t, err) // Should not return error when no sessions exist

	// Test case 4: Database error during deletion
	mock.ExpectExec(`DELETE FROM user_sessions WHERE user_id = \$1`).
		WithArgs("user-id-error").
		WillReturnError(errors.New("database connection error"))

	err = repo.DeleteAllSessionsByUserID(context.Background(), "user-id-error")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete sessions")

	// Test case 5: Error getting rows affected
	mock.ExpectExec(`DELETE FROM user_sessions WHERE user_id = \$1`).
		WithArgs("user-id-rows-error").
		WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected error")))

	err = repo.DeleteAllSessionsByUserID(context.Background(), "user-id-rows-error")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get rows affected")
}

func TestDeleteUser(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()

	repo := NewUserRepository(db)
	userID := "user-id-to-delete"

	// Test case 1: User deleted successfully
	// First, expect deletion of user's sessions
	mock.ExpectExec(`DELETE FROM user_sessions WHERE user_id = \$1`).
		WithArgs(userID).
		WillReturnResult(sqlmock.NewResult(0, 2)) // Assuming 2 sessions were deleted

	// Then, expect deletion of the user
	mock.ExpectExec(`DELETE FROM users WHERE id = \$1`).
		WithArgs(userID).
		WillReturnResult(sqlmock.NewResult(0, 1)) // 1 user deleted

	err := repo.Delete(context.Background(), userID)
	require.NoError(t, err)

	// Test case 2: User not found
	mock.ExpectExec(`DELETE FROM user_sessions WHERE user_id = \$1`).
		WithArgs("nonexistent-id").
		WillReturnResult(sqlmock.NewResult(0, 0)) // No sessions deleted

	mock.ExpectExec(`DELETE FROM users WHERE id = \$1`).
		WithArgs("nonexistent-id").
		WillReturnResult(sqlmock.NewResult(0, 0)) // No users deleted

	err = repo.Delete(context.Background(), "nonexistent-id")
	require.Error(t, err)
	assert.IsType(t, &domain.ErrUserNotFound{}, err)

	// Test case 3: Error deleting sessions
	mock.ExpectExec(`DELETE FROM user_sessions WHERE user_id = \$1`).
		WithArgs("error-id").
		WillReturnError(errors.New("database error"))

	err = repo.Delete(context.Background(), "error-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete user sessions")

	// Test case 4: Error deleting user
	mock.ExpectExec(`DELETE FROM user_sessions WHERE user_id = \$1`).
		WithArgs("user-error-id").
		WillReturnResult(sqlmock.NewResult(0, 1)) // Sessions deleted successfully

	mock.ExpectExec(`DELETE FROM users WHERE id = \$1`).
		WithArgs("user-error-id").
		WillReturnError(errors.New("database error"))

	err = repo.Delete(context.Background(), "user-error-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete user")
}

// === Veridian patch === Tests pour MarkVeridianManaged (ticket P1
// hide-veridian-api-key, cf todo/done/2026-05-18-hide-veridian-api-key.md).

func TestMarkVeridianManaged_Success(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	mock.ExpectExec(`UPDATE users SET veridian_managed = TRUE WHERE email = \$1`).
		WithArgs("veridian-api-ws-1@notifuse.test").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.MarkVeridianManaged(context.Background(), "veridian-api-ws-1@notifuse.test")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkVeridianManaged_NotFound(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	mock.ExpectExec(`UPDATE users SET veridian_managed = TRUE WHERE email = \$1`).
		WithArgs("ghost@example.com").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.MarkVeridianManaged(context.Background(), "ghost@example.com")
	require.Error(t, err)
	assert.IsType(t, &domain.ErrUserNotFound{}, err)
}

func TestMarkVeridianManaged_DBError(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	mock.ExpectExec(`UPDATE users SET veridian_managed = TRUE WHERE email = \$1`).
		WithArgs("err@example.com").
		WillReturnError(errors.New("connection refused"))

	err := repo.MarkVeridianManaged(context.Background(), "err@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mark veridian-managed")
}

// TestMarkVeridianManaged_Idempotent verifie qu'un re-call sur un user deja
// marque ne renvoie pas d'erreur (UPDATE matche 1 row, valeur identique).
func TestMarkVeridianManaged_Idempotent(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	// Re-call : 1 row matche, on retourne rows_affected=1 comme PostgreSQL
	// le fait meme si la valeur ne change pas (UPDATE = match, pas diff).
	mock.ExpectExec(`UPDATE users SET veridian_managed = TRUE WHERE email = \$1`).
		WithArgs("api@notifuse.test").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.MarkVeridianManaged(context.Background(), "api@notifuse.test")
	require.NoError(t, err, "re-call sur user deja marque doit etre no-op silencieux")
}

// === Veridian patch === Tests pour BackfillHubUserID (V46, CONTRAT-HUB §3.7).
// Le UPDATE est conditionnel sur (hub_user_id IS NULL OR hub_user_id = $1)
// pour ne JAMAIS ecraser un binding existant different. 0 rows affected =>
// disambigue user manquant vs mismatch via SELECT probe.

func TestUserRepository_BackfillHubUserID_NullColumn_SetsOK(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	mock.ExpectExec(`UPDATE users\s+SET hub_user_id = \$1, updated_at = NOW\(\)\s+WHERE id = \$2 AND \(hub_user_id IS NULL OR hub_user_id = \$1\)`).
		WithArgs("hub-uuid-1", "user-local-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.BackfillHubUserID(context.Background(), "user-local-1", "hub-uuid-1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepository_BackfillHubUserID_MatchingValue_NoOp(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	// Re-call avec MEME hub_user_id = UPDATE matche la ligne (clause `OR
	// hub_user_id = $1`) → rows=1 sans probe.
	mock.ExpectExec(`UPDATE users\s+SET hub_user_id = \$1, updated_at = NOW\(\)\s+WHERE id = \$2 AND \(hub_user_id IS NULL OR hub_user_id = \$1\)`).
		WithArgs("hub-uuid-1", "user-local-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.BackfillHubUserID(context.Background(), "user-local-1", "hub-uuid-1")
	require.NoError(t, err, "re-call avec meme hub_user_id = idempotent no-op")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepository_BackfillHubUserID_Mismatch_ReturnsSentinel(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	// User existe avec hub_user_id DIFFERENT → UPDATE ne matche pas (clause
	// WHERE refuse l'overwrite) → rows=0 → probe SELECT pour disambiguer.
	mock.ExpectExec(`UPDATE users\s+SET hub_user_id`).
		WithArgs("hub-uuid-NEW", "user-local-1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	probeRows := sqlmock.NewRows([]string{"hub_user_id"}).AddRow("hub-uuid-OLD")
	mock.ExpectQuery(`SELECT hub_user_id FROM users WHERE id = \$1`).
		WithArgs("user-local-1").
		WillReturnRows(probeRows)

	err := repo.BackfillHubUserID(context.Background(), "user-local-1", "hub-uuid-NEW")
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrHubUserIDMismatch),
		"doit etre attrapable via errors.Is(ErrHubUserIDMismatch): %v", err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepository_BackfillHubUserID_UserMissing_ReturnsErrUserNotFound(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	// User n'existe pas du tout → UPDATE rows=0 → probe SELECT → ErrNoRows
	// → ErrUserNotFound (PAS Mismatch).
	mock.ExpectExec(`UPDATE users\s+SET hub_user_id`).
		WithArgs("hub-uuid-1", "ghost-id").
		WillReturnResult(sqlmock.NewResult(0, 0))

	mock.ExpectQuery(`SELECT hub_user_id FROM users WHERE id = \$1`).
		WithArgs("ghost-id").
		WillReturnError(sql.ErrNoRows)

	err := repo.BackfillHubUserID(context.Background(), "ghost-id", "hub-uuid-1")
	require.Error(t, err)
	assert.IsType(t, &domain.ErrUserNotFound{}, err)
	assert.False(t, errors.Is(err, domain.ErrHubUserIDMismatch),
		"un user manquant n'est PAS un mismatch")
}

func TestUserRepository_BackfillHubUserID_EmptyInputs_Validation(t *testing.T) {
	db, _, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	err := repo.BackfillHubUserID(context.Background(), "", "hub-uuid-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty user_id")

	err = repo.BackfillHubUserID(context.Background(), "user-1", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty hub_user_id")
}

func TestUserRepository_BackfillHubUserID_DBError(t *testing.T) {
	db, mock, cleanup := testutil.SetupMockDB(t)
	defer cleanup()
	repo := NewUserRepository(db)

	mock.ExpectExec(`UPDATE users\s+SET hub_user_id`).
		WithArgs("hub-uuid-1", "user-1").
		WillReturnError(errors.New("connection refused"))

	err := repo.BackfillHubUserID(context.Background(), "user-1", "hub-uuid-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backfill hub_user_id")
}
