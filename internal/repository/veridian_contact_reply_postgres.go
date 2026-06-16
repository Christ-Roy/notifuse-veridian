package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/Notifuse/notifuse/internal/domain"
)

// veridianContactReplyRepository implémente domain.VeridianContactReplyRepository :
// persistance du signal durable "ce contact a répondu" (stop-on-reply cold, Lot 3).
// Workspace-scoped : la table veridian_contact_reply vit dans la DB de chaque
// workspace (créée par la migration V51 / init.go), résolue via GetConnection.
type veridianContactReplyRepository struct {
	workspaceRepo domain.WorkspaceRepository
}

// NewVeridianContactReplyRepository crée le repo du signal stop-on-reply.
func NewVeridianContactReplyRepository(workspaceRepo domain.WorkspaceRepository) domain.VeridianContactReplyRepository {
	return &veridianContactReplyRepository{
		workspaceRepo: workspaceRepo,
	}
}

// MarkReplied pose (idempotemment) le signal 'replied'. ON CONFLICT (contact_email)
// DO NOTHING : le PREMIER signal gagne, un re-dispatch IMAP ne double rien. L'email
// est normalisé lowercase pour cohérence avec HasReplied et les autres tables.
func (r *veridianContactReplyRepository) MarkReplied(ctx context.Context, workspaceID string, reply *domain.VeridianContactReply) error {
	if reply == nil {
		return fmt.Errorf("reply is nil")
	}
	email := strings.ToLower(strings.TrimSpace(reply.ContactEmail))
	if email == "" {
		return fmt.Errorf("contact_email is required")
	}

	workspaceDB, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace connection: %w", err)
	}

	// matched_message_id nullable : NULL en fallback (pas d'envoi cité).
	var matchedMessageID sql.NullString
	if strings.TrimSpace(reply.MatchedMessageID) != "" {
		matchedMessageID = sql.NullString{String: reply.MatchedMessageID, Valid: true}
	}

	query := `
		INSERT INTO veridian_contact_reply (contact_email, replied_at, match_type, matched_message_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (contact_email) DO NOTHING
	`
	if _, err := workspaceDB.ExecContext(ctx, query,
		email,
		reply.RepliedAt,
		string(reply.MatchType),
		matchedMessageID,
	); err != nil {
		return fmt.Errorf("failed to mark contact replied: %w", err)
	}
	return nil
}

// HasReplied retourne true si le contact a déjà un signal 'replied'. Lookup PK
// (contact_email) → O(1). L'email est normalisé lowercase (le caller le fait aussi,
// défense en profondeur ici).
func (r *veridianContactReplyRepository) HasReplied(ctx context.Context, workspaceID, email string) (bool, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return false, nil
	}

	workspaceDB, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return false, fmt.Errorf("failed to get workspace connection: %w", err)
	}

	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM veridian_contact_reply WHERE contact_email = $1)`
	if err := workspaceDB.QueryRowContext(ctx, query, normalized).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to check contact replied: %w", err)
	}
	return exists, nil
}

// CountRepliedSince compte les contacts ayant répondu dans la fenêtre
// [since, until[. Bornes optionnelles : since.IsZero() = pas de borne basse,
// until.IsZero() = pas de borne haute. Sert le KPI reply rate du dashboard.
// COUNT(*) sur la table (1 ligne = 1 contact ayant répondu ≥1 fois) → renvoie
// donc le nombre de contacts uniques ayant répondu sur la période.
func (r *veridianContactReplyRepository) CountRepliedSince(
	ctx context.Context,
	workspaceID string,
	since, until time.Time,
) (int, error) {
	workspaceDB, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("failed to get workspace connection: %w", err)
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	sb := psql.Select("COUNT(*)").From("veridian_contact_reply")
	if !since.IsZero() {
		sb = sb.Where(sq.GtOrEq{"replied_at": since})
	}
	if !until.IsZero() {
		sb = sb.Where(sq.Lt{"replied_at": until})
	}

	query, args, err := sb.ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to build reply count query: %w", err)
	}

	var count int
	if err := workspaceDB.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count replied contacts: %w", err)
	}
	return count, nil
}
