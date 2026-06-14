package repository

// === Veridian patch ===
// Repository du breakdown contacts par classe de provider (R1, ticket
// todo/2026-06-14-tunnel-vente-ui-controle-et-roadmap.md).
//
// Projette uniquement (email, custom_string_5) — la classification se fait en
// Go côté domaine (VeridianAggregateProviderBreakdown) pour ne PAS dupliquer la
// table de domaines veridianProviderDomainTable en CASE SQL. Le filtrage par
// liste réutilise le même pattern EXISTS subquery que contactRepository.GetContacts
// (membre de la liste + hors soft-delete), pour rester cohérent avec le reste.

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/Notifuse/notifuse/internal/domain"
)

type veridianContactBreakdownRepository struct {
	workspaceRepo domain.WorkspaceRepository
}

// NewVeridianContactBreakdownRepository construit le repo de breakdown.
func NewVeridianContactBreakdownRepository(
	workspaceRepo domain.WorkspaceRepository,
) domain.VeridianContactProviderBreakdownRepository {
	return &veridianContactBreakdownRepository{
		workspaceRepo: workspaceRepo,
	}
}

// GetProviderClassRows retourne (email, custom_string_5) pour chaque contact du
// workspace, restreint à la liste listID si non vide. Les entrées de liste
// soft-deleted sont exclues (EXISTS ... deleted_at IS NULL), aligné sur le
// filtrage liste de GetContacts.
func (r *veridianContactBreakdownRepository) GetProviderClassRows(
	ctx context.Context,
	workspaceID, listID string,
) ([]domain.VeridianContactProviderRow, error) {
	db, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	sb := psql.Select("c.email", "c.custom_string_5").From("contacts c")

	if listID != "" {
		// EXISTS subquery : contact membre de la liste, hors soft-delete.
		// Même forme que contactRepository.GetContacts pour cohérence du plan.
		sb = sb.Where(sq.Expr(
			"EXISTS (SELECT 1 FROM contact_lists cl WHERE cl.email = c.email AND cl.deleted_at IS NULL AND cl.list_id = ?)",
			listID,
		))
	}

	query, args, err := sb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build breakdown query: %w", err)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute breakdown query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []domain.VeridianContactProviderRow
	for rows.Next() {
		var email string
		var customString5 sql.NullString
		if err := rows.Scan(&email, &customString5); err != nil {
			return nil, fmt.Errorf("failed to scan provider row: %w", err)
		}
		row := domain.VeridianContactProviderRow{Email: email}
		if customString5.Valid {
			row.CustomString5 = &domain.NullableString{String: customString5.String, IsNull: false}
		}
		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating provider rows: %w", err)
	}

	return result, nil
}
