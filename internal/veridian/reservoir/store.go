package reservoir

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Connector : la base d'un espace.
type Connector interface {
	GetConnection(ctx context.Context, workspaceID string) (*sql.DB, error)
}

// Store : lectures et écritures dans la base de l'espace.
type Store struct{ WS Connector }

// blockedSQL : adresses du tableau $1 à ne plus contacter, avec la raison. Désinscription, plainte et rebond valent pour
// TOUT l'espace, quelle que soit la liste ; un rebond temporaire (soft) ou de politique ne bloque pas.
const blockedSQL = `
	SELECT email, reason FROM (
		SELECT cl.email, CASE cl.status WHEN 'complained' THEN 'plainte' WHEN 'unsubscribed' THEN 'desinscrit' ELSE 'rebond' END AS reason
		FROM contact_lists cl WHERE cl.email = ANY($1) AND cl.status IN ('unsubscribed', 'bounced', 'complained')
		UNION ALL
		SELECT mh.contact_email, CASE WHEN mh.complained_at IS NOT NULL THEN 'plainte' WHEN mh.unsubscribed_at IS NOT NULL THEN 'desinscrit' ELSE 'rebond' END
		FROM message_history mh WHERE mh.contact_email = ANY($1)
		  AND (mh.complained_at IS NOT NULL OR mh.unsubscribed_at IS NOT NULL OR (mh.bounced_at IS NOT NULL AND mh.bounce_type ILIKE 'hard%'))
	) b ORDER BY CASE reason WHEN 'plainte' THEN 0 WHEN 'desinscrit' THEN 1 ELSE 2 END`

func blocked(ctx context.Context, db *sql.DB, emails []string) (map[string]string, error) {
	out := map[string]string{}
	rows, err := db.QueryContext(ctx, blockedSQL, pq.Array(emails))
	if err != nil {
		return nil, fmt.Errorf("adresses bloquées : %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e, r string
		if err := rows.Scan(&e, &r); err != nil {
			return nil, err
		}
		if out[e] == "" {
			out[e] = r
		}
	}
	return out, rows.Err()
}

// Known lit tout ce qu'il faut pour décider d'un lot.
func (s *Store) Known(ctx context.Context, ws string, rows []Row, d Dedupe) (Known, error) {
	k := Known{Emails: map[string]bool{}, Sirens: map[string]string{}, Domains: map[string]string{}, Blocked: map[string]string{}}
	var emails, sirens, domains []string
	for _, r := range rows {
		emails = append(emails, r.Email)
		if d.Siren && r.Siren != "" {
			sirens = append(sirens, r.Siren)
		}
		if d.Domain {
			domains = append(domains, r.Domains...)
		}
	}
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return k, err
	}
	if k.Blocked, err = blocked(ctx, db, emails); err != nil {
		return k, err
	}
	if err := scan(ctx, db, func(v []string) { k.Emails[v[0]] = true }, 1, `SELECT email FROM contacts WHERE email = ANY($1)`, pq.Array(emails)); err != nil {
		return k, err
	}
	if len(sirens) > 0 {
		// Le SIREN vit dans custom_json_N.siren ou dans external_id selon qui a importé.
		if err := scan(ctx, db, func(v []string) { k.Sirens[v[0]] = v[1] }, 2, `
			SELECT s, email FROM (
				SELECT c.email, unnest(ARRAY[c.custom_json_1->>'siren', c.custom_json_2->>'siren', c.custom_json_3->>'siren', c.custom_json_4->>'siren', c.custom_json_5->>'siren', c.external_id]) AS s
				FROM contacts c) x WHERE s = ANY($1)`, pq.Array(sirens)); err != nil {
			return k, err
		}
	}
	if len(domains) > 0 {
		if err := scan(ctx, db, func(v []string) { k.Domains[v[0]] = v[1] }, 2, `
			SELECT d, email FROM (
				SELECT c.email, unnest(ARRAY[split_part(c.email, '@', 2), c.custom_json_1->>'domaine', c.custom_json_1->>'domain', lower(c.external_id)]) AS d
				FROM contacts c) x WHERE d = ANY($1)`, pq.Array(domains)); err != nil {
			return k, err
		}
	}
	return k, nil
}

