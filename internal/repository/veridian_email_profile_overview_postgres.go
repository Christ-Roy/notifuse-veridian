package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

type veridianEmailProfileOverviewRepository struct{ workspaceRepo domain.WorkspaceRepository }

func NewVeridianEmailProfileOverviewRepository(repo domain.WorkspaceRepository) domain.VeridianEmailProfileOverviewRepository {
	return &veridianEmailProfileOverviewRepository{workspaceRepo: repo}
}

// GetPlanObservations lit, en lecture seule, les deux sources que consulte le
// worker pour ses plafonds du jour : message_history (même filtre que les
// COUNT des portes : envois acceptés du jour de compte, failed_at IS NULL) et les
// compteurs atomiques de réservation de la date civile du jour de compte.
func (r *veridianEmailProfileOverviewRepository) GetPlanObservations(ctx context.Context, workspaceID string, day domain.VeridianDay) ([]domain.VeridianPlanObservationRow, []domain.VeridianPlanCounterRow, error) {
	db, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	histRows, err := db.QueryContext(ctx, `
		SELECT COALESCE(veridian_profile_id, ''), COALESCE(veridian_sender_email, ''),
		       COALESCE(veridian_provider_class, ''),
		       CASE WHEN veridian_message_type = 'transactional' OR transactional_notification_id IS NOT NULL
		            THEN 'transactional' ELSE '' END,
		       COUNT(*)
		FROM message_history
		WHERE sent_at >= $1 AND sent_at < $2 AND failed_at IS NULL
		GROUP BY 1, 2, 3, 4
	`, day.Start.UTC(), day.End.UTC())
	if err != nil {
		return nil, nil, fmt.Errorf("query plan observations: %w", err)
	}
	defer histRows.Close()
	rows := []domain.VeridianPlanObservationRow{}
	for histRows.Next() {
		var row domain.VeridianPlanObservationRow
		if err := histRows.Scan(&row.ProfileID, &row.SenderEmail, &row.ProviderClass, &row.MessageType, &row.Accepted); err != nil {
			return nil, nil, fmt.Errorf("scan plan observation: %w", err)
		}
		rows = append(rows, row)
	}
	if err := histRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate plan observations: %w", err)
	}

	counterRows, err := db.QueryContext(ctx, `
		SELECT quota_kind, sender_domain, provider_class, used
		FROM veridian_daily_quota_counters
		WHERE workspace_id = $2 AND quota_day = $1::date
	`, day.LabelDate(), workspaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("query plan quota counters: %w", err)
	}
	defer counterRows.Close()
	counters := []domain.VeridianPlanCounterRow{}
	for counterRows.Next() {
		var c domain.VeridianPlanCounterRow
		if err := counterRows.Scan(&c.Kind, &c.Scope, &c.ProviderClass, &c.Used); err != nil {
			return nil, nil, fmt.Errorf("scan plan quota counter: %w", err)
		}
		counters = append(counters, c)
	}
	if err := counterRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate plan quota counters: %w", err)
	}
	return rows, counters, nil
}

// veridianTransactionalRowSQL : les messages transactionnels (même définition que la
// dimension analytics message_type et que les compteurs, à l'inverse de
// veridianCommercialRowSQL).
const veridianTransactionalRowSQL = ` AND (veridian_message_type = 'transactional' OR transactional_notification_id IS NOT NULL)`

