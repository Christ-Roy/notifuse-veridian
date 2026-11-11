package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork (fiche 78) : journal, idempotence, plafond et rejeu des copies de réponses de
// prospects redirigées par profil. Table WORKSPACE veridian_reply_forward (migrations V69 + V74).
// Le contenu d'une copie (Payload) n'est conservé que tant qu'elle n'est pas partie : il est
// effacé dès que la ligne est sent ou failed.

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

// replyForwardTx : ce que promote() demande à une transaction.
type replyForwardTx interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// promoteTx donne une place aux copies deferred d'un profil, dans l'ordre d'arrivée, tant que le
// plafond glissant de 24 h n'est pas atteint (les échecs comptent : c'est le fusible anti-boucle).
// Les copies promues passent en `retry` échues maintenant ; le worker de tâches (ou l'appelant) les
// prend à bail. Doit tourner sous le verrou consultatif du profil.
func promoteReplyForwardTx(ctx context.Context, tx replyForwardTx, profileID string, dailyCap int, now time.Time) ([]string, error) {
	var used int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM veridian_reply_forward WHERE profile_id = $1 AND status <> 'deferred' AND COALESCE(slot_at, created_at) >= $2`,
		profileID, now.Add(-24*time.Hour)).Scan(&used); err != nil {
		return nil, fmt.Errorf("count reply forward slots: %w", err)
	}
	free := dailyCap - used
	if free <= 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `
		UPDATE veridian_reply_forward SET status = 'retry', slot_at = $2, next_attempt_at = $2
		WHERE reply_key IN (
			SELECT reply_key FROM veridian_reply_forward
			WHERE profile_id = $1 AND status = 'deferred'
			ORDER BY created_at, reply_key LIMIT $3 FOR UPDATE)
		RETURNING reply_key`, profileID, now, free)
	if err != nil {
		return nil, fmt.Errorf("promote deferred reply forward: %w", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func lockReplyForwardProfile(ctx context.Context, tx replyForwardTx, profileID string) error {
	// Verrou consultatif par profil : deux réservations concurrentes ne dépassent pas le plafond.
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "veridian_reply_forward:"+profileID)
	return err
}

// Claim pose la copie (idempotent sur reply_key) en `deferred`, puis lui donne une place sous le plafond
// dans l'ordre d'arrivée. Si la place est pour elle, elle est prise à bail et la première tentative est
// à l'appelant ; sinon elle reste `deferred` et partira dès que le plafond le permet.
func (r *veridianReplyForwardRepository) Claim(ctx context.Context, workspaceID string, rec domain.VeridianReplyForwardRecord, dailyCap int, now time.Time) (domain.VeridianReplyForwardClaim, error) {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return domain.VeridianReplyForwardClaim{}, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.VeridianReplyForwardClaim{}, fmt.Errorf("claim reply forward begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockReplyForwardProfile(ctx, tx, rec.ProfileID); err != nil {
		return domain.VeridianReplyForwardClaim{}, fmt.Errorf("claim reply forward lock: %w", err)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO veridian_reply_forward (reply_key, profile_id, recipients, status, created_at, payload, attempts)
		VALUES ($1, $2, $3, 'deferred', $4, $5, 0)
		ON CONFLICT (reply_key) DO NOTHING`,
		rec.ReplyKey, rec.ProfileID, rec.Recipients, now, rec.Payload)
	if err != nil {
		return domain.VeridianReplyForwardClaim{}, fmt.Errorf("claim reply forward: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.VeridianReplyForwardClaim{}, fmt.Errorf("claim reply forward rows: %w", err)
	}
	if n == 0 {
		if err := tx.Commit(); err != nil {
			return domain.VeridianReplyForwardClaim{}, fmt.Errorf("claim reply forward commit: %w", err)
		}
		return domain.VeridianReplyForwardClaim{Duplicate: true}, nil
	}
	promoted, err := promoteReplyForwardTx(ctx, tx, rec.ProfileID, dailyCap, now)
	if err != nil {
		return domain.VeridianReplyForwardClaim{}, err
	}
	mine := false
	for _, k := range promoted {
		if k == rec.ReplyKey {
			mine = true
		}
	}
	out := domain.VeridianReplyForwardClaim{Deferred: !mine}
	if mine {
		var attempts int
		if err := tx.QueryRowContext(ctx, `
			UPDATE veridian_reply_forward SET status = 'pending', next_attempt_at = $2, attempts = COALESCE(attempts, 0) + 1
			WHERE reply_key = $1 AND status = 'retry' RETURNING attempts`,
			rec.ReplyKey, now.Add(domain.VeridianReplyForwardLease)).Scan(&attempts); err != nil {
			return domain.VeridianReplyForwardClaim{}, fmt.Errorf("lease reply forward: %w", err)
		}
		out.Leased, out.Attempts = true, attempts
	}
	if err := tx.Commit(); err != nil {
		return domain.VeridianReplyForwardClaim{}, fmt.Errorf("claim reply forward commit: %w", err)
	}
	return out, nil
}

