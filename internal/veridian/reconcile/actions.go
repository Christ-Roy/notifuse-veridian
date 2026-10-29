package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Les réparations. Chaque action relit ses cibles au moment d'appliquer (même prédicat
// que le contrôle) : elle ne se fie jamais à la liste vue à la détection, et elle ne
// touche que ce que le contrôle juge encore en écart. Tout se fait dans UNE transaction.
const (
	ActionReclassify   = "reclassify_contacts"
	ActionExitContacts = "exit_contacts"
	ActionCancelQueue  = "cancel_queue_entries"
	ActionRecountStats = "recount_automation_stats"
)

// Executor applique les actions proposées.
type Executor struct {
	DB   BeginnerDB
	Time func() time.Time
}

func (x *Executor) now() time.Time {
	if x.Time != nil {
		return x.Time().UTC()
	}
	return time.Now().UTC()
}

var classTagPattern = regexp.MustCompile(`^[a-z0-9_]{0,40}$`)

func paramString(p map[string]any, key string) string {
	v, _ := p[key].(string)
	return v
}

// Apply exécute l'action d'un écart et rend ce qui a été fait.
func (x *Executor) Apply(ctx context.Context, f *Finding) (map[string]any, error) {
	if f == nil || f.ProposedAction == nil {
		return nil, fmt.Errorf("cet écart n'a pas de réparation proposée")
	}
	a := f.ProposedAction
	switch a.Kind {
	case ActionReclassify:
		return x.reclassify(ctx, a.Params)
	case ActionExitContacts:
		return x.exitContacts(ctx, a.Params)
	case ActionCancelQueue:
		return x.cancelQueue(ctx, a.Params)
	case ActionRecountStats:
		return x.recountStats(ctx, a.Params)
	}
	return nil, fmt.Errorf("action inconnue: %s", a.Kind)
}

func (x *Executor) reclassify(ctx context.Context, p map[string]any) (map[string]any, error) {
	from, to := paramString(p, "from"), paramString(p, "to")
	if !classTagPattern.MatchString(from) {
		return nil, fmt.Errorf("classe d'origine invalide")
	}
	if !domain.IsValidProviderClass(to) || to == domain.ProviderClassCorporate || to == domain.ProviderClassFreemailFR {
		return nil, fmt.Errorf("classe cible invalide: %q", to)
	}
	domains, _ := domain.VeridianDomainsForClass(to)
	if len(domains) == 0 {
		return nil, fmt.Errorf("aucun domaine connu pour la classe %q", to)
	}
	res, err := x.DB.ExecContext(ctx, `UPDATE contacts SET custom_string_5 = $1, updated_at = $2
		WHERE lower(split_part(email, '@', 2)) = ANY($3) AND lower(btrim(COALESCE(custom_string_5, ''))) = $4`,
		to, x.now(), pq.Array(domains), from)
	if err != nil {
		return nil, fmt.Errorf("reclassement: %w", err)
	}
	n, _ := res.RowsAffected()
	return map[string]any{"affected": n, "from": from, "to": to}, nil
}

// targets relit les cibles d'une cause : identifiants d'inscription ou d'entrée de file
// et adresses, pour UNE séquence.
func (x *Executor) targets(ctx context.Context, tx DBTX, cause statutCause, automationID string) (ids, emails []string, err error) {
	q := `SELECT DISTINCT ` + idColumn(cause) + `, ` + cause.emailExp + ` ` + cause.from + ` AND ` + cause.idExpr + ` = $2`
	rs, err := tx.QueryContext(ctx, q, x.now().Add(-StatusGrace), automationID)
	if err != nil {
		return nil, nil, fmt.Errorf("relecture des cibles: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var id, email string
		if err := rs.Scan(&id, &email); err != nil {
			return nil, nil, err
		}
		ids, emails = append(ids, id), append(emails, email)
	}
	return ids, emails, rs.Err()
}

func idColumn(c statutCause) string {
	if c.enrolled {
		return "ca.id"
	}
	return "e.id"
}

func (x *Executor) exitContacts(ctx context.Context, p map[string]any) (map[string]any, error) {
	cause, ok := statutCauseByCode(paramString(p, "cause"))
	automationID := paramString(p, "automation_id")
	if !ok || !cause.enrolled || automationID == "" {
		return nil, fmt.Errorf("cause ou séquence invalide")
	}
	tx, err := x.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	ids, emails, err := x.targets(ctx, tx, cause, automationID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return map[string]any{"exited": 0, "queue_removed": 0, "note": "plus aucune cible"}, nil
	}
	exited, removed, err := x.exitAndClean(ctx, tx, automationID, ids, emails, cause.exitReason, "réconciliateur : "+cause.code)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"exited": exited, "queue_removed": removed, "exit_reason": cause.exitReason}, nil
}