// querier : base ou transaction.
type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

func scan(ctx context.Context, db querier, each func([]string), cols int, q string, args ...any) error {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		v := make([]string, cols)
		ptr := make([]any, cols)
		for i := range v {
			ptr[i] = &v[i]
		}
		if err := rows.Scan(ptr...); err != nil {
			return err
		}
		each(v)
	}
	return rows.Err()
}

// Stored : ce qu'une mise à jour doit préserver.
type Stored struct {
	JSON1   json.RawMessage
	Created time.Time
}

// Existing lit custom_json_1 et la date de création des fiches à compléter.
func (s *Store) Existing(ctx context.Context, ws string, emails []string) (map[string]Stored, error) {
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT email, COALESCE(custom_json_1::text, ''), created_at FROM contacts WHERE email = ANY($1)`, pq.Array(emails))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Stored{}
	for rows.Next() {
		var e, j string
		var st Stored
		if err := rows.Scan(&e, &j, &st.Created); err != nil {
			return nil, err
		}
		st.JSON1 = json.RawMessage(j)
		out[e] = st
	}
	return out, rows.Err()
}

// MissingLists : les listes demandées qui n'existent pas.
func (s *Store) MissingLists(ctx context.Context, ws string, ids []string) ([]string, error) {
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	if err := scan(ctx, db, func(v []string) { found[v[0]] = true }, 1, `SELECT id FROM lists WHERE id = ANY($1) AND deleted_at IS NULL`, pq.Array(ids)); err != nil {
		return nil, err
	}
	var miss []string
	for _, id := range ids {
		if !found[id] {
			miss = append(miss, id)
		}
	}
	return miss, nil
}

// Automation : ce qu'il faut savoir d'une séquence avant d'écrire.
type Automation struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Event  string `json:"event_kind"`
	ListID string `json:"trigger_list_id,omitempty"`
	status string
	root   string
	list   string // liste de la séquence (garde d'envoi)
	freq   string
}

const automationCols = `id, name, COALESCE(trigger_config->>'event_kind', ''), COALESCE(trigger_config->>'list_id', ''), COALESCE(status, ''), COALESCE(root_node_id, ''), COALESCE(list_id, ''), COALESCE(trigger_config->>'frequency', 'every_time')`

func (a *Automation) dests() []any {
	return []any{&a.ID, &a.Name, &a.Event, &a.ListID, &a.status, &a.root, &a.list, &a.freq}
}

// LiveTriggers : séquences actives qu'un import peut démarrer tout seul : inscription aux listes visées, création de contact, fiche
// complétée, et tout segment (une fiche ou une liste neuve peut faire entrer un contact dans un segment, recalculé après coup).
func (s *Store) LiveTriggers(ctx context.Context, ws string, listIDs []string, creates, updates bool) ([]Automation, error) {
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+automationCols+` FROM automations
		WHERE status = 'live' AND deleted_at IS NULL
		  AND ((trigger_config->>'event_kind' = 'list.subscribed' AND trigger_config->>'list_id' = ANY($1)) OR ($2 AND trigger_config->>'event_kind' = 'contact.created')
		    OR ($3 AND trigger_config->>'event_kind' = 'contact.updated') OR trigger_config->>'event_kind' = 'segment.joined')
		ORDER BY name`, pq.Array(listIDs), creates, updates)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Automation{}
	for rows.Next() {
		var a Automation
		if err := rows.Scan(a.dests()...); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Automation lit une séquence. nil : inconnue.
func (s *Store) Automation(ctx context.Context, ws, id string) (*Automation, error) {
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return nil, err
	}
	var a Automation
	err = db.QueryRowContext(ctx, `SELECT `+automationCols+` FROM automations WHERE id = $1 AND deleted_at IS NULL`, id).Scan(a.dests()...)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// TransactionalTemplates : modèles de catégorie « transactionnel » parmi les nœuds e-mail d'une séquence (dernière version).
func (s *Store) TransactionalTemplates(ctx context.Context, ws, automationID string) ([]string, error) {
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return nil, err
	}
	var out []string
	err = scan(ctx, db, func(v []string) { out = append(out, v[0]) }, 1, `
		SELECT DISTINCT t.id FROM automations a
		CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(a.nodes) = 'array' THEN a.nodes ELSE '[]'::jsonb END) n
		JOIN LATERAL (SELECT id, category FROM templates WHERE id = n->'config'->>'template_id' AND deleted_at IS NULL ORDER BY version DESC LIMIT 1) t ON true
		WHERE a.id = $1 AND n->>'type' = 'email' AND t.category = 'transactional'`, automationID)
	return out, err
}

// subscribeSQL : jamais de réinscription d'un désinscrit, d'un rebond ou d'une plainte, et rien n'est touché si la ligne est
// déjà active (pas de second événement « inscription »).
const subscribeSQL = `
	INSERT INTO contact_lists (email, list_id, status, created_at, updated_at, deleted_at)
	SELECT e, l, 'active', NOW(), NOW(), NULL FROM unnest($1::text[]) e CROSS JOIN unnest($2::text[]) l
	ON CONFLICT (email, list_id) DO UPDATE SET status = 'active', updated_at = NOW(), deleted_at = NULL
	WHERE contact_lists.status NOT IN ('unsubscribed', 'bounced', 'complained') AND (contact_lists.status <> 'active' OR contact_lists.deleted_at IS NOT NULL)`

// Subscribe inscrit des adresses à des listes ; rend le nombre de lignes écrites.
func (s *Store) Subscribe(ctx context.Context, ws string, emails, lists []string) (int, error) {
	if len(emails) == 0 || len(lists) == 0 {
		return 0, nil
	}
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, subscribeSQL, pq.Array(emails), pq.Array(lists))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// Source : d'où viennent les contacts à activer. Un seul des trois.
type Source struct {
	ListID    string
	SegmentID string
	Emails    []string
}

// poolSQL : contacts d'une source (liste : toute la liste hors inscription à confirmer, les bloqués y sont décomptés), avec leur état. Les conditions d'écart sont calculées ici et décomptées, jamais muettes.
func poolSQL(src Source) (string, any) {
	switch {
	case src.ListID != "":
		return `SELECT cl.email FROM contact_lists cl WHERE cl.list_id = $1 AND cl.status <> 'pending' AND cl.deleted_at IS NULL`, src.ListID
	case src.SegmentID != "":
		return `SELECT cs.email FROM contact_segments cs JOIN segments sg ON sg.id = cs.segment_id AND sg.version = cs.version WHERE cs.segment_id = $1`, src.SegmentID
	}
	return `SELECT e AS email FROM unnest($1::text[]) e`, pq.Array(src.Emails)
}

// Pool : décompte d'une source, par état. Chaque contact n'est compté qu'une fois, dans l'ordre des raisons.
type Pool struct {
	Total        int `json:"total"`
	Available    int `json:"available"`
	Unsubscribed int `json:"unsubscribed"`
	Bounced      int `json:"bounced"`
	Complained   int `json:"complained"`
	Replied      int `json:"replied"`
	Already      int `json:"already_in_automation"`
	InSequence   int `json:"in_another_sequence"`
	Contacted    int `json:"already_contacted"`
}

const stateCTE = `
	WITH pool AS (%s), p AS (SELECT DISTINCT c.email, c.custom_number_1, c.created_at FROM pool JOIN contacts c ON c.email = pool.email),
	bl AS (SELECT DISTINCT ON (email) email, reason FROM (
		SELECT cl.email, CASE cl.status WHEN 'complained' THEN 'plainte' WHEN 'unsubscribed' THEN 'desinscrit' ELSE 'rebond' END AS reason
		FROM contact_lists cl JOIN p ON p.email = cl.email WHERE cl.status IN ('unsubscribed', 'bounced', 'complained')
		UNION ALL
		SELECT mh.contact_email, CASE WHEN mh.complained_at IS NOT NULL THEN 'plainte' WHEN mh.unsubscribed_at IS NOT NULL THEN 'desinscrit' ELSE 'rebond' END
		FROM message_history mh JOIN p ON p.email = mh.contact_email
		WHERE mh.complained_at IS NOT NULL OR mh.unsubscribed_at IS NOT NULL OR (mh.bounced_at IS NOT NULL AND mh.bounce_type ILIKE 'hard%%')
	) b ORDER BY email, CASE reason WHEN 'plainte' THEN 0 WHEN 'desinscrit' THEN 1 ELSE 2 END),
	st AS (SELECT p.email, p.custom_number_1, p.created_at,
		CASE WHEN bl.reason IS NOT NULL THEN bl.reason
		     WHEN EXISTS (SELECT 1 FROM veridian_contact_reply r WHERE r.contact_email = p.email AND r.reply_type = 'human') THEN 'replied'
		     WHEN $2 <> '' AND EXISTS (SELECT 1 FROM contact_automations ca WHERE ca.contact_email = p.email AND ca.automation_id = $2) THEN 'already'
		     WHEN EXISTS (SELECT 1 FROM contact_automations ca WHERE ca.contact_email = p.email AND ca.automation_id <> $2 AND ca.status IN ('active', 'sending')) THEN 'in_sequence'
		     WHEN EXISTS (SELECT 1 FROM message_history mh WHERE mh.contact_email = p.email AND mh.sent_at IS NOT NULL AND mh.veridian_message_type IS DISTINCT FROM 'transactional' AND mh.transactional_notification_id IS NULL) THEN 'contacted'
		     ELSE 'ok' END AS state
		FROM p LEFT JOIN bl ON bl.email = p.email)`

// Inspect décompte une source pour une séquence (automationID vide : sans séquence).
func (s *Store) Inspect(ctx context.Context, ws string, src Source, automationID string) (Pool, error) {
	var p Pool
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return p, err
	}
	q, arg := poolSQL(src)
	rows, err := db.QueryContext(ctx, fmt.Sprintf(stateCTE, q)+` SELECT state, count(*) FROM st GROUP BY state`, arg, automationID)
	if err != nil {
		return p, err
	}
	defer func() { _ = rows.Close() }()
	dst := map[string]*int{"ok": &p.Available, "plainte": &p.Complained, "desinscrit": &p.Unsubscribed, "rebond": &p.Bounced,
		"replied": &p.Replied, "already": &p.Already, "in_sequence": &p.InSequence, "contacted": &p.Contacted}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return p, err
		}
		if d := dst[st]; d != nil {
			*d = n
		}
		p.Total += n
	}
	return p, rows.Err()
}

// Pick choisit les contacts à activer : le meilleur score d'abord (custom_number_1), puis le plus ancien.
func (s *Store) Pick(ctx context.Context, ws string, src Source, automationID string, limit int, includeContacted bool) ([]string, error) {
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return nil, err
	}
	return pickFrom(ctx, db, src, automationID, limit, includeContacted)
}

func pickFrom(ctx context.Context, db querier, src Source, automationID string, limit int, includeContacted bool) ([]string, error) {
	q, arg := poolSQL(src)
	out := []string{}
	err := scan(ctx, db, func(v []string) { out = append(out, v[0]) }, 1,
		fmt.Sprintf(stateCTE, q)+`
		SELECT email FROM st WHERE state = 'ok' OR ($4 AND state = 'contacted') ORDER BY custom_number_1 DESC NULLS LAST, created_at, email LIMIT $3`,
		arg, automationID, limit, includeContacted)
	return out, err
}

// Activate inscrit les contacts dans la séquence, en une transaction : liste de la séquence d'abord (la garde d'envoi l'exige,
// et son déclencheur inscrit souvent lui-même), puis inscription directe de ceux qui n'y sont pas. Rend les adresses inscrites.
// batchID non vide : le lot est consigné dans la même transaction, un rejeu rend le résultat d'origine (replayed) sans rien inscrire.
func (s *Store) Activate(ctx context.Context, ws string, a *Automation, batchID string, pick func(querier) ([]string, error)) (done []string, selected int, replayed bool, err error) {
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return nil, 0, false, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Deux activations simultanées ne doivent pas inscrire deux fois le même contact, ni rejouer deux fois le même lot.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('veridian_reservoir:' || $1))`, a.ID); err != nil {
		return nil, 0, false, err
	}
	if batchID != "" {
		err := tx.QueryRowContext(ctx, `SELECT emails, selected FROM veridian_reservoir_batch WHERE automation_id = $1 AND batch_id = $2`, a.ID, batchID).Scan(pq.Array(&done), &selected)
		if err == nil {
			return done, selected, true, nil
		}
		if err != sql.ErrNoRows {
			return nil, 0, false, err
		}
	}
	// Choix DANS la transaction, sous le verrou : deux lots simultanés ne choisissent pas les mêmes contacts.
	emails, err := pick(tx)
	if err != nil {
		return nil, 0, false, err
	}
	if a.list != "" {
		if _, err := tx.ExecContext(ctx, subscribeSQL, pq.Array(emails), pq.Array([]string{a.list})); err != nil {
			return nil, 0, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		SELECT automation_enroll_contact($1, e, $2, $3) FROM unnest($4::text[]) e
		WHERE NOT EXISTS (SELECT 1 FROM contact_automations ca WHERE ca.automation_id = $1 AND ca.contact_email = e)
		  AND ($5 = '' OR EXISTS (SELECT 1 FROM contact_lists cl WHERE cl.email = e AND cl.list_id = $5 AND cl.status = 'active' AND cl.deleted_at IS NULL))`,
		a.ID, a.root, a.freq, pq.Array(emails), a.list); err != nil {
		return nil, 0, false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(array_agg(DISTINCT contact_email::text), '{}') FROM contact_automations WHERE automation_id = $1 AND contact_email = ANY($2) AND status = 'active'`,
		a.ID, pq.Array(emails)).Scan(pq.Array(&done)); err != nil {
		return nil, 0, false, err
	}
	if batchID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO veridian_reservoir_batch (automation_id, batch_id, emails, selected) VALUES ($1, $2, $3, $4)`, a.ID, batchID, pq.Array(done), len(emails)); err != nil {
			return nil, 0, false, err
		}
	}
	return done, len(emails), false, tx.Commit()
}

// ListRef : une liste de l'espace.
type ListRef struct{ ID, Name string }

// Lists : les listes de l'espace.
func (s *Store) Lists(ctx context.Context, ws string) ([]ListRef, error) {
	db, err := s.WS.GetConnection(ctx, ws)
	if err != nil {
		return nil, err
	}
	var out []ListRef
	err = scan(ctx, db, func(v []string) { out = append(out, ListRef{v[0], v[1]}) }, 2, `SELECT id, name FROM lists WHERE deleted_at IS NULL ORDER BY name LIMIT 200`)
	return out, err
}

// Contacts : écriture des fiches (dépôt upstream, dont le garde VER-220).
type Contacts interface {
	BulkUpsertContacts(ctx context.Context, workspaceID string, contacts []*domain.Contact) ([]domain.BulkUpsertResult, error)
}
