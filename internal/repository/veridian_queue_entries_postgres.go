package repository

// === Veridian — file d'envoi : lister et annuler (2026-10-27) ===
// Lecture par curseur keyset (created_at, id) et annulation dans UNE transaction :
// suppression des lignes de email_queue ET écriture de leur ligne de journal
// (veridian_send_decisions, outcome « cancelled »). Si le journal échoue, rien n'est
// supprimé. Le périmètre (automation, nœud, profil, classe, statut, identifiants)
// est un seul constructeur de clause, partagé par la lecture et l'annulation : elles
// voient donc les mêmes entrées.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type veridianQueueEntriesRepository struct {
	workspaceRepo domain.WorkspaceRepository
	db            *sql.DB
}

// NewVeridianQueueEntriesRepository : db fixe (tests) ou connexion du workspace.
func NewVeridianQueueEntriesRepository(workspaceRepo domain.WorkspaceRepository, db *sql.DB) domain.VeridianQueueEntriesRepository {
	return &veridianQueueEntriesRepository{workspaceRepo: workspaceRepo, db: db}
}

func (r *veridianQueueEntriesRepository) conn(ctx context.Context, workspaceID string) (*sql.DB, error) {
	if r.db != nil {
		return r.db, nil
	}
	return r.workspaceRepo.GetConnection(ctx, workspaceID)
}

// queueEntryScope : périmètre commun à la lecture et à l'annulation.
type queueEntryScope struct {
	automationID string
	nodeID       string
	profileID    string
	class        string
	statuses     []string // IN (...) ; vide = pas de filtre de statut
	entryIDs     []string
	cursorAt     *time.Time
	cursorID     string
}