// exitAndClean sort des inscriptions (par identifiant), retire leurs envois en attente,
// compte la sortie et laisse la trace dans la chronologie du contact.
func (x *Executor) exitAndClean(ctx context.Context, tx DBTX, automationID string, caIDs, emails []string, reason, detail string) (int64, int64, error) {
	now := x.now()
	res, err := tx.ExecContext(ctx, `UPDATE contact_automations SET status = 'exited', exit_reason = $1, current_node_id = NULL, scheduled_at = NULL
		WHERE id = ANY($2) AND status IN ('active', 'sending')`, reason, pq.Array(caIDs))
	if err != nil {
		return 0, 0, fmt.Errorf("sortie des inscriptions: %w", err)
	}
	exited, _ := res.RowsAffected()
	removed, err := x.deleteQueue(ctx, tx, `source_type = 'automation' AND source_id = $1 AND status = 'pending' AND contact_email = ANY($2)`, []any{automationID, pq.Array(emails)}, detail)
	if err != nil {
		return 0, 0, err
	}
	if exited > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE automations SET stats = COALESCE(stats, '{}'::jsonb) ||
			jsonb_build_object('exited', COALESCE((stats->>'exited')::int, 0) + $2::int), updated_at = $3 WHERE id = $1`,
			automationID, exited, now); err != nil {
			return 0, 0, fmt.Errorf("compteur de sorties: %w", err)
		}
		changes, _ := json.Marshal(map[string]any{
			"automation_id": map[string]any{"new": automationID},
			"exit_reason":   map[string]any{"new": reason},
			"status":        map[string]any{"new": "exited"},
		})
		if _, err := tx.ExecContext(ctx, `INSERT INTO contact_timeline (email, operation, entity_type, kind, entity_id, changes, created_at)
			SELECT ca.contact_email, 'update', 'automation', 'automation.end', $2, $3::jsonb, $4
			FROM contact_automations ca WHERE ca.id = ANY($1) AND ca.status = 'exited' AND ca.exit_reason = $5`,
			pq.Array(caIDs), automationID, string(changes), now, reason); err != nil {
			return 0, 0, fmt.Errorf("chronologie: %w", err)
		}
	}
	return exited, removed, nil
}

// deleteQueue supprime les entrées de file désignées par `where` et écrit une ligne de
// journal « annulé » par entrée. Si le journal échoue, rien n'est supprimé.
func (x *Executor) deleteQueue(ctx context.Context, tx DBTX, where string, args []any, detail string) (int64, error) {
	rs, err := tx.QueryContext(ctx, `DELETE FROM email_queue WHERE `+where+` RETURNING id, contact_email, source_id, COALESCE(node_id, '')`, args...)
	if err != nil {
		return 0, fmt.Errorf("retrait de la file: %w", err)
	}
	type gone struct{ id, email, src, node string }
	var all []gone
	for rs.Next() {
		var g gone
		if err := rs.Scan(&g.id, &g.email, &g.src, &g.node); err != nil {
			_ = rs.Close()
			return 0, err
		}
		all = append(all, g)
	}
	if err := rs.Close(); err != nil {
		return 0, err
	}
	for _, g := range all {
		if _, err := tx.ExecContext(ctx, `INSERT INTO veridian_send_decisions (id, at, entry_id, contact_email, automation_id, node_id, outcome, reason, detail)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			uuid.NewString(), x.now(), g.id, g.email, nullIfEmpty(g.src), nullIfEmpty(g.node),
			domain.VeridianOutcomeCancelled, domain.VeridianReasonOperatorCancel, detail); err != nil {
			return 0, fmt.Errorf("journal d'annulation (rien n'est retiré): %w", err)
		}
	}
	return int64(len(all)), nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// cancelQueue retire des envois en attente ET sort les contacts restés « en cours
