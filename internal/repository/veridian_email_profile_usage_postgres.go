package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

type veridianEmailProfileUsageRepository struct{ workspaceRepo domain.WorkspaceRepository }

func NewVeridianEmailProfileUsageRepository(repo domain.WorkspaceRepository) domain.VeridianEmailProfileUsageRepository {
	return &veridianEmailProfileUsageRepository{workspaceRepo: repo}
}

func (r *veridianEmailProfileUsageRepository) GetEmailProfileUsage(ctx context.Context, workspaceID string, since time.Time) ([]domain.VeridianEmailProfileUsageRow, error) {
	db, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	// The profile counter is quota authority. Accepted history remains a separate
	// provider-class breakdown because an ambiguous post-DATA outcome deliberately
	// consumes capacity without being marked successful. Policy days are UTC in
	// both this query and the worker's reservation key.
	rows, err := db.QueryContext(ctx, `
		SELECT sender_domain AS profile_id, '' AS provider_class, used AS reserved_used, 0 AS accepted_used
		FROM veridian_daily_quota_counters
		WHERE workspace_id = $2 AND quota_kind = 'profile'
		  AND quota_day = ($1::timestamptz AT TIME ZONE 'UTC')::date
		UNION ALL
		SELECT veridian_profile_id AS profile_id,
		       COALESCE(veridian_provider_class, 'unclassified') AS provider_class,
		       0 AS reserved_used,
		       COUNT(*) AS accepted_used
		FROM message_history
		WHERE sent_at >= $1 AND sent_at < $1 + INTERVAL '1 day'
		  AND failed_at IS NULL AND veridian_profile_id IS NOT NULL
		GROUP BY veridian_profile_id, COALESCE(veridian_provider_class, 'unclassified')
		ORDER BY profile_id, provider_class
	`, since.UTC(), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query email profile usage: %w", err)
	}
	defer rows.Close()
	result := []domain.VeridianEmailProfileUsageRow{}
	for rows.Next() {
		var row domain.VeridianEmailProfileUsageRow
		if err := rows.Scan(&row.ProfileID, &row.ProviderClass, &row.ReservedUsed, &row.AcceptedUsed); err != nil {
			return nil, fmt.Errorf("scan email profile usage: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate email profile usage: %w", err)
	}
	return result, nil
}
