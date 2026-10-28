package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork, lot 5 (08/10/2026) : lecture seule des agrégats du tableau de bord de
// prospection (voir internal/domain/veridian_prospection_stats.go pour le contrat).
// Chaque requête ne lit que les séquences non supprimées (deleted_at IS NULL) et les
// listes non supprimées ; les messages transactionnels sont écartés (même prédicat que
// les compteurs commerciaux).

type veridianProspectionStatsRepository struct{ workspaceRepo domain.WorkspaceRepository }

func NewVeridianProspectionStatsRepository(repo domain.WorkspaceRepository) domain.VeridianProspectionStatsRepository {
	return &veridianProspectionStatsRepository{workspaceRepo: repo}
}

// veridianProspectionBounds remplace une borne absente (zéro) par une borne qui ne
// coupe rien, pour garder une requête unique.
func veridianProspectionBounds(since, until time.Time) (time.Time, time.Time) {
	if since.IsZero() {
		since = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if until.IsZero() {
		until = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return since.UTC(), until.UTC()
}

const veridianProspectionAutomationsSQL = `
	SELECT id, name, COALESCE(status, ''), COALESCE(list_id, ''), COALESCE(root_node_id, ''), COALESCE(nodes, '[]'::jsonb)
	FROM automations
	WHERE deleted_at IS NULL
	ORDER BY created_at, id`

// Dernier nœud email dont l'exécution est terminée, par contact. « completed » est
// posé quand le mail est MIS EN FILE : un contact au statut « sending » n'a pas encore
// vu partir ce mail (le domaine le corrige).
const veridianProspectionProgressSQL = `
	SELECT ca.automation_id, COALESCE(ca.status, ''), COALESCE(ca.exit_reason, ''),
	       COALESCE(ca.current_node_id, ''), COALESCE(le.node_id, ''), COUNT(*)
	FROM contact_automations ca
	JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
	LEFT JOIN LATERAL (
		SELECT e.node_id FROM automation_node_executions e
		WHERE e.contact_automation_id = ca.id AND e.node_type = 'email' AND e.action = 'completed'
		ORDER BY e.entered_at DESC LIMIT 1
	) le ON TRUE
	GROUP BY 1, 2, 3, 4, 5`

const veridianProspectionAutomationRepliesSQL = `
	SELECT ca.automation_id,
	       COUNT(DISTINCT r.contact_email) FILTER (WHERE r.reply_type = 'human'),
	       COUNT(DISTINCT r.contact_email) FILTER (WHERE r.reply_type <> 'human')
	FROM veridian_contact_reply r
	JOIN contact_automations ca ON lower(ca.contact_email) = r.contact_email
	JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
	WHERE r.replied_at >= $1 AND r.replied_at < $2
	GROUP BY 1`

// Réponses humaines de la fenêtre, par séquence et par DERNIER nœud email terminé du
// contact dans cette séquence (même LATERAL que veridianProspectionProgressSQL). Le JOIN
// LATERAL est interne : un contact sans aucun mail n'est jamais compté. Le domaine cumule
// ensuite sur les étapes atteintes.
const veridianProspectionStepRepliesSQL = `
	SELECT ca.automation_id, le.node_id, COUNT(DISTINCT r.contact_email)
	FROM veridian_contact_reply r
	JOIN contact_automations ca ON lower(ca.contact_email) = r.contact_email
	JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
	JOIN LATERAL (
		SELECT e.node_id FROM automation_node_executions e
		WHERE e.contact_automation_id = ca.id AND e.node_type = 'email' AND e.action = 'completed'
		ORDER BY e.entered_at DESC LIMIT 1
	) le ON TRUE
	WHERE r.reply_type = 'human' AND r.replied_at >= $1 AND r.replied_at < $2
	GROUP BY 1, 2`

const veridianProspectionAutomationSentSQL = `
	SELECT automation_id, COUNT(DISTINCT contact_email)
	FROM message_history
	WHERE automation_id IS NOT NULL AND sent_at >= $1 AND sent_at < $2` + veridianCommercialRowSQL + `
	GROUP BY 1`

// Dernières réponses humaines : le nom d'entreprise SIRENE du contact et sa séquence la
// plus récente. Lecture seule, bornée par LIMIT.
const veridianProspectionRecentRepliesSQL = `
	SELECT r.contact_email, COALESCE(c.custom_string_1, ''), r.replied_at,
	       COALESCE((SELECT a.name FROM contact_automations ca
	                 JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
	                 WHERE lower(ca.contact_email) = r.contact_email
	                 ORDER BY ca.entered_at DESC LIMIT 1), '')
	FROM veridian_contact_reply r
	LEFT JOIN contacts c ON lower(c.email) = r.contact_email
	WHERE r.reply_type = 'human' AND r.replied_at >= $1 AND r.replied_at < $2
	ORDER BY r.replied_at DESC
	LIMIT 10`

const veridianProspectionListsSQL = `
	SELECT l.id, l.name,
	       COUNT(*) FILTER (WHERE cl.status = 'active'),
	       COUNT(*) FILTER (WHERE cl.status = 'bounced'),
	       COUNT(*) FILTER (WHERE cl.status = 'unsubscribed'),
	       COUNT(*) FILTER (WHERE cl.status = 'complained'),
	       COUNT(*) FILTER (WHERE cl.status = 'active' AND NOT EXISTS (
	           SELECT 1 FROM message_history mh
	           WHERE mh.contact_email = cl.email AND mh.sent_at IS NOT NULL AND mh.veridian_message_type IS DISTINCT FROM 'transactional' AND mh.transactional_notification_id IS NULL))
	FROM lists l
	LEFT JOIN contact_lists cl ON cl.list_id = l.id AND cl.deleted_at IS NULL
	WHERE l.deleted_at IS NULL
	GROUP BY l.id, l.name
	ORDER BY l.name, l.id`

// Stock restant en PERSONNES : actifs jamais contactes, distincts sur toutes les listes
// (un contact dans deux listes compte une fois). Meme definition de « contacte » que la
// requete par liste ci-dessus.
const veridianProspectionStockDistinctSQL = `
	SELECT COUNT(DISTINCT lower(cl.email))
	FROM contact_lists cl
	JOIN lists l ON l.id = cl.list_id AND l.deleted_at IS NULL
	WHERE cl.deleted_at IS NULL AND cl.status = 'active' AND NOT EXISTS (
	    SELECT 1 FROM message_history mh
	    WHERE mh.contact_email = cl.email AND mh.sent_at IS NOT NULL AND mh.veridian_message_type IS DISTINCT FROM 'transactional' AND mh.transactional_notification_id IS NULL)`

const veridianProspectionListRepliesSQL = `
	SELECT cl.list_id,
	       COUNT(DISTINCT r.contact_email) FILTER (WHERE r.reply_type = 'human'),
	       COUNT(DISTINCT r.contact_email) FILTER (WHERE r.reply_type <> 'human')
	FROM veridian_contact_reply r
	JOIN contact_lists cl ON cl.email = r.contact_email AND cl.deleted_at IS NULL
	WHERE r.replied_at >= $1 AND r.replied_at < $2
	GROUP BY 1`

const veridianProspectionListSentSQL = `
	SELECT cl.list_id, COUNT(DISTINCT mh.contact_email)
	FROM message_history mh
	JOIN contact_lists cl ON cl.email = mh.contact_email AND cl.deleted_at IS NULL
	WHERE mh.sent_at >= $1 AND mh.sent_at < $2 AND mh.veridian_message_type IS DISTINCT FROM 'transactional' AND mh.transactional_notification_id IS NULL
	GROUP BY 1`

func (r *veridianProspectionStatsRepository) GetProspectionRaw(ctx context.Context, workspaceID string, since, until time.Time) (*domain.VeridianProspectionRaw, error) {
	db, err := r.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	from, to := veridianProspectionBounds(since, until)
	raw := &domain.VeridianProspectionRaw{}

	// Les lectures se font dans UNE transaction en lecture seule : le même instant
	// pour les séquences, les réponses et le stock.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin read-only transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, veridianProspectionAutomationsSQL)
	if err != nil {
		return nil, fmt.Errorf("query prospection automations: %w", err)
	}
	for rows.Next() {
		var a domain.VeridianProspectionAutomation
		var nodes []byte
		if err := rows.Scan(&a.ID, &a.Name, &a.Status, &a.ListID, &a.RootNodeID, &nodes); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan prospection automation: %w", err)
		}
		if err := json.Unmarshal(nodes, &a.Nodes); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("decode nodes of automation %s: %w", a.ID, err)
		}
		raw.Automations = append(raw.Automations, a)
	}
	if err := veridianCloseRows(rows); err != nil {
		return nil, fmt.Errorf("iterate prospection automations: %w", err)
	}

	rows, err = tx.QueryContext(ctx, veridianProspectionProgressSQL)
	if err != nil {
		return nil, fmt.Errorf("query prospection progress: %w", err)
	}
	for rows.Next() {
		var p domain.VeridianProspectionProgressRow
		if err := rows.Scan(&p.AutomationID, &p.Status, &p.ExitReason, &p.CurrentNodeID, &p.LastEmailNodeID, &p.Count); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan prospection progress: %w", err)
		}
		raw.Progress = append(raw.Progress, p)
	}
	if err := veridianCloseRows(rows); err != nil {
		return nil, fmt.Errorf("iterate prospection progress: %w", err)
	}

	if raw.AutomationReplies, err = queryProspectionReplies(ctx, tx, veridianProspectionAutomationRepliesSQL, from, to); err != nil {
		return nil, err
	}
	if raw.AutomationStepReplies, err = queryProspectionStepReplies(ctx, tx, from, to); err != nil {
		return nil, err
	}
	if raw.AutomationSent, err = queryProspectionCounts(ctx, tx, veridianProspectionAutomationSentSQL, from, to); err != nil {
		return nil, err
	}

	rows, err = tx.QueryContext(ctx, veridianProspectionListsSQL)
	if err != nil {
		return nil, fmt.Errorf("query prospection lists: %w", err)
	}
	for rows.Next() {
		var l domain.VeridianProspectionListRow
		if err := rows.Scan(&l.ID, &l.Name, &l.Active, &l.Bounced, &l.Unsubscribed, &l.Complained, &l.NeverContacted); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan prospection list: %w", err)
		}
		raw.Lists = append(raw.Lists, l)
	}
	if err := veridianCloseRows(rows); err != nil {
		return nil, fmt.Errorf("iterate prospection lists: %w", err)
	}

	var stockDistinct int
	if err := tx.QueryRowContext(ctx, veridianProspectionStockDistinctSQL).Scan(&stockDistinct); err != nil {
		return nil, fmt.Errorf("query distinct prospection stock: %w", err)
	}
	raw.StockDistinct = &stockDistinct

	if raw.ListReplies, err = queryProspectionReplies(ctx, tx, veridianProspectionListRepliesSQL, from, to); err != nil {
		return nil, err
	}
	if raw.ListSent, err = queryProspectionCounts(ctx, tx, veridianProspectionListSentSQL, from, to); err != nil {
		return nil, err
	}

	rows, err = tx.QueryContext(ctx, veridianProspectionRecentRepliesSQL, from, to)
	if err != nil {
		return nil, fmt.Errorf("query recent human replies: %w", err)
	}
	for rows.Next() {
		var rr domain.VeridianRecentReply
		if err := rows.Scan(&rr.ContactEmail, &rr.Company, &rr.RepliedAt, &rr.Sequence); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan recent human reply: %w", err)
		}
		raw.RecentReplies = append(raw.RecentReplies, rr)
	}
	if err := veridianCloseRows(rows); err != nil {
		return nil, fmt.Errorf("iterate recent human replies: %w", err)
	}
	return raw, nil
}

