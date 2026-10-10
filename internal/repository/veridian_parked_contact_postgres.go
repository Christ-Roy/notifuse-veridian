package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// ListOrphanParked rend les contacts en 'sending' sans aucune entree de file pour leur
// paire (automation, contact), apres un delai de GRACE. La grace couvre la fenetre
// entre « ligne de file supprimee par un envoi reussi » et « message_history ecrit +
// callback » : sans elle on prendrait un envoi en cours pour un orphelin et on le
// rejouerait (mail en double). Le delai se mesure depuis la mise en file du mail (sortie
// « queued » du noeud), sinon depuis l'entree du contact.
func (r *AutomationRepository) ListOrphanParked(ctx context.Context, workspaceID string, grace time.Duration, limit int) ([]domain.VeridianParkedContact, error) {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT ca.id, ca.automation_id, ca.contact_email, COALESCE(ca.current_node_id, ''),
		       COALESCE(ne.message_id, ''),
		       mh.id IS NOT NULL, COALESCE(mh.sent_at IS NOT NULL, false), COALESCE(mh.failed_at IS NOT NULL AND mh.sent_at IS NULL, false),
		       EXISTS (SELECT 1 FROM message_history x WHERE x.contact_email = ca.contact_email AND x.sent_at IS NOT NULL
		                     AND x.automation_id IS DISTINCT FROM ca.automation_id)
		FROM contact_automations ca
		LEFT JOIN LATERAL (
			SELECT x.output->>'message_id' AS message_id, x.entered_at
			FROM automation_node_executions x
			WHERE x.contact_automation_id = ca.id AND x.node_id = ca.current_node_id
			  AND x.output->>'queued' = 'true'
			ORDER BY x.entered_at DESC LIMIT 1
		) ne ON true
		LEFT JOIN message_history mh ON mh.id = ne.message_id
		WHERE ca.status = 'sending'
		  AND NOT EXISTS (SELECT 1 FROM email_queue e
		                  WHERE e.source_type = 'automation' AND e.source_id = ca.automation_id
		                    AND e.contact_email = ca.contact_email)
		  AND COALESCE(ne.entered_at, ca.entered_at) < NOW() - make_interval(secs => $1)
		ORDER BY ca.entered_at
		LIMIT $2`, grace.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list orphan parked contacts: %w", err)
	}
	defer rows.Close()
	var out []domain.VeridianParkedContact
	for rows.Next() {
		var p domain.VeridianParkedContact
		var found sql.NullBool
		if err := rows.Scan(&p.ContactAutomationID, &p.AutomationID, &p.ContactEmail, &p.NodeID, &p.MessageID,
			&found, &p.MessageSent, &p.MessageFailed, &p.AlreadyContacted); err != nil {
			return nil, fmt.Errorf("failed to scan orphan parked contact: %w", err)
		}
		p.MessageFound = found.Valid && found.Bool
		out = append(out, p)
	}
	return out, rows.Err()
}
