package repository

// === Veridian patch ===
// Repository de l'engagement par classe de provider destinataire (KPI dashboard
// cold).
//
// La classe est la colonne PERSISTEE message_history.veridian_provider_class (posee
// a l'envoi, MX resolu compris). L'ancienne version groupait par suffixe de
// domaine et rangeait presque tout en "corporate".
//
// Chaque compteur est borne sur SA date, jamais sur created_at (date de mise en
// file) : envois sur sent_at, rejets sur bounced_at, reponses humaines sur
// veridian_contact_reply.replied_at.

import (
	"context"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/Notifuse/notifuse/internal/domain"
)

type veridianEngagementByClassRepository struct {
	workspaceRepo domain.WorkspaceRepository
}

// NewVeridianEngagementByClassRepository construit le repo.
func NewVeridianEngagementByClassRepository(
	workspaceRepo domain.WorkspaceRepository,
) domain.VeridianEngagementByClassRepository {
	return &veridianEngagementByClassRepository{workspaceRepo: workspaceRepo}
}

// windowCond rend la condition "col dans [since, until[" (col non nul si pas de
// borne) et ses arguments, avec des placeholders `?`.
func windowCond(col string, since, until time.Time) (string, []interface{}) {
	cond := col + " IS NOT NULL"
	var args []interface{}
	if !since.IsZero() {
		cond += " AND " + col + " >= ?"
		args = append(args, since)
	}
	if !until.IsZero() {
		cond += " AND " + col + " < ?"
		args = append(args, until)
	}
	return cond, args
}

// GetEngagementByClass compte envois, rejets et reponses humaines par classe
// persistee sur la fenetre [since, until[. Une borne zero time est ignoree.
// Une classe NULL ressort avec Class == "".
func (r *veridianEngagementByClassRepository) GetEngagementByClass(
	ctx context.Context,
	workspaceID string,
	since, until time.Time,
) ([]domain.VeridianClassEngagementRow, error) {
	db, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

	sentCond, sentArgs := windowCond("sent_at", since, until)
	bouncedCond, bouncedArgs := windowCond("bounced_at", since, until)

	var whereArgs []interface{}
	whereArgs = append(whereArgs, sentArgs...)
	whereArgs = append(whereArgs, bouncedArgs...)

	query, args, err := psql.
		Select("COALESCE(veridian_provider_class, '') AS class").
		Column(sq.Expr("COUNT(*) FILTER (WHERE "+sentCond+") AS sent", sentArgs...)).
		Column(sq.Expr("COUNT(*) FILTER (WHERE "+bouncedCond+") AS bounced", bouncedArgs...)).
		From("message_history").
		Where(sq.Expr("(("+sentCond+") OR ("+bouncedCond+"))", whereArgs...)).
		GroupBy("1").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build engagement query: %w", err)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute engagement query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byClass := make(map[string]*domain.VeridianClassEngagementRow)
	var order []string
	get := func(class string) *domain.VeridianClassEngagementRow {
		if row, ok := byClass[class]; ok {
			return row
		}
		row := &domain.VeridianClassEngagementRow{Class: class}
		byClass[class] = row
		order = append(order, class)
		return row
	}

	for rows.Next() {
		var class string
		var sent, bounced int
		if err := rows.Scan(&class, &sent, &bounced); err != nil {
			return nil, fmt.Errorf("failed to scan engagement row: %w", err)
		}
		row := get(class)
		row.Sent += sent
		row.Bounced += bounced
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating engagement rows: %w", err)
	}

	// Reponses humaines : la classe est celle du dernier message envoye au contact.
	replyCond, replyArgs := windowCond("r.replied_at", since, until)
	replyQuery, replyQArgs, err := psql.
		Select("COALESCE(m.veridian_provider_class, '') AS class", "COUNT(*) AS replied_human").
		From("veridian_contact_reply r").
		JoinClause("LEFT JOIN LATERAL (SELECT veridian_provider_class FROM message_history WHERE contact_email = r.contact_email AND sent_at IS NOT NULL ORDER BY sent_at DESC LIMIT 1) m ON TRUE").
		Where(sq.Expr("r.reply_type = 'human' AND "+replyCond, replyArgs...)).
		GroupBy("1").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build reply query: %w", err)
	}

	replyRows, err := db.QueryContext(ctx, replyQuery, replyQArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute reply query: %w", err)
	}
	defer func() { _ = replyRows.Close() }()

	for replyRows.Next() {
		var class string
		var replied int
		if err := replyRows.Scan(&class, &replied); err != nil {
			return nil, fmt.Errorf("failed to scan reply row: %w", err)
		}
		get(class).RepliedHuman += replied
	}
	if err := replyRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating reply rows: %w", err)
	}

	var result []domain.VeridianClassEngagementRow
	for _, class := range order {
		result = append(result, *byClass[class])
	}
	return result, nil
}
