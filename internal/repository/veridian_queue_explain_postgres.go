package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/lib/pq"
)

// Veridian fork (fiche 62, lot 1) : lecture de email_queue pour l'explorateur de file.
// Lecture seule, sauf Recompute (remise a zero bornee de next_retry_at).

type veridianQueueExplainRepository struct {
	workspaceRepo domain.WorkspaceRepository
	db            *sql.DB // tests
}

// NewVeridianQueueExplainRepository cree le depot de l'explorateur de file.
func NewVeridianQueueExplainRepository(workspaceRepo domain.WorkspaceRepository) domain.VeridianQueueExplainRepository {
	return &veridianQueueExplainRepository{workspaceRepo: workspaceRepo}
}

// NewVeridianQueueExplainRepositoryWithDB cree le depot sur une connexion directe (tests).
func NewVeridianQueueExplainRepositoryWithDB(db *sql.DB) domain.VeridianQueueExplainRepository {
	return &veridianQueueExplainRepository{db: db}
}

func (r *veridianQueueExplainRepository) conn(ctx context.Context, workspaceID string) (*sql.DB, error) {
	if r.db != nil {
		return r.db, nil
	}
	return r.workspaceRepo.GetConnection(ctx, workspaceID)
}

const veridianQueueGroupLimit = 500

// veridianQueueReasonSQL derive la raison d'une entree A LA LECTURE : la raison
// persistee par le worker si elle existe, sinon l'etat. Une entree jamais examinee
// (ni raison, ni prochaine tentative) est « not_examined » ; un report ancien sans
// raison (poses avant V62) est « deferred_legacy » : on le dit, on ne le devine pas.
func veridianQueueReasonSQL(alias string) string {
	p := alias + "."
	return `CASE
		WHEN ` + p + `status = 'paused' THEN 'automation_paused'
		WHEN ` + p + `status = 'processing' THEN 'in_flight'
		WHEN ` + p + `defer_reason IS NOT NULL THEN ` + p + `defer_reason
		WHEN ` + p + `status = 'failed' THEN 'send_error'
		WHEN ` + p + `next_retry_at IS NOT NULL THEN 'deferred_legacy'
		ELSE 'not_examined'
	END`
}

// veridianQueueBaseCTE : une ligne par entree avec ses dimensions resolues. Le noeud
// vient de l'entree (posee a la mise en file) sinon du contact_automation le plus
// recent de la paire (historique d'avant V62).
const veridianQueueBaseCTEHead = `
	WITH q AS (
		SELECT e.id, e.status, e.created_at, e.next_retry_at, e.first_examined_at,
		       CASE WHEN e.source_type = 'automation' THEN e.source_id ELSE '' END AS automation_id,
		       COALESCE(NULLIF(e.node_id, ''), ca.current_node_id, '') AS node_id,
		       %s AS reason,
		       COALESCE(e.defer_detail, '') AS reason_detail,
		       COALESCE(NULLIF(e.defer_profile, ''), e.integration_id) AS profile_id,
		       COALESCE(e.payload->>'veridian_provider_class', '') AS class
		FROM email_queue e
		LEFT JOIN LATERAL (
			SELECT c.current_node_id
			FROM contact_automations c
			WHERE e.source_type = 'automation' AND (e.node_id IS NULL OR e.node_id = '')
			  AND c.automation_id = e.source_id AND c.contact_email = e.contact_email
			ORDER BY c.entered_at DESC LIMIT 1
		) ca ON true
	)`

func veridianQueueBaseCTE() string {
	return fmt.Sprintf(veridianQueueBaseCTEHead, veridianQueueReasonSQL("e"))
}