// buildQueueEntryWhere rend la clause WHERE (alias e = email_queue) et ses arguments.
func buildQueueEntryWhere(s queueEntryScope) (string, []interface{}) {
	var where []string
	var args []interface{}
	add := func(cond string, vals ...interface{}) {
		for _, v := range vals {
			args = append(args, v)
			cond = strings.Replace(cond, "?", fmt.Sprintf("$%d", len(args)), 1)
		}
		where = append(where, cond)
	}
	if s.automationID != "" {
		add("e.source_type = 'automation' AND e.source_id = ?", s.automationID)
	}
	if len(s.entryIDs) > 0 {
		add("e.id = ANY(?)", pq.Array(s.entryIDs))
	}
	if len(s.statuses) > 0 {
		add("e.status = ANY(?)", pq.Array(s.statuses))
	}
	if s.nodeID != "" {
		// Même résolution du nœud que l'explorateur : celui de l'entrée, sinon celui du contact.
		args = append(args, s.nodeID)
		n := fmt.Sprintf("$%d", len(args))
		where = append(where, `(e.node_id = `+n+` OR ((e.node_id IS NULL OR e.node_id = '') AND EXISTS (
			SELECT 1 FROM contact_automations c WHERE c.automation_id = e.source_id
			  AND c.contact_email = e.contact_email AND c.current_node_id = `+n+`)))`)
	}
	if s.profileID != "" {
		add("COALESCE(NULLIF(e.defer_profile, ''), e.integration_id) = ?", s.profileID)
	}
	if s.class != "" {
		add("(e.payload->>'veridian_provider_class') = ?", s.class)
	}
	if s.cursorAt != nil {
		args = append(args, *s.cursorAt, s.cursorID)
		where = append(where, fmt.Sprintf("(e.created_at, e.id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	if len(where) == 0 {
		return "TRUE", args
	}
	return strings.Join(where, " AND "), args
}

const queueEntrySelectCols = `e.id, e.status, e.contact_email, e.source_id, COALESCE(e.node_id, ''),
	` + "%s" + ` AS reason, COALESCE(e.defer_detail, ''),
	COALESCE(NULLIF(e.defer_profile, ''), COALESCE(e.integration_id, '')),
	COALESCE(e.payload->>'veridian_provider_class', ''), e.created_at, e.next_retry_at, e.priority`

// ListEntries rend une page d'entrées d'une automation (ordre d'arrivée dans la file).
func (r *veridianQueueEntriesRepository) ListEntries(ctx context.Context, f domain.VeridianQueueEntriesFilter) (*domain.VeridianQueueEntriesPage, error) {
	db, err := r.conn(ctx, f.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	scope := queueEntryScope{automationID: f.AutomationID, nodeID: f.NodeID, profileID: f.ProfileID, class: f.Class}
	if f.Status != "" {
		scope.statuses = []string{f.Status}
	}
	if f.Cursor != "" {
		at, id, err := domain.DecodeVeridianEnrollmentCursor(f.Cursor)
		if err != nil {
			return nil, err
		}
		scope.cursorAt, scope.cursorID = &at, id
	}
	where, args := buildQueueEntryWhere(scope)
	args = append(args, f.Limit+1)
	query := `SELECT ` + fmt.Sprintf(queueEntrySelectCols, veridianQueueReasonSQL("e")) + `
		FROM email_queue e WHERE ` + where + fmt.Sprintf(" ORDER BY e.created_at, e.id LIMIT $%d", len(args))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list queue entries: %w", err)
	}
	defer rows.Close()

	out := []*domain.VeridianQueueEntry{}
	var createdAt []time.Time
	for rows.Next() {
		var e domain.VeridianQueueEntry
		var created time.Time
		var nextRetry sql.NullTime
		if err := rows.Scan(&e.ID, &e.Status, &e.ContactEmail, &e.AutomationID, &e.NodeID, &e.Reason,
			&e.ReasonDetail, &e.ProfileID, &e.Class, &created, &nextRetry, &e.Priority); err != nil {
			return nil, fmt.Errorf("failed to scan queue entry: %w", err)
		}
		e.CreatedAt = created.UTC().Format(time.RFC3339Nano)
		if nextRetry.Valid {
			s := nextRetry.Time.UTC().Format(time.RFC3339Nano)
			e.NextRetryAt = &s
		}
		createdAt = append(createdAt, created)
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	page := &domain.VeridianQueueEntriesPage{Entries: out}
	if len(out) > f.Limit {
		page.Entries = out[:f.Limit]
		last := page.Entries[len(page.Entries)-1]
		page.NextCursor = domain.EncodeVeridianEnrollmentCursor(createdAt[f.Limit-1], last.ID)
	}
	return page, nil
}

// CancelEntries annule les entrées éligibles (pending, failed, paused ; jamais processing).
// Dry-run : compte et échantillonne sans rien toucher. Sinon : suppression et journal
// dans la même transaction.
func (r *veridianQueueEntriesRepository) CancelEntries(ctx context.Context, req domain.VeridianQueueCancelRequest, actor string) (*domain.VeridianQueueCancelResult, error) {
	db, err := r.conn(ctx, req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	statuses := []string{"pending", "failed", "paused"}
	if req.Status != "" {
		statuses = []string{req.Status}
	}
	where, args := buildQueueEntryWhere(queueEntryScope{
		automationID: req.AutomationID, nodeID: req.NodeID, profileID: req.ProfileID, class: req.Class,
		statuses: statuses, entryIDs: req.EntryIDs,
	})

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var matched int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM email_queue e WHERE "+where, args...).Scan(&matched); err != nil {
		return nil, fmt.Errorf("failed to count cancellable entries: %w", err)
	}
	result := &domain.VeridianQueueCancelResult{DryRun: req.DryRun, Matched: matched, Entries: []*domain.VeridianQueueCancelledEntry{}}

	if req.DryRun {
		sample := append(append([]interface{}{}, args...), req.Limit)
		rows, err := tx.QueryContext(ctx, "SELECT e.id, e.contact_email, e.source_id, COALESCE(e.node_id, '') FROM email_queue e WHERE "+where+
			fmt.Sprintf(" ORDER BY e.created_at, e.id LIMIT $%d", len(sample)), sample...)
		if err != nil {
			return nil, fmt.Errorf("failed to sample cancellable entries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c domain.VeridianQueueCancelledEntry
			if err := rows.Scan(&c.ID, &c.ContactEmail, &c.AutomationID, &c.NodeID); err != nil {
				return nil, err
			}
			result.Entries = append(result.Entries, &c)
		}
		return result, rows.Err()
	}

	limited := append(append([]interface{}{}, args...), req.Limit)
	rows, err := tx.QueryContext(ctx, `DELETE FROM email_queue WHERE id IN (
			SELECT e.id FROM email_queue e WHERE `+where+fmt.Sprintf(`
			ORDER BY e.created_at, e.id LIMIT $%d FOR UPDATE SKIP LOCKED)
		RETURNING id, contact_email, source_id, COALESCE(node_id, '')`, len(limited)), limited...)
	if err != nil {
		return nil, fmt.Errorf("failed to cancel queue entries: %w", err)
	}
	var removed []*domain.VeridianQueueCancelledEntry
	for rows.Next() {
		var c domain.VeridianQueueCancelledEntry
		if err := rows.Scan(&c.ID, &c.ContactEmail, &c.AutomationID, &c.NodeID); err != nil {
			rows.Close()
			return nil, err
		}
		removed = append(removed, &c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	detail := truncateRunes(fmt.Sprintf("annulé par %s : %s", actor, req.Reason), 200)
	for _, c := range removed {
		if _, err := tx.ExecContext(ctx, `INSERT INTO veridian_send_decisions
			(id, at, entry_id, contact_email, automation_id, node_id, outcome, reason, detail)
			VALUES ($1, NOW(), $2, $3, $4, $5, $6, $7, $8)`,
			uuid.NewString(), c.ID, c.ContactEmail, nullableString(c.AutomationID), nullableString(c.NodeID),
			domain.VeridianOutcomeCancelled, domain.VeridianReasonOperatorCancel, detail); err != nil {
			return nil, fmt.Errorf("failed to journal cancellation (rien n'est annule) : %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit cancellation: %w", err)
	}
	result.Cancelled = len(removed)
	result.Entries = removed
	return result, nil
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// truncateRunes tronque sans couper un caractère multi-octets.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