// d'envoi » sans entrée de file (sinon ils resteraient orphelins).
func (x *Executor) cancelQueue(ctx context.Context, p map[string]any) (map[string]any, error) {
	causeCode, automationID := paramString(p, "cause"), paramString(p, "automation_id")
	if automationID == "" {
		return nil, fmt.Errorf("séquence manquante")
	}
	tx, err := x.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var entryIDs, emails []string
	exitReason := ""
	if causeCode == causeQueueDup {
		exitReason = "queue_duplicate_cancelled"
		q := `SELECT e.id, e.contact_email ` + queueDupFrom + ` AND e.source_id = $1`
		rs, err := tx.QueryContext(ctx, q, automationID)
		if err != nil {
			return nil, fmt.Errorf("relecture des doublons: %w", err)
		}
		for rs.Next() {
			var id, em string
			if err := rs.Scan(&id, &em); err != nil {
				_ = rs.Close()
				return nil, err
			}
			entryIDs, emails = append(entryIDs, id), append(emails, em)
		}
		if err := rs.Close(); err != nil {
			return nil, err
		}
	} else {
		cause, ok := statutCauseByCode(causeCode)
		if !ok || cause.enrolled {
			return nil, fmt.Errorf("cause invalide")
		}
		exitReason = map[string]string{causeQueueSuppr: "list_status_suppressed", causeQueueReplied: "contact_replied", causeQueueExcl: "excluded_list"}[causeCode]
		entryIDs, emails, err = x.targets(ctx, tx, cause, automationID)
		if err != nil {
			return nil, err
		}
	}
	if len(entryIDs) == 0 {
		return map[string]any{"cancelled": 0, "exited": 0, "note": "plus aucune cible"}, nil
	}
	removed, err := x.deleteQueue(ctx, tx, `id = ANY($1) AND status = 'pending'`, []any{pq.Array(entryIDs)}, "réconciliateur : "+causeCode)
	if err != nil {
		return nil, err
	}
	// Les contacts parqués « sending » dont l'entrée vient de disparaître sortent avec la raison.
	rs, err := tx.QueryContext(ctx, `SELECT ca.id FROM contact_automations ca WHERE ca.automation_id = $1 AND ca.contact_email = ANY($2) AND ca.status = 'sending'
		AND NOT EXISTS (SELECT 1 FROM email_queue q WHERE q.source_type = 'automation' AND q.source_id = ca.automation_id AND q.contact_email = ca.contact_email)`,
		automationID, pq.Array(emails))
	if err != nil {
		return nil, fmt.Errorf("contacts orphelins: %w", err)
	}
	var caIDs []string
	for rs.Next() {
		var id string
		if err := rs.Scan(&id); err != nil {
			_ = rs.Close()
			return nil, err
		}
		caIDs = append(caIDs, id)
	}
	if err := rs.Close(); err != nil {
		return nil, err
	}
	var exited int64
	if len(caIDs) > 0 {
		exited, _, err = x.exitAndClean(ctx, tx, automationID, caIDs, nil, exitReason, "réconciliateur : "+causeCode)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"cancelled": removed, "exited": exited, "exit_reason": exitReason}, nil
}

func (x *Executor) recountStats(ctx context.Context, p map[string]any) (map[string]any, error) {
	id := paramString(p, "automation_id")
	if id == "" {
		return nil, fmt.Errorf("séquence manquante")
	}
	res, err := x.DB.ExecContext(ctx, `UPDATE automations a SET stats = COALESCE(a.stats, '{}'::jsonb) || jsonb_build_object(
			'enrolled',  (SELECT COUNT(*) FROM contact_automations WHERE automation_id = a.id),
			'completed', (SELECT COUNT(*) FROM contact_automations WHERE automation_id = a.id AND status = 'completed'),
			'exited',    (SELECT COUNT(*) FROM contact_automations WHERE automation_id = a.id AND status = 'exited'),
			'failed',    (SELECT COUNT(*) FROM contact_automations WHERE automation_id = a.id AND status = 'failed')),
		updated_at = $2 WHERE a.id = $1 AND a.deleted_at IS NULL`, id, x.now())
	if err != nil {
		return nil, fmt.Errorf("recalcul des compteurs: %w", err)
	}
	n, _ := res.RowsAffected()
	return map[string]any{"automations": n}, nil
}