func (r *veridianQueueExplainRepository) Explain(ctx context.Context, workspaceID string, f domain.VeridianQueueExplainFilter) (*domain.VeridianQueueExplain, error) {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	out := &domain.VeridianQueueExplain{
		WorkspaceID: workspaceID,
		GeneratedAt: time.Now().UTC(),
		Groups:      []domain.VeridianQueueGroup{},
		Orphans:     domain.VeridianQueueOrphans{ByNode: []domain.VeridianQueueOrphanNode{}},
	}

	var where []string
	var args []interface{}
	add := func(col, v string) {
		if v == "" {
			return
		}
		args = append(args, v)
		where = append(where, col+" = $"+strconv.Itoa(len(args)))
	}
	add("q.automation_id", f.AutomationID)
	add("q.node_id", f.NodeID)
	add("q.reason", f.Reason)
	add("q.profile_id", f.ProfileID)
	add("q.class", f.Class)
	add("q.status", f.Status)
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ")
	}

	// Dimensions de regroupement (liste blanche : jamais d'identifiant venu du client
	// dans le SQL).
	has := map[string]bool{}
	for _, g := range f.GroupBy {
		has[g] = true
	}
	dim := func(name, col string) string {
		if has[name] {
			return "q." + col
		}
		return "''"
	}
	groupCols := []string{}
	for _, d := range []struct{ name, col string }{{"automation", "automation_id"}, {"node", "node_id"}, {"reason", "reason"}, {"profile", "profile_id"}, {"class", "class"}} {
		if has[d.name] {
			groupCols = append(groupCols, "q."+d.col)
		}
	}
	groupBy := ""
	if len(groupCols) > 0 {
		groupBy = "GROUP BY " + strings.Join(groupCols, ", ")
	}

	nameExpr, joinSQL := "''", ""
	if has["automation"] {
		nameExpr = "COALESCE(a.name, '')"
		joinSQL = "LEFT JOIN automations a ON a.id = q.automation_id"
	}
	query := veridianQueueBaseCTE() + fmt.Sprintf(`
		SELECT %s AS automation_id, %s AS automation_name, %s AS node_id, %s AS reason,
		       COALESCE(mode() WITHIN GROUP (ORDER BY q.reason_detail), '') AS reason_detail,
		       %s AS profile_id, %s AS class,
		       count(*) AS n,
		       count(*) FILTER (WHERE q.first_examined_at IS NULL AND q.next_retry_at IS NULL) AS never_examined,
		       min(q.created_at), min(q.next_retry_at), max(q.next_retry_at),
		       (array_agg(q.id ORDER BY q.created_at))[1:3]
		FROM q
		%s
		%s
		%s
		ORDER BY n DESC
		LIMIT %d`,
		dim("automation", "automation_id"), nameExpr, dim("node", "node_id"), dim("reason", "reason"),
		dim("profile", "profile_id"), dim("class", "class"),
		joinSQL, cond, groupByWithName(groupBy, has["automation"]), veridianQueueGroupLimit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to explain queue: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var g domain.VeridianQueueGroup
		var oldest, nmin, nmax sql.NullTime
		var samples pq.StringArray
		if err := rows.Scan(&g.AutomationID, &g.AutomationName, &g.NodeID, &g.Reason, &g.ReasonDetail,
			&g.ProfileID, &g.Class, &g.Count, &g.NeverExamined, &oldest, &nmin, &nmax, &samples); err != nil {
			return nil, fmt.Errorf("failed to scan queue group: %w", err)
		}
		if oldest.Valid {
			t := oldest.Time
			g.OldestCreatedAt = &t
		}
		if nmin.Valid {
			t := nmin.Time
			g.NextAttemptMin = &t
		}
		if nmax.Valid {
			t := nmax.Time
			g.NextAttemptMax = &t
		}
		g.SampleEntryIDs = []string(samples)
		if g.SampleEntryIDs == nil {
			g.SampleEntryIDs = []string{}
		}
		out.Total += g.Count
		out.Groups = append(out.Groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate queue groups: %w", err)
	}

	// Orphelins : contacts parques en envoi sans aucune entree de file.
	var oargs []interface{}
	var ocond []string
	if f.AutomationID != "" {
		oargs = append(oargs, f.AutomationID)
		ocond = append(ocond, "ca.automation_id = $"+strconv.Itoa(len(oargs)))
	}
	if f.NodeID != "" {
		oargs = append(oargs, f.NodeID)
		ocond = append(ocond, "ca.current_node_id = $"+strconv.Itoa(len(oargs)))
	}
	extra := ""
	if len(ocond) > 0 {
		extra = " AND " + strings.Join(ocond, " AND ")
	}
	orows, err := db.QueryContext(ctx, `
		SELECT ca.automation_id, COALESCE(a.name, ''), COALESCE(ca.current_node_id, ''), count(*)
		FROM contact_automations ca
		LEFT JOIN automations a ON a.id = ca.automation_id
		WHERE ca.status = 'sending'
		  AND NOT EXISTS (SELECT 1 FROM email_queue e
		                  WHERE e.source_type = 'automation' AND e.source_id = ca.automation_id
		                    AND e.contact_email = ca.contact_email)`+extra+`
		GROUP BY 1, 2, 3 ORDER BY 4 DESC`, oargs...)
	if err != nil {
		return nil, fmt.Errorf("failed to count orphan parked contacts: %w", err)
	}
	defer orows.Close()
	for orows.Next() {
		var n domain.VeridianQueueOrphanNode
		if err := orows.Scan(&n.AutomationID, &n.AutomationName, &n.NodeID, &n.Count); err != nil {
			return nil, fmt.Errorf("failed to scan orphan node: %w", err)
		}
		out.Orphans.Count += n.Count
		out.Orphans.ByNode = append(out.Orphans.ByNode, n)
	}
	return out, orows.Err()
}

