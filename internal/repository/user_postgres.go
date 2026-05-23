package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opencensus.io/trace"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/tracing"
)

type userRepository struct {
	systemDB *sql.DB
}

// NewUserRepository creates a new PostgreSQL user repository
func NewUserRepository(db *sql.DB) domain.UserRepository {
	return &userRepository{systemDB: db}
}

func (r *userRepository) CreateUser(ctx context.Context, user *domain.User) error {
	if user.ID == "" {
		user.ID = uuid.New().String()
	}
	if user.Type == "" {
		user.Type = domain.UserTypeUser
	}
	// Coerce empty or unrecognized languages to the default so the column
	// always holds a supported UI locale.
	if !domain.IsSupportedUILanguage(user.Language) {
		user.Language = domain.DefaultLanguageCode
	}
	now := time.Now().UTC()
	user.CreatedAt = now
	user.UpdatedAt = now

	// === Veridian patch V46 === HubUserID inclus dans l'INSERT pour
	// persister le binding Hub-side cross-app (CONTRAT-HUB §3.7). Stocke
	// NULL si pointer nil (cas user upstream pre-V46 ou api_key sans
	// counterpart Hub). Sans ça, AttachMember crée bien un User avec
	// HubUserID en mémoire mais la colonne reste NULL en DB → user.me
	// renvoie undefined (omitempty cache le champ nil après SELECT).
	var hubUserID interface{}
	if user.HubUserID != nil && *user.HubUserID != "" {
		hubUserID = *user.HubUserID
	}

	query := `
		INSERT INTO users (id, email, name, type, language, created_at, updated_at, hub_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.systemDB.ExecContext(ctx, query,
		user.ID,
		user.Email,
		user.Name,
		user.Type,
		user.Language,
		user.CreatedAt,
		user.UpdatedAt,
		hubUserID,
	)
	if err != nil {
		// Check for duplicate key constraint violation (PostgreSQL error code 23505)
		if strings.Contains(err.Error(), "duplicate key value violates unique constraint") ||
			strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return &domain.ErrUserExists{Message: "user already exists"}
		}
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

func (r *userRepository) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var user domain.User
	// === Veridian patch === SELECT veridian_managed pour que le service
	// RemoveMember puisse refuser 403 sur les api_key gérées par le Hub.
	// SELECT hub_user_id (V46, CONTRAT-HUB §3.7) pour exposer le binding
	// Hub-side cross-app aux services qui en ont besoin (debug, audit).
	query := `
		SELECT id, email, name, type, language, created_at, updated_at, veridian_managed, hub_user_id
		FROM users
		WHERE email = $1
	`
	var hubUserID sql.NullString
	err := r.systemDB.QueryRowContext(ctx, query, email).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.Type,
		&user.Language,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.VeridianManaged,
		&hubUserID,
	)
	if err == sql.ErrNoRows {
		return nil, &domain.ErrUserNotFound{Message: "user not found"}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if hubUserID.Valid {
		v := hubUserID.String
		user.HubUserID = &v
	}
	return &user, nil
}

func (r *userRepository) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	ctx, span := tracing.StartServiceSpan(ctx, "UserRepository", "GetUserByID")
	defer span.End()

	span.AddAttributes(trace.StringAttribute("user.id", id))

	var user domain.User
	// === Veridian patch === SELECT veridian_managed pour que le service
	// RemoveMember puisse refuser 403 sur les api_key gérées par le Hub.
	// SELECT hub_user_id (V46, CONTRAT-HUB §3.7) pour exposer le binding
	// Hub-side cross-app aux services qui en ont besoin (debug, audit).
	query := `
		SELECT id, email, name, type, language, created_at, updated_at, veridian_managed, hub_user_id
		FROM users
		WHERE id = $1
	`
	startTime := time.Now()
	var hubUserID sql.NullString
	err := r.systemDB.QueryRowContext(ctx, query, id).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.Type,
		&user.Language,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.VeridianManaged,
		&hubUserID,
	)
	queryDuration := time.Since(startTime)

	// Add query duration to span
	span.AddAttributes(trace.StringAttribute("db.query", "SELECT FROM users"),
		trace.Int64Attribute("db.query_duration_ms", queryDuration.Milliseconds()))

	if err == sql.ErrNoRows {
		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeNotFound,
			Message: "user not found",
		})
		return nil, &domain.ErrUserNotFound{Message: "user not found"}
	}

	if err != nil {
		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeUnknown,
			Message: fmt.Sprintf("failed to get user: %s", err.Error()),
		})
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Add user email to span
	span.AddAttributes(trace.StringAttribute("user.email", user.Email))

	if hubUserID.Valid {
		v := hubUserID.String
		user.HubUserID = &v
	}

	return &user, nil
}

// UpdateUserLanguage updates a user's preferred language
func (r *userRepository) UpdateUserLanguage(ctx context.Context, userID string, language string) error {
	query := `UPDATE users SET language = $1, updated_at = $2 WHERE id = $3`
	result, err := r.systemDB.ExecContext(ctx, query, language, time.Now().UTC(), userID)
	if err != nil {
		return fmt.Errorf("failed to update user language: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return &domain.ErrUserNotFound{Message: "user not found"}
	}
	return nil
}

func (r *userRepository) CreateSession(ctx context.Context, session *domain.Session) error {
	if session.ID == "" {
		session.ID = uuid.New().String()
	}
	session.CreatedAt = time.Now().UTC()
	session.ExpiresAt = session.ExpiresAt.UTC()

	// Handle nullable magic code expiration
	var magicCodeExpires interface{}
	if session.MagicCodeExpires != nil {
		expiresUTC := session.MagicCodeExpires.UTC()
		magicCodeExpires = expiresUTC
	}

	query := `
		INSERT INTO user_sessions (
			id, user_id, expires_at, created_at, 
			magic_code, magic_code_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := r.systemDB.ExecContext(ctx, query,
		session.ID,
		session.UserID,
		session.ExpiresAt,
		session.CreatedAt,
		session.MagicCode,
		magicCodeExpires,
	)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

func (r *userRepository) GetSessionByID(ctx context.Context, id string) (*domain.Session, error) {
	var session domain.Session
	var magicCode sql.NullString
	var magicCodeExpires sql.NullTime

	query := `
		SELECT id, user_id, expires_at, created_at, 
			magic_code, magic_code_expires_at
		FROM user_sessions
		WHERE id = $1
	`
	err := r.systemDB.QueryRowContext(ctx, query, id).Scan(
		&session.ID,
		&session.UserID,
		&session.ExpiresAt,
		&session.CreatedAt,
		&magicCode,
		&magicCodeExpires,
	)
	if err == sql.ErrNoRows {
		return nil, &domain.ErrSessionNotFound{Message: "session not found"}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Convert nullable types to pointers
	if magicCode.Valid {
		session.MagicCode = &magicCode.String
	}
	if magicCodeExpires.Valid {
		session.MagicCodeExpires = &magicCodeExpires.Time
	}

	return &session, nil
}

func (r *userRepository) DeleteSession(ctx context.Context, id string) error {
	query := `DELETE FROM user_sessions WHERE id = $1`
	result, err := r.systemDB.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return &domain.ErrSessionNotFound{Message: "session not found"}
	}
	return nil
}

func (r *userRepository) DeleteAllSessionsByUserID(ctx context.Context, userID string) error {
	query := `DELETE FROM user_sessions WHERE user_id = $1`
	result, err := r.systemDB.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to delete sessions: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		// It's ok if no sessions exist - user might already be logged out
		return nil
	}
	return nil
}

func (r *userRepository) GetSessionsByUserID(ctx context.Context, userID string) ([]*domain.Session, error) {
	query := `
		SELECT id, user_id, expires_at, created_at, magic_code, magic_code_expires_at
		FROM user_sessions
		WHERE user_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.systemDB.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []*domain.Session
	for rows.Next() {
		var session domain.Session
		var magicCode sql.NullString
		var magicCodeExpires sql.NullTime

		err := rows.Scan(
			&session.ID,
			&session.UserID,
			&session.ExpiresAt,
			&session.CreatedAt,
			&magicCode,
			&magicCodeExpires,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}

		// Convert nullable types to pointers
		if magicCode.Valid {
			session.MagicCode = &magicCode.String
		}
		if magicCodeExpires.Valid {
			session.MagicCodeExpires = &magicCodeExpires.Time
		}

		sessions = append(sessions, &session)
	}
	return sessions, rows.Err()
}

func (r *userRepository) UpdateSession(ctx context.Context, session *domain.Session) error {
	query := `
		UPDATE user_sessions 
		SET expires_at = $1, 
			magic_code = $2, 
			magic_code_expires_at = $3
		WHERE id = $4
	`
	result, err := r.systemDB.ExecContext(
		ctx,
		query,
		session.ExpiresAt,
		session.MagicCode,
		session.MagicCodeExpires,
		session.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return &domain.ErrSessionNotFound{Message: "session not found"}
	}

	return nil
}

// Delete removes a user by their ID
func (r *userRepository) Delete(ctx context.Context, id string) error {
	// First delete all sessions for this user
	deleteSessionsQuery := `DELETE FROM user_sessions WHERE user_id = $1`
	_, err := r.systemDB.ExecContext(ctx, deleteSessionsQuery, id)
	if err != nil {
		return fmt.Errorf("failed to delete user sessions: %w", err)
	}

	// Then delete the user
	deleteUserQuery := `DELETE FROM users WHERE id = $1`
	result, err := r.systemDB.ExecContext(ctx, deleteUserQuery, id)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return &domain.ErrUserNotFound{Message: "user not found"}
	}

	return nil
}

// === Veridian patch === MarkVeridianManaged set veridian_managed=TRUE pour le
// user identifié par email. Idempotent. ErrUserNotFound si l'email n'existe pas.
// Appelé par VeridianService.Provision après CreateAPIKey pour verrouiller le
// user api_key contre la suppression UI.
func (r *userRepository) MarkVeridianManaged(ctx context.Context, email string) error {
	const q = `UPDATE users SET veridian_managed = TRUE WHERE email = $1`
	result, err := r.systemDB.ExecContext(ctx, q, email)
	if err != nil {
		return fmt.Errorf("mark veridian-managed: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark veridian-managed rows: %w", err)
	}
	if rows == 0 {
		return &domain.ErrUserNotFound{Message: "user not found"}
	}
	return nil
}

// === Veridian patch === BackfillHubUserID lie un user Notifuse local a son
// id source-of-truth Hub (CONTRAT-HUB §3.7, migration V46). Idempotent et
// non destructif :
//
//   - SET conditionne sur (hub_user_id IS NULL OR hub_user_id = $1) → un
//     re-call avec la meme valeur est no-op silencieux, mais on refuse
//     d'ecraser une valeur differente preexistante.
//   - 0 row affected → on disambigue : soit le user n'existe pas
//     (ErrUserNotFound), soit il existe avec un hub_user_id DIFFERENT
//     (ErrHubUserIDMismatch). Le caller est tenu de logger warn et
//     **continuer** : le lien Hub est informationnel, pas un blocker (§3.7).
//   - Met aussi updated_at = NOW() pour tracer la mutation cross-app.
//
// Concurrence : conditionnel WHERE clause = idempotent sous race
// (PostgreSQL serialise via row lock implicit du UPDATE).
func (r *userRepository) BackfillHubUserID(ctx context.Context, userID, hubUserID string) error {
	if userID == "" {
		return fmt.Errorf("backfill hub_user_id: empty user_id")
	}
	if hubUserID == "" {
		return fmt.Errorf("backfill hub_user_id: empty hub_user_id")
	}
	const q = `
		UPDATE users
		SET hub_user_id = $1, updated_at = NOW()
		WHERE id = $2 AND (hub_user_id IS NULL OR hub_user_id = $1)
	`
	result, err := r.systemDB.ExecContext(ctx, q, hubUserID, userID)
	if err != nil {
		return fmt.Errorf("backfill hub_user_id: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("backfill hub_user_id rows: %w", err)
	}
	if rows == 1 {
		return nil
	}
	// 0 rows → disambigue : user manquant vs mismatch.
	var existing sql.NullString
	probe := `SELECT hub_user_id FROM users WHERE id = $1`
	if err := r.systemDB.QueryRowContext(ctx, probe, userID).Scan(&existing); err != nil {
		if err == sql.ErrNoRows {
			return &domain.ErrUserNotFound{Message: "user not found"}
		}
		return fmt.Errorf("backfill hub_user_id probe: %w", err)
	}
	// User existe → c'est un mismatch (rows=0 + clause WHERE n'a pas matche).
	// Note : si existing.Valid == false on aurait du matcher (clause IS NULL)
	// donc on est forcement dans le cas existing.Valid && existing.String != $1.
	return fmt.Errorf("backfill hub_user_id: %w (existing=%q, requested=%q)",
		domain.ErrHubUserIDMismatch, existing.String, hubUserID)
}
