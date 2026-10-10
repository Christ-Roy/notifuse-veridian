package repository

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork (fiche 62, lot 1) : journal des decisions d'envoi, table
// veridian_send_decisions de la base du workspace (migration V62).
//
// Retention OPPORTUNISTE (loi 2 : aucune tache planifiee) : toutes les
// veridianDecisionPurgeEvery insertions, une passe bornee (500 lignes par etape) qui
//   - elague la trace des lignes de plus de 14 jours (le resume reste),
//   - supprime les lignes de plus de 90 jours,
//   - applique le plafond dur de 500 000 lignes par workspace (les plus anciennes
//     partent d'abord).

const (
	veridianDecisionPurgeEvery   = 1000
	veridianDecisionPurgeBatch   = 500
	veridianDecisionTraceKeep    = "14 days"
	veridianDecisionRowKeep      = "90 days"
	veridianDecisionHardCap      = 500000
	veridianDecisionDefaultLimit = 50
	veridianDecisionMaxLimit     = 200
)

type veridianSendDecisionRepository struct {
	workspaceRepo domain.WorkspaceRepository
	db            *sql.DB // tests
	inserts       atomic.Uint64
}

// NewVeridianSendDecisionRepository cree le depot du journal des decisions.
func NewVeridianSendDecisionRepository(workspaceRepo domain.WorkspaceRepository) domain.VeridianSendDecisionRepository {
	return &veridianSendDecisionRepository{workspaceRepo: workspaceRepo}
}

// NewVeridianSendDecisionRepositoryWithDB cree le depot sur une connexion directe (tests).
func NewVeridianSendDecisionRepositoryWithDB(db *sql.DB) domain.VeridianSendDecisionRepository {
	return &veridianSendDecisionRepository{db: db}
}

func (r *veridianSendDecisionRepository) conn(ctx context.Context, workspaceID string) (*sql.DB, error) {
	if r.db != nil {
		return r.db, nil
	}
	return r.workspaceRepo.GetConnection(ctx, workspaceID)
}

func nullIfEmpty(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// Insert ecrit une decision puis, de temps en temps, applique la retention.
func (r *veridianSendDecisionRepository) Insert(ctx context.Context, workspaceID string, d *domain.VeridianSendDecision) error {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	var trace interface{}
	if d.Trace != nil {
		b, err := json.Marshal(d.Trace)
		if err != nil {
			return fmt.Errorf("failed to marshal decision trace: %w", err)
		}
		trace = b
	}
	var until sql.NullTime
	if d.Until != nil {
		until = sql.NullTime{Time: *d.Until, Valid: true}
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO veridian_send_decisions
			(id, at, entry_id, message_id, contact_email, automation_id, node_id, outcome, reason, detail, until, profile_id, sampled, trace)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		d.ID, d.At, nullIfEmpty(d.EntryID), nullIfEmpty(d.MessageID), d.ContactEmail,
		nullIfEmpty(d.AutomationID), nullIfEmpty(d.NodeID), d.Outcome, nullIfEmpty(d.Reason),
		nullIfEmpty(d.Detail), until, nullIfEmpty(d.ProfileID), d.Sampled, trace)
	if err != nil {
		return fmt.Errorf("failed to insert send decision: %w", err)
	}
	if r.inserts.Add(1)%veridianDecisionPurgeEvery == 0 {
		r.purge(ctx, db)
	}
	return nil
}

// purge : une passe de retention bornee. Les erreurs sont ignorees (best-effort, la
// passe suivante recommence).
func (r *veridianSendDecisionRepository) purge(ctx context.Context, db *sql.DB) {
	_, _ = db.ExecContext(ctx, `
		UPDATE veridian_send_decisions SET trace = NULL
		WHERE id IN (SELECT id FROM veridian_send_decisions
		             WHERE at < NOW() - INTERVAL '`+veridianDecisionTraceKeep+`' AND trace IS NOT NULL LIMIT $1)`,
		veridianDecisionPurgeBatch)
	_, _ = db.ExecContext(ctx, `
		DELETE FROM veridian_send_decisions
		WHERE id IN (SELECT id FROM veridian_send_decisions
		             WHERE at < NOW() - INTERVAL '`+veridianDecisionRowKeep+`' LIMIT $1)`,
		veridianDecisionPurgeBatch)
	_, _ = db.ExecContext(ctx, `
		DELETE FROM veridian_send_decisions
		WHERE (SELECT COALESCE(reltuples, 0)::bigint FROM pg_class WHERE relname = 'veridian_send_decisions') > $1
		  AND id IN (SELECT id FROM veridian_send_decisions ORDER BY at ASC LIMIT $2)`,
		veridianDecisionHardCap, veridianDecisionPurgeBatch)
}

func encodeDecisionCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeDecisionCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	return at, parts[1], nil
}

// List rend les decisions, les plus recentes d'abord.
func (r *veridianSendDecisionRepository) List(ctx context.Context, workspaceID string, f domain.VeridianSendDecisionFilter) ([]*domain.VeridianSendDecision, string, error) {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get database connection: %w", err)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = veridianDecisionDefaultLimit
	}
	if limit > veridianDecisionMaxLimit {
		limit = veridianDecisionMaxLimit
	}

	var where []string
	var args []interface{}
	add := func(cond string, v interface{}) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.AutomationID != "" {
		add("automation_id = ?", f.AutomationID)
	}
	if f.NodeID != "" {
		add("node_id = ?", f.NodeID)
	}
	if f.Email != "" {
		add("contact_email = ?", strings.ToLower(strings.TrimSpace(f.Email)))
	}
	if f.EntryID != "" {
		add("entry_id = ?", f.EntryID)
	}
	if f.Reason != "" {
		add("reason = ?", f.Reason)
	}
	if f.Outcome != "" {
		add("outcome = ?", f.Outcome)
	}
	if f.Since != nil {
		add("at >= ?", *f.Since)
	}
	if f.Cursor != "" {
		at, id, err := decodeDecisionCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, at, id)
		where = append(where, fmt.Sprintf("(at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ")
	}
	traceCol := "NULL::jsonb"
	if f.WithTrace {
		traceCol = "trace"
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(`
		SELECT id, at, COALESCE(entry_id,''), COALESCE(message_id,''), contact_email, COALESCE(automation_id,''),
		       COALESCE(node_id,''), outcome, COALESCE(reason,''), COALESCE(detail,''), until,
		       COALESCE(profile_id,''), sampled, %s
		FROM veridian_send_decisions %s
		ORDER BY at DESC, id DESC
		LIMIT $%d`, traceCol, cond, len(args))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list send decisions: %w", err)
	}
	defer rows.Close()

	var out []*domain.VeridianSendDecision
	for rows.Next() {
		var d domain.VeridianSendDecision
		var until sql.NullTime
		var trace []byte
		if err := rows.Scan(&d.ID, &d.At, &d.EntryID, &d.MessageID, &d.ContactEmail, &d.AutomationID,
			&d.NodeID, &d.Outcome, &d.Reason, &d.Detail, &until, &d.ProfileID, &d.Sampled, &trace); err != nil {
			return nil, "", fmt.Errorf("failed to scan send decision: %w", err)
		}
		if until.Valid {
			t := until.Time
			d.Until = &t
		}
		if len(trace) > 0 {
			var tr domain.VeridianSendTrace
			if err := json.Unmarshal(trace, &tr); err == nil {
				d.Trace = &tr
			}
		}
		out = append(out, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate send decisions: %w", err)
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = encodeDecisionCursor(last.At, last.ID)
	}
	return out, next, nil
}