// groupByWithName ajoute le nom d'automation au GROUP BY quand on regroupe par automation.
func groupByWithName(groupBy string, byAutomation bool) string {
	if groupBy == "" {
		return ""
	}
	if byAutomation {
		return groupBy + ", a.name"
	}
	return groupBy
}

// EntryDetail rend le detail d'une entree de file (sans sa derniere decision, que le
// service ajoute depuis le journal).
func (r *veridianQueueExplainRepository) EntryDetail(ctx context.Context, workspaceID, entryID string) (*domain.VeridianQueueEntryDetail, error) {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	var d domain.VeridianQueueEntryDetail
	var next, deferredAt, first, last sql.NullTime
	var deferCount sql.NullInt64
	err = db.QueryRowContext(ctx, `
		SELECT e.id, e.status, CASE WHEN e.source_type = 'automation' THEN e.source_id ELSE '' END,
		       COALESCE(a.name, ''), COALESCE(NULLIF(e.node_id, ''), ca.current_node_id, ''),
		       e.contact_email, e.integration_id,
		       COALESCE(e.payload->>'veridian_provider_class', ''), e.created_at, e.attempts, e.max_attempts,
		       e.next_retry_at, `+veridianQueueReasonSQL("e")+`, COALESCE(e.defer_detail, ''),
		       e.deferred_at, e.defer_count, e.first_examined_at, e.last_examined_at, COALESCE(e.last_error, '')
		FROM email_queue e
		LEFT JOIN automations a ON a.id = e.source_id AND e.source_type = 'automation'
		LEFT JOIN LATERAL (
			SELECT c.current_node_id FROM contact_automations c
			WHERE e.source_type = 'automation' AND (e.node_id IS NULL OR e.node_id = '')
			  AND c.automation_id = e.source_id AND c.contact_email = e.contact_email
			ORDER BY c.entered_at DESC LIMIT 1
		) ca ON true
		WHERE e.id = $1`, entryID).Scan(
		&d.ID, &d.Status, &d.AutomationID, &d.AutomationName, &d.NodeID, &d.ContactEmail, &d.IntegrationID,
		&d.Class, &d.CreatedAt, &d.Attempts, &d.MaxAttempts, &next, &d.Reason, &d.ReasonDetail,
		&deferredAt, &deferCount, &first, &last, &d.LastError)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read queue entry: %w", err)
	}
	set := func(dst **time.Time, v sql.NullTime) {
		if v.Valid {
			t := v.Time
			*dst = &t
		}
	}
	set(&d.NextRetryAt, next)
	set(&d.DeferredAt, deferredAt)
	set(&d.FirstExaminedAt, first)
	set(&d.LastExaminedAt, last)
	d.DeferUntil = d.NextRetryAt
	d.DeferCount = int(deferCount.Int64)
	return &d, nil
}

// Recompute remet next_retry_at a NULL et efface la raison des entrees PENDING du
// filtre (une entree pending sans prochaine tentative est due : le worker la reprend
// au prochain tick). Aucune entree supprimee, aucune tentative consommee. Borne par
// req.Limit ; les lignes verrouillees par un worker sont sautees.
func (r *veridianQueueExplainRepository) Recompute(ctx context.Context, workspaceID string, req domain.VeridianQueueRecomputeRequest) ([]string, error) {
	db, err := r.conn(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	var where []string
	var args []interface{}
	add := func(cond string, v interface{}) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if req.AutomationID != "" {
		add("e.source_type = 'automation' AND e.source_id = ?", req.AutomationID)
	}
	if len(req.EntryIDs) > 0 {
		add("e.id = ANY(?)", pq.Array(req.EntryIDs))
	}
	if req.NodeID != "" {
		args = append(args, req.NodeID)
		n := strconv.Itoa(len(args))
		where = append(where, `(e.node_id = $`+n+` OR ((e.node_id IS NULL OR e.node_id = '') AND EXISTS (
			SELECT 1 FROM contact_automations c WHERE c.automation_id = e.source_id
			  AND c.contact_email = e.contact_email AND c.current_node_id = $`+n+`)))`)
	}
	if req.Reason != "" {
		add(veridianQueueReasonSQL("e")+" = ?", req.Reason)
	}
	if req.ProfileID != "" {
		add("COALESCE(NULLIF(e.defer_profile, ''), e.integration_id) = ?", req.ProfileID)
	}
	args = append(args, req.Limit)
	query := `
		UPDATE email_queue
		SET next_retry_at = NULL, defer_reason = NULL, defer_detail = NULL, defer_profile = NULL, updated_at = NOW()
		WHERE id IN (
			SELECT e.id FROM email_queue e
			WHERE e.status = 'pending' AND ` + strings.Join(where, " AND ") + `
			ORDER BY e.created_at, e.id
			LIMIT $` + strconv.Itoa(len(args)) + `
			FOR UPDATE SKIP LOCKED)
		RETURNING id`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to recompute queue entries: %w", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