// GetTransactionalWatchInput : un seul balayage de message_history pour le profil.
// Les rejets durs se comptent comme ceux du fusible commercial (bounced_at posé et
// bounce_type dur), les refus de politique par bounce_type = 'PolicyBounce'. Un envoi
// compte s'il est parti (sent_at) et n'a pas échoué (failed_at IS NULL).
func (r *veridianEmailProfileOverviewRepository) GetTransactionalWatchInput(ctx context.Context, workspaceID, profileID string, day domain.VeridianDay, previousStart, rollingStart time.Time) (domain.VeridianTransactionalWatchInput, error) {
	var in domain.VeridianTransactionalWatchInput
	db, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return in, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	earliest := previousStart
	if rollingStart.Before(earliest) {
		earliest = rollingStart
	}
	err = db.QueryRowContext(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE sent_at >= $2 AND sent_at < $3),
		  COUNT(*) FILTER (WHERE sent_at >= $4 AND sent_at < $2),
		  COUNT(*) FILTER (WHERE sent_at >= $5),
		  COUNT(*) FILTER (WHERE sent_at >= $5 AND bounced_at IS NOT NULL AND bounce_type ILIKE 'hard%'),
		  COUNT(*) FILTER (WHERE sent_at >= $5 AND complained_at IS NOT NULL),
		  COUNT(*) FILTER (WHERE sent_at >= $5 AND bounce_type = 'PolicyBounce')
		FROM message_history
		WHERE veridian_profile_id = $1 AND sent_at >= $6 AND failed_at IS NULL`+veridianTransactionalRowSQL,
		profileID, day.Start.UTC(), day.End.UTC(), previousStart.UTC(), rollingStart.UTC(), earliest.UTC(),
	).Scan(&in.SentToday, &in.SentPrevious7Days, &in.Sent7d, &in.HardBounces7d, &in.Complaints7d, &in.PolicyRefusals7d)
	if err != nil {
		return in, fmt.Errorf("query transactional watch: %w", err)
	}
	return in, nil
}

// GetDueStock : le stock dû avant `until`, en lecture seule. Deux sources, la file (même
// prédicat « dû » que FetchPending : en attente, à rejouer, en cours, échéance passée ou
// nulle ; les entrées en pause sont écartées) et les contacts actifs d'une automation
// vivante dont la prochaine étape (envoi ou attente) échoit avant `until`. Un contact qui a
// répondu n'est plus dû (sortie sur réponse).
func (r *veridianEmailProfileOverviewRepository) GetDueStock(ctx context.Context, workspaceID string, until time.Time) ([]domain.VeridianStockRow, error) {
	db, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(q.payload->>'veridian_provider_class', ''), NULLIF(lower(c.custom_string_5), ''), ''),
		       lower(split_part(q.contact_email, '@', 2)), COUNT(*)
		FROM email_queue q
		LEFT JOIN contacts c ON c.email = q.contact_email
		WHERE q.source_type IN ('broadcast', 'automation')
		  AND (q.status = 'pending' OR (q.status = 'failed' AND q.attempts < q.max_attempts) OR q.status = 'processing')
		  AND (q.next_retry_at IS NULL OR q.next_retry_at < $1)
		  AND NOT EXISTS (SELECT 1 FROM veridian_contact_reply rp WHERE rp.contact_email = q.contact_email)
		GROUP BY 1, 2
		UNION ALL
		SELECT COALESCE(NULLIF(lower(c.custom_string_5), ''), ''), lower(split_part(ca.contact_email, '@', 2)), COUNT(*)
		FROM contact_automations ca
		JOIN automations a ON a.id = ca.automation_id AND a.status = 'live' AND a.deleted_at IS NULL
		LEFT JOIN contacts c ON c.email = ca.contact_email
		WHERE ca.status = 'active' AND ca.scheduled_at IS NOT NULL AND ca.scheduled_at < $1
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(a.nodes) n
		              WHERE n->>'id' = ca.current_node_id AND n->>'type' IN ('email', 'delay'))
		  AND NOT EXISTS (SELECT 1 FROM veridian_contact_reply rp WHERE rp.contact_email = ca.contact_email)
		GROUP BY 1, 2
	`, until.UTC())
	if err != nil {
		return nil, fmt.Errorf("query due stock: %w", err)
	}
	defer rows.Close()
	out := []domain.VeridianStockRow{}
	for rows.Next() {
		var row domain.VeridianStockRow
		if err := rows.Scan(&row.Tag, &row.Domain, &row.Count); err != nil {
			return nil, fmt.Errorf("scan due stock: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due stock: %w", err)
	}
	return out, nil
}
