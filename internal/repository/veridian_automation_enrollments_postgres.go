package repository

// === Veridian — lecture des inscriptions d'automation (2026-10-27) ===
// Pagination par curseur keyset sur (entered_at DESC, id DESC) : la page suivante
// reprend exactement après la dernière ligne vue, sans doublon ni trou même si de
// nouveaux contacts s'inscrivent entre deux appels. Index utilisé :
// idx_contact_automations_automation (automation_id, status).

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/lib/pq"
)

type veridianAutomationEnrollmentRepository struct {
	workspaceRepo domain.WorkspaceRepository
	db            *sql.DB
}

// NewVeridianAutomationEnrollmentRepository lit les inscriptions via la base du workspace.
func NewVeridianAutomationEnrollmentRepository(workspaceRepo domain.WorkspaceRepository) domain.VeridianAutomationEnrollmentReader {
	return &veridianAutomationEnrollmentRepository{workspaceRepo: workspaceRepo}
}

// NewVeridianAutomationEnrollmentRepositoryWithDB fixe la connexion (tests).
func NewVeridianAutomationEnrollmentRepositoryWithDB(db *sql.DB) domain.VeridianAutomationEnrollmentReader {
	return &veridianAutomationEnrollmentRepository{db: db}
}

func (r *veridianAutomationEnrollmentRepository) conn(ctx context.Context, workspaceID string) (*sql.DB, error) {
	if r.db != nil {
		return r.db, nil
	}
	return r.workspaceRepo.GetConnection(ctx, workspaceID)
}

// ListEnrollmentsPage rend une page de (Limit) inscrits et le curseur de la suivante.
// Le contexte JSON de l'inscription n'est pas renvoyé (poids, et souvent inutile au tri).
func (r *veridianAutomationEnrollmentRepository) ListEnrollmentsPage(ctx context.Context, f domain.VeridianAutomationEnrollmentFilter, statuses []domain.ContactAutomationStatus) (*domain.VeridianEnrollmentPage, error) {
	db, err := r.conn(ctx, f.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}

	args := []interface{}{f.AutomationID}
	where := "automation_id = $1"
	if len(statuses) > 0 {
		names := make([]string, len(statuses))
		for i, s := range statuses {
			names[i] = string(s)
		}
		args = append(args, pq.Array(names))
		where += fmt.Sprintf(" AND status = ANY($%d)", len(args))
	}
	if f.Cursor != "" {
		at, id, err := domain.DecodeVeridianEnrollmentCursor(f.Cursor)
		if err != nil {
			return nil, err
		}
		args = append(args, at, id)
		where += fmt.Sprintf(" AND (entered_at, id) < ($%d, $%d)", len(args)-1, len(args))
	}
	// Une ligne de plus que la page : elle dit s'il existe une page suivante.
	args = append(args, f.Limit+1)
	query := `SELECT id, automation_id, contact_email, current_node_id, status, exit_reason,
		entered_at, scheduled_at, retry_count, last_error, last_retry_at, max_retries
		FROM contact_automations WHERE ` + where +
		fmt.Sprintf(" ORDER BY entered_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list automation enrollments: %w", err)
	}
	defer rows.Close()

	out := []*domain.ContactAutomation{}
	for rows.Next() {
		var ca domain.ContactAutomation
		var status string
		if err := rows.Scan(&ca.ID, &ca.AutomationID, &ca.ContactEmail, &ca.CurrentNodeID, &status,
			&ca.ExitReason, &ca.EnteredAt, &ca.ScheduledAt, &ca.RetryCount, &ca.LastError,
			&ca.LastRetryAt, &ca.MaxRetries); err != nil {
			return nil, fmt.Errorf("failed to scan automation enrollment: %w", err)
		}
		ca.Status = domain.ContactAutomationStatus(status)
		out = append(out, &ca)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	page := &domain.VeridianEnrollmentPage{Enrollments: out}
	if len(out) > f.Limit {
		page.Enrollments = out[:f.Limit]
		last := page.Enrollments[len(page.Enrollments)-1]
		page.NextCursor = domain.EncodeVeridianEnrollmentCursor(last.EnteredAt, last.ID)
	}
	return page, nil
}
