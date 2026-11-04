package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork (fiche 78, 04/11/2026) : journal et idempotence des copies de réponses de
// prospects redirigées par profil. Table WORKSPACE veridian_reply_forward (migration V63).
// Aucune adresse, aucun contenu : seulement de quoi dédoublonner, plafonner et compter.

type veridianReplyForwardRepository struct {
	workspaceRepo domain.WorkspaceRepository
	db            *sql.DB // tests
}

// NewVeridianReplyForwardRepository crée le dépôt du journal des copies de réponses.
func NewVeridianReplyForwardRepository(workspaceRepo domain.WorkspaceRepository) domain.VeridianReplyForwardRepository {
	return &veridianReplyForwardRepository{workspaceRepo: workspaceRepo}
}

// NewVeridianReplyForwardRepositoryWithDB crée le dépôt sur une connexion directe (tests).
func NewVeridianReplyForwardRepositoryWithDB(db *sql.DB) domain.VeridianReplyForwardRepository {
	return &veridianReplyForwardRepository{db: db}
}

func (r *veridianReplyForwardRepository) conn(ctx context.Context, workspaceID string) (*sql.DB, error) {
	if r.db != nil {
		return r.db, nil
	}
	return r.workspaceRepo.GetConnection(ctx, workspaceID)
}

// ResolveSentMessage rattache la réponse à un de NOS envois (profil + adresse d'envoi).
func (r *veridianReplyForwardRepository) ResolveSentMessage(ctx context.Context, workspaceID, matchedMessageID, contactEmail string) (domain.VeridianSentMessageRef, bool, error) {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return domain.VeridianSentMessageRef{}, false, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	if id := strings.TrimSpace(matchedMessageID); id != "" {
		var ref domain.VeridianSentMessageRef
		err := db.QueryRowContext(ctx,
			`SELECT COALESCE(veridian_profile_id, ''), COALESCE(veridian_sender_email, '') FROM message_history WHERE id = $1 OR id = $2 LIMIT 1`,
			id, domain.VeridianReconstructStoredMessageID(workspaceID, id),
		).Scan(&ref.ProfileID, &ref.SenderEmail)
		if err != nil && err != sql.ErrNoRows {
			return domain.VeridianSentMessageRef{}, false, fmt.Errorf("resolve sent message by id: %w", err)
		}
		if err == nil && ref.ProfileID != "" {
			return ref, true, nil
		}
	}
	contact := domain.VeridianNormalizeEmail(contactEmail)
	if contact == "" {
		return domain.VeridianSentMessageRef{}, false, nil
	}
	var ref domain.VeridianSentMessageRef
	err = db.QueryRowContext(ctx,
		`SELECT COALESCE(veridian_profile_id, ''), COALESCE(veridian_sender_email, '') FROM message_history
		 WHERE lower(contact_email) = $1 AND sent_at IS NOT NULL AND failed_at IS NULL AND veridian_profile_id IS NOT NULL
		 ORDER BY sent_at DESC LIMIT 1`,
		contact,
	).Scan(&ref.ProfileID, &ref.SenderEmail)
	if err == sql.ErrNoRows {
		return domain.VeridianSentMessageRef{}, false, nil
	}
	if err != nil {
		return domain.VeridianSentMessageRef{}, false, fmt.Errorf("resolve sent message by contact: %w", err)
	}
	return ref, ref.ProfileID != "", nil
}

// Claim réserve la copie : INSERT conditionnel (plafond) et idempotent (PK reply_key).
func (r *veridianReplyForwardRepository) Claim(ctx context.Context, workspaceID string, rec domain.VeridianReplyForwardRecord, dailyCap int, now time.Time) (bool, bool, bool, error) {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return false, false, false, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO veridian_reply_forward (reply_key, profile_id, recipients, status, created_at)
		SELECT $1::varchar, $2::varchar, $3::int, 'pending', $4::timestamptz
		WHERE (SELECT COUNT(*) FROM veridian_reply_forward WHERE profile_id = $2::varchar AND created_at >= $5::timestamptz) < $6::int
		ON CONFLICT (reply_key) DO NOTHING`,
		rec.ReplyKey, rec.ProfileID, rec.Recipients, now, now.Add(-24*time.Hour), dailyCap)
	if err != nil {
		return false, false, false, fmt.Errorf("claim reply forward: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, false, false, fmt.Errorf("claim reply forward rows: %w", err)
	}
	if n > 0 {
		return true, false, false, nil
	}
	var one int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM veridian_reply_forward WHERE reply_key = $1`, rec.ReplyKey).Scan(&one)
	if err == nil {
		return false, true, false, nil
	}
	if err == sql.ErrNoRows {
		return false, false, true, nil
	}
	return false, false, false, fmt.Errorf("claim reply forward lookup: %w", err)
}

// Finish pose le résultat de la réservation.
func (r *veridianReplyForwardRepository) Finish(ctx context.Context, workspaceID, replyKey, status, errorClass string, now time.Time) error {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace connection: %w", err)
	}
	var sentAt interface{}
	if status == domain.VeridianReplyForwardStatusSent {
		sentAt = now
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE veridian_reply_forward SET status = $2, error_class = NULLIF($3, ''), sent_at = $4 WHERE reply_key = $1`,
		replyKey, status, errorClass, sentAt); err != nil {
		return fmt.Errorf("finish reply forward: %w", err)
	}
	return nil
}

// Stats compte les copies par profil et statut depuis since.
func (r *veridianReplyForwardRepository) Stats(ctx context.Context, workspaceID string, since time.Time) ([]domain.VeridianReplyForwardStat, error) {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT profile_id, status, COUNT(*) FROM veridian_reply_forward WHERE created_at >= $1 GROUP BY profile_id, status ORDER BY profile_id, status`, since)
	if err != nil {
		return nil, fmt.Errorf("reply forward stats: %w", err)
	}
	defer rows.Close()
	var out []domain.VeridianReplyForwardStat
	for rows.Next() {
		var s domain.VeridianReplyForwardStat
		if err := rows.Scan(&s.ProfileID, &s.Status, &s.Count); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
