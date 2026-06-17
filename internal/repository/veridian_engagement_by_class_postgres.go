package repository

// === Veridian patch ===
// Repository de l'engagement par classe de provider destinataire (KPI dashboard
// cold, ticket todo/2026-06-16-kpi-engagement-par-classe-provider.md).
//
// Agrège par DOMAINE destinataire en SQL (COUNT(*) FILTER, indexé sur
// created_at) ; la classification domaine → classe se fait en Go côté domaine
// (VeridianAggregateEngagementByClass) pour ne PAS dupliquer la table de
// domaines de veridian_provider_class.go en CASE SQL — même posture que le
// breakdown contacts R1 (veridian_contact_breakdown_postgres.go).

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

// GetEngagementByDomain agrège sent/delivered/bounced/opened/clicked par domaine
// destinataire sur la fenêtre [since, until[ (created_at). Une borne zero time
// est ignorée (pas de filtre de ce côté) — aligné sur le handler reply stats.
func (r *veridianEngagementByClassRepository) GetEngagementByDomain(
	ctx context.Context,
	workspaceID string,
	since, until time.Time,
) ([]domain.VeridianDomainEngagementRow, error) {
	db, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	sb := psql.
		Select(
			"lower(split_part(contact_email, '@', 2)) AS domain",
			"COUNT(*) FILTER (WHERE sent_at IS NOT NULL) AS sent",
			"COUNT(*) FILTER (WHERE delivered_at IS NOT NULL) AS delivered",
			"COUNT(*) FILTER (WHERE bounced_at IS NOT NULL) AS bounced",
			"COUNT(*) FILTER (WHERE opened_at IS NOT NULL) AS opened",
			"COUNT(*) FILTER (WHERE clicked_at IS NOT NULL) AS clicked",
		).
		From("message_history").
		GroupBy("1")

	if !since.IsZero() {
		sb = sb.Where(sq.GtOrEq{"created_at": since})
	}
	if !until.IsZero() {
		sb = sb.Where(sq.Lt{"created_at": until})
	}

	query, args, err := sb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build engagement query: %w", err)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute engagement query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []domain.VeridianDomainEngagementRow
	for rows.Next() {
		var row domain.VeridianDomainEngagementRow
		if err := rows.Scan(&row.Domain, &row.Sent, &row.Delivered, &row.Bounced, &row.Opened, &row.Clicked); err != nil {
			return nil, fmt.Errorf("failed to scan engagement row: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating engagement rows: %w", err)
	}

	return result, nil
}
