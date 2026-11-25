package reservoir

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Notifuse/notifuse/internal/domain"
)

// ContactState : ce qu'une autre app montre d'un contact (fiche 87) : connu, dans quelles listes, bloqué, en séquence, dernier envoi commercial.
type ContactState struct {
	Email     string     `json:"email"`
	Exists    bool       `json:"exists"`
	Lists     []ListIn   `json:"lists"`
	Blocked   string     `json:"blocked,omitempty"` // desinscrit, rebond, plainte : mêmes règles que l'activation
	Replied   bool       `json:"replied"`
	Sequences []Named    `json:"sequences"` // en cours
	LastSent  *time.Time `json:"last_sent,omitempty"`
}

// ListIn : une liste et le statut du contact dedans.
type ListIn struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Named : un objet nommé.
type Named struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ContactStates rend l'état de chaque adresse, dans l'ordre demandé, sans doublon. Mêmes droits que Summary.
func (s *Service) ContactStates(ctx context.Context, ws string, emails []string) ([]ContactState, error) {
	ctx, err := s.authorize(ctx, ws, contactsRead, listsRead, automationsRead, historyRead)
	if err != nil {
		return nil, err
	}
	order, seen := []string{}, map[string]bool{}
	for _, e := range emails {
		if e = domain.NormalizeEmail(e); e != "" && !seen[e] {
			seen[e] = true
			order = append(order, e)
		}
	}
	if len(order) == 0 || len(order) > StatesMax {
		return nil, invalid("emails : 1 à %d adresses", StatesMax)
	}
	return s.Store.ContactStates(ctx, ws, order)
}

// ContactStates lit l'espace.
func (s *Store) ContactStates(ctx context.Context, ws string, emails []string) ([]ContactState, error) {
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return nil, err
	}
	out := make([]ContactState, len(emails))
	at := map[string]*ContactState{}
	for i, e := range emails {
		out[i] = ContactState{Email: e, Lists: []ListIn{}, Sequences: []Named{}}
		at[e] = &out[i]
	}
	q, arg := poolSQL(Source{Emails: emails})
	if err := scan(ctx, db, func(v []string) {
		c := at[v[0]]
		if c == nil {
			return
		}
		c.Exists = true
		switch v[1] {
		case "plainte", "desinscrit", "rebond":
			c.Blocked = v[1]
		}
		c.Replied = v[2] == "true"
	}, 3, fmt.Sprintf(stateCTE, q)+` SELECT email, state, (EXISTS (SELECT 1 FROM veridian_contact_reply r WHERE r.contact_email = st.email AND r.reply_type = 'human'))::text FROM st WHERE $2 = ''`, arg, ""); err != nil {
		return nil, err
	}
	if err := scan(ctx, db, func(v []string) {
		if c := at[v[0]]; c != nil {
			c.Lists = append(c.Lists, ListIn{v[1], v[2], v[3]})
		}
	}, 4, `SELECT cl.email, l.id, l.name, cl.status FROM contact_lists cl JOIN lists l ON l.id = cl.list_id AND l.deleted_at IS NULL
		WHERE cl.email = ANY($1) AND cl.deleted_at IS NULL ORDER BY l.name`, pq.Array(emails)); err != nil {
		return nil, err
	}
	if err := scan(ctx, db, func(v []string) {
		if c := at[v[0]]; c != nil {
			c.Sequences = append(c.Sequences, Named{v[1], v[2]})
		}
	}, 3, `SELECT ca.contact_email, a.id, a.name FROM contact_automations ca JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
		WHERE ca.contact_email = ANY($1) AND ca.status IN ('active', 'sending') ORDER BY a.name`, pq.Array(emails)); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT contact_email, max(sent_at) FROM message_history WHERE contact_email = ANY($1) AND sent_at IS NOT NULL
		AND veridian_message_type IS DISTINCT FROM 'transactional' AND transactional_notification_id IS NULL GROUP BY contact_email`, pq.Array(emails))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e string
		var t sql.NullTime
		if err := rows.Scan(&e, &t); err != nil {
			return nil, err
		}
		if c := at[e]; c != nil && t.Valid {
			u := t.Time.UTC()
			c.LastSent = &u
		}
	}
	return out, rows.Err()
}