func veridianCloseRows(rows *sql.Rows) error {
	iterErr := rows.Err()
	closeErr := rows.Close()
	if iterErr != nil {
		return iterErr
	}
	return closeErr
}

func queryProspectionReplies(ctx context.Context, tx *sql.Tx, query string, from, to time.Time) ([]domain.VeridianProspectionReplyRow, error) {
	rows, err := tx.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("query prospection replies: %w", err)
	}
	var out []domain.VeridianProspectionReplyRow
	for rows.Next() {
		var r domain.VeridianProspectionReplyRow
		if err := rows.Scan(&r.Key, &r.Human, &r.Auto); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan prospection reply: %w", err)
		}
		out = append(out, r)
	}
	if err := veridianCloseRows(rows); err != nil {
		return nil, fmt.Errorf("iterate prospection replies: %w", err)
	}
	return out, nil
}

func queryProspectionStepReplies(ctx context.Context, tx *sql.Tx, from, to time.Time) ([]domain.VeridianProspectionStepReplyRow, error) {
	rows, err := tx.QueryContext(ctx, veridianProspectionStepRepliesSQL, from, to)
	if err != nil {
		return nil, fmt.Errorf("query prospection step replies: %w", err)
	}
	var out []domain.VeridianProspectionStepReplyRow
	for rows.Next() {
		var r domain.VeridianProspectionStepReplyRow
		if err := rows.Scan(&r.Key, &r.NodeID, &r.Human); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan prospection step reply: %w", err)
		}
		out = append(out, r)
	}
	if err := veridianCloseRows(rows); err != nil {
		return nil, fmt.Errorf("iterate prospection step replies: %w", err)
	}
	return out, nil
}

func queryProspectionCounts(ctx context.Context, tx *sql.Tx, query string, from, to time.Time) ([]domain.VeridianProspectionKeyCount, error) {
	rows, err := tx.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("query prospection sent contacts: %w", err)
	}
	var out []domain.VeridianProspectionKeyCount
	for rows.Next() {
		var c domain.VeridianProspectionKeyCount
		if err := rows.Scan(&c.Key, &c.Count); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan prospection sent contacts: %w", err)
		}
		out = append(out, c)
	}
	if err := veridianCloseRows(rows); err != nil {
		return nil, fmt.Errorf("iterate prospection sent contacts: %w", err)
	}
	return out, nil
}
