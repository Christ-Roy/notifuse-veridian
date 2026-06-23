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

// GetProviderClassCounts retourne le nombre de contacts par (domaine d'email,
// custom_string_5), PRÉ-AGRÉGÉ en SQL (GROUP BY). Restreint à la liste listID si
// non vide (EXISTS ... deleted_at IS NULL, aligné sur GetContacts).
//
// Anti-OOM : l'ancienne version faisait un SELECT SANS LIMIT et streamait TOUS
// les contacts (millions sur un workspace cold) dans un slice Go. Ici le GROUP BY
// réduit ça à K (domaine,tag) distincts (milliers) côté serveur — la classe ne
// dépendant QUE du domaine et du tag, l'agrégat est strictement équivalent.
// On groupe par le DOMAINE de l'email (lower(split_part(email,'@',2))), donc la
// classification Go (VeridianClassifyDomainProviderClass) reçoit exactement le
// même domaine qu'elle aurait dérivé de l'email — zéro CASE SQL, table de
// domaines non dupliquée.
func (r *veridianContactBreakdownRepository) GetProviderClassCounts(
	ctx context.Context,
	workspaceID, listID string,
) ([]domain.VeridianContactProviderCount, error) {
	db, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	sb := psql.
		Select(
			"lower(split_part(c.email, '@', 2)) AS domain",
			"c.custom_string_5",
			"COUNT(*) AS cnt",
		).
		From("contacts c").
		GroupBy("lower(split_part(c.email, '@', 2))", "c.custom_string_5")

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

	var result []domain.VeridianContactProviderCount
	for rows.Next() {
		var domainStr string
		var customString5 sql.NullString
		var count int
		if err := rows.Scan(&domainStr, &customString5, &count); err != nil {
			return nil, fmt.Errorf("failed to scan provider count: %w", err)
		}
		c := domain.VeridianContactProviderCount{Domain: domainStr, Count: count}
		if customString5.Valid {
			c.CustomString5 = &domain.NullableString{String: customString5.String, IsNull: false}
		}
		result = append(result, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating provider counts: %w", err)
	}

	return result, nil
}