// LeaseDue : voir l'interface. Les lignes sans contenu (créées avant V74, ou effacées) qui sont encore
// pending/retry depuis plus de 10 minutes sont closes en failed/no_payload : elles ne peuvent pas être
// rejouées, et restent visibles dans l'overview plutôt que d'attendre pour toujours.
func (r *veridianReplyForwardRepository) LeaseDue(ctx context.Context, workspaceID string, caps map[string]int, defaultCap int, now time.Time, limit int) ([]domain.VeridianReplyForwardJob, error) {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE veridian_reply_forward SET status = 'failed', error_class = 'no_payload', next_attempt_at = NULL
		WHERE status IN ('pending', 'retry') AND payload IS NULL AND created_at < $1`, now.Add(-10*time.Minute)); err != nil {
		return nil, fmt.Errorf("close reply forward without payload: %w", err)
	}
	prows, err := db.QueryContext(ctx, `SELECT DISTINCT profile_id FROM veridian_reply_forward WHERE status = 'deferred'`)
	if err != nil {
		return nil, fmt.Errorf("list deferred profiles: %w", err)
	}
	var profiles []string
	for prows.Next() {
		var p string
		if err := prows.Scan(&p); err != nil {
			_ = prows.Close()
			return nil, err
		}
		profiles = append(profiles, p)
	}
	if err := prows.Err(); err != nil {
		_ = prows.Close()
		return nil, err
	}
	_ = prows.Close()
	for _, p := range profiles {
		cap := defaultCap
		if c, ok := caps[p]; ok && c > 0 {
			cap = c
		}
		if err := r.promoteProfile(ctx, db, p, cap, now); err != nil {
			return nil, err
		}
	}
	rows, err := db.QueryContext(ctx, `
		UPDATE veridian_reply_forward SET status = 'pending', next_attempt_at = $2, attempts = COALESCE(attempts, 0) + 1
		WHERE reply_key IN (
			SELECT reply_key FROM veridian_reply_forward
			WHERE status IN ('pending', 'retry') AND payload IS NOT NULL AND next_attempt_at <= $1
			ORDER BY next_attempt_at, reply_key LIMIT $3 FOR UPDATE SKIP LOCKED)
		RETURNING reply_key, profile_id, payload, attempts`,
		now, now.Add(domain.VeridianReplyForwardLease), limit)
	if err != nil {
		return nil, fmt.Errorf("lease due reply forward: %w", err)
	}
	defer rows.Close()
	var jobs []domain.VeridianReplyForwardJob
	for rows.Next() {
		var j domain.VeridianReplyForwardJob
		if err := rows.Scan(&j.ReplyKey, &j.ProfileID, &j.Payload, &j.Attempts); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (r *veridianReplyForwardRepository) promoteProfile(ctx context.Context, db *sql.DB, profileID string, dailyCap int, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("promote reply forward begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockReplyForwardProfile(ctx, tx, profileID); err != nil {
		return fmt.Errorf("promote reply forward lock: %w", err)
	}
	if _, err := promoteReplyForwardTx(ctx, tx, profileID, dailyCap, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("promote reply forward commit: %w", err)
	}
	return nil
}

// Finish pose l'issue définitive (sent ou failed) et efface le contenu de la copie.
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
		`UPDATE veridian_reply_forward SET status = $2, error_class = NULLIF($3, ''), sent_at = $4, payload = NULL, next_attempt_at = NULL WHERE reply_key = $1`,
		replyKey, status, errorClass, sentAt); err != nil {
		return fmt.Errorf("finish reply forward: %w", err)
	}
	return nil
}

// Reschedule remet la copie en retry à l'échéance next.
func (r *veridianReplyForwardRepository) Reschedule(ctx context.Context, workspaceID, replyKey, errorClass string, next time.Time) error {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace connection: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE veridian_reply_forward SET status = 'retry', error_class = NULLIF($2, ''), next_attempt_at = $3 WHERE reply_key = $1`,
		replyKey, errorClass, next); err != nil {
		return fmt.Errorf("reschedule reply forward: %w", err)
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

// ProfileStats rend les compteurs d'overview : envoyées sur 24 h, en attente (pending, retry, deferred),
// échecs sur 7 jours, âge de la plus ancienne non envoyée et classe d'erreur dominante.
func (r *veridianReplyForwardRepository) ProfileStats(ctx context.Context, workspaceID string, now time.Time) (map[string]domain.VeridianReplyForwardProfileStats, error) {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT profile_id,
		  COUNT(*) FILTER (WHERE status = 'sent' AND sent_at >= $1),
		  COUNT(*) FILTER (WHERE status = 'pending'),
		  COUNT(*) FILTER (WHERE status = 'retry'),
		  COUNT(*) FILTER (WHERE status = 'deferred'),
		  COUNT(*) FILTER (WHERE status = 'failed' AND created_at >= $2),
		  MIN(created_at) FILTER (WHERE status IN ('pending', 'retry', 'deferred'))
		FROM veridian_reply_forward GROUP BY profile_id`, now.Add(-24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("reply forward profile stats: %w", err)
	}
	out := map[string]domain.VeridianReplyForwardProfileStats{}
	for rows.Next() {
		var s domain.VeridianReplyForwardProfileStats
		var oldest sql.NullTime
		if err := rows.Scan(&s.ProfileID, &s.Sent24h, &s.Pending, &s.Retry, &s.Deferred, &s.Failed7d, &oldest); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if oldest.Valid {
			t := oldest.Time.UTC()
			s.OldestUnsentAt = &t
		}
		out[s.ProfileID] = s
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	crows, err := db.QueryContext(ctx, `
		SELECT profile_id, error_class, COUNT(*) FROM veridian_reply_forward
		WHERE error_class IS NOT NULL AND ((status = 'failed' AND created_at >= $1) OR status IN ('pending', 'retry'))
		GROUP BY profile_id, error_class ORDER BY profile_id, COUNT(*) DESC, error_class`, now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("reply forward error classes: %w", err)
	}
	defer crows.Close()
	for crows.Next() {
		var p, class string
		var n int
		if err := crows.Scan(&p, &class, &n); err != nil {
			return nil, err
		}
		if s, ok := out[p]; ok && s.DominantErrorClass == "" { // première ligne par profil = la plus fréquente
			s.DominantErrorClass = class
			out[p] = s
		}
	}
	return out, crows.Err()
}
