package mailbox

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/pkg/crypto"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ErrNotFound : message ou fil introuvable.
var ErrNotFound = errors.New("introuvable")

// DBProvider rend la base d'un workspace.
type DBProvider func(ctx context.Context, workspaceID string) (*sql.DB, error)

// SecretProvider rend la clé de chiffrement d'un workspace (comme message_data).
type SecretProvider func(ctx context.Context, workspaceID string) (string, error)

// Store persiste la boîte unifiée dans la base de chaque workspace.
type Store struct {
	db     DBProvider
	secret SecretProvider
}

// NewStore construit le store.
func NewStore(db DBProvider, secret SecretProvider) *Store { return &Store{db: db, secret: secret} }

const msgColumns = `id, seq, workspace_id, direction, kind, profile_id, mailbox_integration_id, mailbox_folder,
	mailbox_uid_validity, mailbox_uid, message_id, in_reply_to, references_ids, thread_id, contact_email, from_addr,
	from_name, to_addrs, cc_addrs, subject, sent_at, received_at, occurred_at, headers, body_text, body_html_sanitized,
	raw_eml, body_source, size_bytes, truncated, attachments, dsn, link, sent_ref, handled_at, handled_by, labels,
	created_at, updated_at`

// summaryColumns : sans corps ni message brut (le corps texte sert à l'aperçu, déchiffré à la lecture).
const summaryColumns = `id, seq, direction, kind, profile_id, thread_id, contact_email, from_addr, to_addrs, subject,
	body_text, occurred_at, sent_at, received_at, attachments, dsn, handled_at, handled_by, labels`

func (s *Store) seal(ws, plain string) (any, error) {
	if plain == "" {
		return nil, nil
	}
	key, err := s.secret(context.Background(), ws)
	if err != nil {
		return nil, err
	}
	return crypto.EncryptString(plain, key)
}

func (s *Store) open(ws string, enc sql.NullString) (string, error) {
	if !enc.Valid || enc.String == "" {
		return "", nil
	}
	key, err := s.secret(context.Background(), ws)
	if err != nil {
		return "", err
	}
	return crypto.DecryptFromHexString(enc.String, key)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// RefHit : un message déjà connu cité par un autre.
type RefHit struct {
	ID           string
	ThreadID     string
	MessageID    string
	Direction    Direction
	ContactEmail string
	ProfileID    string
	SentRef      string
	Link         *Link
	OccurredAt   time.Time
}

// SentHit : un envoi de message_history cité par un entrant.
type SentHit struct {
	ID           string
	ContactEmail string
	AutomationID string
	BroadcastID  string
	ProfileID    string
	SentAt       *time.Time
}

// ---------- écriture ----------

func (s *Store) args(m *Message) ([]any, error) {
	enc := func(v string) (any, error) { return s.seal(m.WorkspaceID, v) }
	bt, err := enc(m.BodyText)
	if err != nil {
		return nil, err
	}
	bh, err := enc(m.BodyHTMLSanitized)
	if err != nil {
		return nil, err
	}
	var raw any
	if len(m.RawEML) > 0 {
		if raw, err = enc(string(m.RawEML)); err != nil {
			return nil, err
		}
	}
	hdr, _ := json.Marshal(nonNilHeaders(m.Headers))
	att, _ := json.Marshal(nonNilAtt(m.Attachments))
	var dsn, link any
	if m.DSN != nil {
		b, _ := json.Marshal(m.DSN)
		dsn = string(b)
	}
	if m.Link != nil {
		b, _ := json.Marshal(m.Link)
		link = string(b)
	}
	var mi, mf, mv, mu any
	if m.Mailbox != nil {
		mi, mf, mv, mu = m.Mailbox.IntegrationID, m.Mailbox.Folder, int64(m.Mailbox.UIDValidity), int64(m.Mailbox.UID)
	}
	return []any{
		m.ID, m.WorkspaceID, string(m.Direction), string(m.Kind), nullStr(m.ProfileID), mi, mf, mv, mu,
		nullStr(m.MessageID), nullStr(m.InReplyTo), pq.Array(nonNilStr(m.References)), m.ThreadID,
		strings.ToLower(m.ContactEmail), m.From, m.FromName, pq.Array(nonNilStr(m.To)), pq.Array(nonNilStr(m.Cc)),
		m.Subject, m.SentAt, m.ReceivedAt, m.OccurredAt, string(hdr), bt, bh, raw, m.BodySource, m.SizeBytes,
		m.Truncated, string(att), dsn, link, nullStr(m.SentRef), m.HandledAt, nullStr(m.HandledBy),
		pq.Array(nonNilStr(m.Labels)),
	}, nil
}

const insertCols = `id, workspace_id, direction, kind, profile_id, mailbox_integration_id, mailbox_folder,
	mailbox_uid_validity, mailbox_uid, message_id, in_reply_to, references_ids, thread_id, contact_email, from_addr,
	from_name, to_addrs, cc_addrs, subject, sent_at, received_at, occurred_at, headers, body_text, body_html_sanitized,
	raw_eml, body_source, size_bytes, truncated, attachments, dsn, link, sent_ref, handled_at, handled_by, labels`

const insertPlaceholders = `$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23::jsonb,$24,$25,$26,$27,$28,$29,$30::jsonb,$31::jsonb,$32::jsonb,$33,$34,$35,$36`

// InsertInbound enregistre un mail reçu. Idempotent : la même boîte, le même UID (ou le même
// Message-ID dans la même boîte, si l'UIDVALIDITY a changé) ne crée pas de doublon.
// inserted=false : déjà présent.
func (s *Store) InsertInbound(ctx context.Context, m *Message) (bool, error) {
	db, err := s.db(ctx, m.WorkspaceID)
	if err != nil {
		return false, err
	}
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	if m.Mailbox != nil && m.MessageID != "" {
		// UIDVALIDITY changé : le même mail relu sous un nouvel UID met à jour le repère.
		res, err := db.ExecContext(ctx, `UPDATE veridian_mail_message SET mailbox_uid_validity=$1, mailbox_uid=$2, updated_at=NOW()
			WHERE mailbox_integration_id=$3 AND mailbox_folder=$4 AND message_id=$5 AND direction='in'
			AND (mailbox_uid_validity IS DISTINCT FROM $1)`,
			int64(m.Mailbox.UIDValidity), int64(m.Mailbox.UID), m.Mailbox.IntegrationID, m.Mailbox.Folder, m.MessageID)
		if err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				return false, nil
			}
		}
	}
	args, err := s.args(m)
	if err != nil {
		return false, err
	}
	res, err := db.ExecContext(ctx, `INSERT INTO veridian_mail_message (`+insertCols+`) VALUES (`+insertPlaceholders+`)
		ON CONFLICT (mailbox_integration_id, mailbox_folder, mailbox_uid_validity, mailbox_uid) WHERE mailbox_uid IS NOT NULL DO NOTHING`, args...)
	if err != nil {
		return false, fmt.Errorf("insertion du message reçu: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_ = db.QueryRowContext(ctx, `SELECT seq, created_at, updated_at FROM veridian_mail_message WHERE id=$1`, m.ID).Scan(&m.Seq, &m.CreatedAt, &m.UpdatedAt)
	}
	return n > 0, nil
}

// UpsertSent enregistre un mail sortant. sent_ref est la clé d'idempotence. Une capture
// (corps exact) remplace une reconstitution ; une reconstitution ne remplace jamais rien.
func (s *Store) UpsertSent(ctx context.Context, m *Message) (bool, error) {
	db, err := s.db(ctx, m.WorkspaceID)
	if err != nil {
		return false, err
	}
	if m.SentRef == "" {
		return false, errors.New("sent_ref requis pour un mail sortant")
	}
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	args, err := s.args(m)
	if err != nil {
		return false, err
	}
	conflict := `DO NOTHING`
	if m.BodySource == BodyCaptured {
		conflict = `DO UPDATE SET subject=EXCLUDED.subject, from_addr=EXCLUDED.from_addr, from_name=EXCLUDED.from_name,
			to_addrs=EXCLUDED.to_addrs, cc_addrs=EXCLUDED.cc_addrs, message_id=EXCLUDED.message_id,
			in_reply_to=EXCLUDED.in_reply_to, references_ids=EXCLUDED.references_ids, headers=EXCLUDED.headers,
			body_text=EXCLUDED.body_text, body_html_sanitized=EXCLUDED.body_html_sanitized, body_source=EXCLUDED.body_source,
			size_bytes=EXCLUDED.size_bytes, attachments=EXCLUDED.attachments, link=EXCLUDED.link,
			profile_id=COALESCE(EXCLUDED.profile_id, veridian_mail_message.profile_id), updated_at=NOW()
			WHERE veridian_mail_message.body_source <> 'captured'`
	}
	res, err := db.ExecContext(ctx, `INSERT INTO veridian_mail_message (`+insertCols+`) VALUES (`+insertPlaceholders+`)
		ON CONFLICT (sent_ref) WHERE sent_ref IS NOT NULL `+conflict, args...)
	if err != nil {
		return false, fmt.Errorf("insertion du message envoyé: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Rethread rattache à threadID les fils des messages qui citent messageID et appartiennent à
// un autre fil (le parent est arrivé après ses enfants). Retourne le nombre de lignes déplacées.
func (s *Store) Rethread(ctx context.Context, ws, threadID, messageID, sentRef string) (int64, error) {
	if messageID == "" && sentRef == "" {
		return 0, nil
	}
	db, err := s.db(ctx, ws)
	if err != nil {
		return 0, err
	}
	cites := []string{}
	if messageID != "" {
		cites = append(cites, messageID)
	}
	if bare := LocalPart(messageID); bare != "" {
		cites = append(cites, "<"+bare+">")
	}
	res, err := db.ExecContext(ctx, `UPDATE veridian_mail_message SET thread_id=$1, updated_at=NOW()
		WHERE thread_id IN (
			SELECT DISTINCT thread_id FROM veridian_mail_message
			WHERE thread_id <> $1 AND (in_reply_to = ANY($2) OR references_ids && $2::text[])
		)`, threadID, pq.Array(cites))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------- recherche de références ----------

// FindByIDs retrouve les messages déjà stockés cités par message_id (avec chevrons) ou par
// sent_ref (id message_history). Le plus ancien d'abord.
func (s *Store) FindByIDs(ctx context.Context, ws string, messageIDs, sentRefs []string) ([]RefHit, error) {
	if len(messageIDs) == 0 && len(sentRefs) == 0 {
		return nil, nil
	}
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, thread_id, COALESCE(message_id,''), direction, contact_email, COALESCE(profile_id,''),
		COALESCE(sent_ref,''), link, occurred_at FROM veridian_mail_message
		WHERE message_id = ANY($1) OR sent_ref = ANY($2) ORDER BY occurred_at ASC LIMIT 20`,
		pq.Array(nonNilStr(messageIDs)), pq.Array(nonNilStr(sentRefs)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefHit
	for rows.Next() {
		var h RefHit
		var dir string
		var link []byte
		if err := rows.Scan(&h.ID, &h.ThreadID, &h.MessageID, &dir, &h.ContactEmail, &h.ProfileID, &h.SentRef, &link, &h.OccurredAt); err != nil {
			return nil, err
		}
		h.Direction = Direction(dir)
		if len(link) > 0 {
			var l Link
			if json.Unmarshal(link, &l) == nil {
				h.Link = &l
			}
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// FindThreadBySubject cherche un fil existant du même contact et du même objet normalisé
// (repli quand le mail ne cite aucune référence), sur les 90 derniers jours.
func (s *Store) FindThreadBySubject(ctx context.Context, ws, contact, subject string, around time.Time) (string, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return "", err
	}
	norm := NormalizeSubject(subject)
	if norm == "" {
		return "", nil
	}
	rows, err := db.QueryContext(ctx, `SELECT thread_id, subject FROM veridian_mail_message
		WHERE contact_email=$1 AND occurred_at BETWEEN $2 AND $3 ORDER BY occurred_at DESC LIMIT 50`,
		strings.ToLower(contact), around.Add(-90*24*time.Hour), around.Add(24*time.Hour))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, subj string
		if err := rows.Scan(&tid, &subj); err != nil {
			return "", err
		}
		if NormalizeSubject(subj) == norm {
			return tid, nil
		}
	}
	return "", rows.Err()
}

// FindSent lit les envois message_history cités (ids sous toutes leurs formes stockées).
func (s *Store) FindSent(ctx context.Context, ws string, storedIDs []string) ([]SentHit, error) {
	if len(storedIDs) == 0 {
		return nil, nil
	}
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, contact_email, COALESCE(automation_id,''), COALESCE(broadcast_id,''),
		COALESCE(veridian_profile_id,''), sent_at FROM message_history WHERE id = ANY($1) LIMIT 10`, pq.Array(storedIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SentHit
	for rows.Next() {
		var h SentHit
		var sent sql.NullTime
		if err := rows.Scan(&h.ID, &h.ContactEmail, &h.AutomationID, &h.BroadcastID, &h.ProfileID, &sent); err != nil {
			return nil, err
		}
		if sent.Valid {
			t := sent.Time
			h.SentAt = &t
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ContactExists dit si l'adresse est un contact du workspace.
func (s *Store) ContactExists(ctx context.Context, ws, email string) (bool, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return false, err
	}
	var one int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM contacts WHERE email=$1 LIMIT 1`, strings.ToLower(email)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Suppressed dit si le contact ne doit plus recevoir de courrier : désinscrit, plainte ou adresse
// morte (statut de liste), ou rebond définitif reçu dans la boîte unifiée.
func (s *Store) Suppressed(ctx context.Context, ws, email string) (bool, string, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return false, "", err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	var st sql.NullString
	err = db.QueryRowContext(ctx, `SELECT status FROM contact_lists WHERE email=$1 AND status IN ('unsubscribed','bounced','complained') LIMIT 1`, email).Scan(&st)
	if err == nil {
		switch st.String {
		case "unsubscribed":
			return true, "désinscrit", nil
		case "complained":
			return true, "a signalé le courrier comme indésirable", nil
		}
		return true, "adresse en rebond", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, "", err
	}
	var one int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM veridian_mail_message WHERE contact_email=$1 AND kind='bounce_hard' LIMIT 1`, email).Scan(&one)
	if err == nil {
		return true, "rebond définitif reçu", nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	return false, "", err
}

// ---------- lecture ----------

func scanFull(row interface{ Scan(...any) error }, s *Store) (*Message, error) {
	var (
		m                                    Message
		dir, kind                            string
		profile, mi, mf, mid, irt, sref, hby sql.NullString
		mv, mu                               sql.NullInt64
		refs, to, cc, labels                 []string
		sent, recv                           sql.NullTime
		headers, att, dsn, link              []byte
		bt, bh, raw                          sql.NullString
		handled                              sql.NullTime
	)
	if err := row.Scan(&m.ID, &m.Seq, &m.WorkspaceID, &dir, &kind, &profile, &mi, &mf, &mv, &mu, &mid, &irt, pq.Array(&refs),
		&m.ThreadID, &m.ContactEmail, &m.From, &m.FromName, pq.Array(&to), pq.Array(&cc), &m.Subject, &sent, &recv,
		&m.OccurredAt, &headers, &bt, &bh, &raw, &m.BodySource, &m.SizeBytes, &m.Truncated, &att, &dsn, &link, &sref,
		&handled, &hby, pq.Array(&labels), &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.Direction, m.Kind = Direction(dir), Kind(kind)
	m.ProfileID, m.MessageID, m.InReplyTo, m.SentRef, m.HandledBy = profile.String, mid.String, irt.String, sref.String, hby.String
	if mi.Valid && mu.Valid {
		m.Mailbox = &MailboxRef{IntegrationID: mi.String, Folder: mf.String, UIDValidity: uint32(mv.Int64), UID: uint32(mu.Int64)}
	}
	m.References, m.To, m.Cc, m.Labels = refs, to, cc, labels
	if sent.Valid {
		t := sent.Time
		m.SentAt = &t
	}
	if recv.Valid {
		t := recv.Time
		m.ReceivedAt = &t
	}
	if handled.Valid {
		t := handled.Time
		m.HandledAt = &t
	}
	_ = json.Unmarshal(headers, &m.Headers)
	_ = json.Unmarshal(att, &m.Attachments)
	if len(dsn) > 0 {
		var d DSN
		if json.Unmarshal(dsn, &d) == nil {
			m.DSN = &d
		}
	}
	if len(link) > 0 {
		var l Link
		if json.Unmarshal(link, &l) == nil {
			m.Link = &l
		}
	}
	var err error
	if m.BodyText, err = s.open(m.WorkspaceID, bt); err != nil {
		return nil, fmt.Errorf("déchiffrement du texte: %w", err)
	}
	if m.BodyHTMLSanitized, err = s.open(m.WorkspaceID, bh); err != nil {
		return nil, fmt.Errorf("déchiffrement du HTML: %w", err)
	}
	if raw.Valid && raw.String != "" {
		rs, err := s.open(m.WorkspaceID, raw)
		if err != nil {
			return nil, fmt.Errorf("déchiffrement du message brut: %w", err)
		}
		m.RawEML = []byte(rs)
	}
	return &m, nil
}

// Get lit un message en entier (corps déchiffrés).
func (s *Store) Get(ctx context.Context, ws, id string) (*Message, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	m, err := scanFull(db.QueryRowContext(ctx, `SELECT `+msgColumns+` FROM veridian_mail_message WHERE id=$1`, id), s)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.WorkspaceID = ws
	return m, nil
}

// Filter : critères de messages.list.
type Filter struct {
	MailboxID    string
	ProfileID    string
	ContactEmail string
	ThreadID     string
	Kinds        []Kind
	Direction    Direction
	Handled      *bool
	Since, Until *time.Time
	Q            string
	// BodySearch : cherche aussi dans les corps (déchiffrés en mémoire, 1500 derniers messages du filtre).
	BodySearch bool
	Limit      int
	Cursor     string
	// AfterSeq : événements, messages de seq > AfterSeq dans l'ordre d'arrivée croissant.
	AfterSeq int64
}

type cursorT struct {
	T   time.Time `json:"t"`
	Seq int64     `json:"s"`
}

func encodeCursor(t time.Time, seq int64) string {
	b, _ := json.Marshal(cursorT{t, seq})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(c string) (cursorT, error) {
	var out cursorT
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return out, errors.New("curseur invalide")
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, errors.New("curseur invalide")
	}
	return out, nil
}

func likeEscape(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

func (f Filter) where(args *[]any, withQ bool) string {
	var w []string
	add := func(cond string, v ...any) {
		for range v {
			*args = append(*args, nil)
		}
		base := len(*args) - len(v)
		for i, x := range v {
			(*args)[base+i] = x
		}
		// remplace {1},{2}... par les numéros de paramètres
		for i := range v {
			cond = strings.ReplaceAll(cond, fmt.Sprintf("{%d}", i+1), fmt.Sprintf("$%d", base+i+1))
		}
		w = append(w, cond)
	}
	if f.MailboxID != "" {
		add("(profile_id={1} OR mailbox_integration_id={1})", f.MailboxID)
	}
	if f.ProfileID != "" {
		add("profile_id={1}", f.ProfileID)
	}
	if f.ContactEmail != "" {
		add("contact_email={1}", strings.ToLower(f.ContactEmail))
	}
	if f.ThreadID != "" {
		add("thread_id={1}", f.ThreadID)
	}
	if len(f.Kinds) > 0 {
		ks := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			ks[i] = string(k)
		}
		add("kind = ANY({1})", pq.Array(ks))
	}
	if f.Direction != "" {
		add("direction={1}", string(f.Direction))
	}
	if f.Handled != nil {
		if *f.Handled {
			w = append(w, "handled_at IS NOT NULL")
		} else {
			w = append(w, "handled_at IS NULL")
		}
	}
	if f.Since != nil {
		add("occurred_at >= {1}", *f.Since)
	}
	if f.Until != nil {
		add("occurred_at < {1}", *f.Until)
	}
	if withQ && f.Q != "" {
		add(`(subject ILIKE {1} OR contact_email ILIKE {1} OR from_addr ILIKE {1} OR from_name ILIKE {1} OR array_to_string(to_addrs,' ') ILIKE {1})`, likeEscape(f.Q))
	}
	if f.AfterSeq > 0 {
		add("seq > {1}", f.AfterSeq)
	}
	if len(w) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w, " AND ")
}

// List rend des lignes de liste, plus récentes d'abord, avec curseur.
func (s *Store) List(ctx context.Context, ws string, f Filter) ([]Summary, string, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, "", err
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	var args []any
	where := f.where(&args, !f.BodySearch)
	if f.BodySearch && f.Q != "" {
		return s.listBodySearch(ctx, ws, db, f)
	}
	if f.Cursor != "" {
		c, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, c.T, c.Seq)
		cond := fmt.Sprintf("(occurred_at, seq) < ($%d, $%d)", len(args)-1, len(args))
		if where == "" {
			where = " WHERE " + cond
		} else {
			where += " AND " + cond
		}
	}
	order := " ORDER BY occurred_at DESC, seq DESC"
	if f.AfterSeq > 0 {
		order = " ORDER BY seq ASC"
	}
	args = append(args, f.Limit+1)
	rows, err := db.QueryContext(ctx, `SELECT `+summaryColumns+` FROM veridian_mail_message`+where+order+fmt.Sprintf(" LIMIT $%d", len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out, err := s.scanSummaries(ws, rows)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		if f.AfterSeq == 0 {
			last := out[len(out)-1]
			next = encodeCursor(last.OccurredAt, last.Seq)
		}
	}
	return out, next, nil
}

func (s *Store) listBodySearch(ctx context.Context, ws string, db *sql.DB, f Filter) ([]Summary, string, error) {
	var args []any
	where := f.where(&args, false)
	args = append(args, 1500)
	rows, err := db.QueryContext(ctx, `SELECT `+summaryColumns+`, subject FROM veridian_mail_message`+where+
		fmt.Sprintf(" ORDER BY occurred_at DESC, seq DESC LIMIT $%d", len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	needle := strings.ToLower(f.Q)
	var out []Summary
	for rows.Next() {
		sm, body, err := s.scanSummaryRow(ws, rows, true)
		if err != nil {
			return nil, "", err
		}
		hay := strings.ToLower(sm.Subject + "\n" + sm.ContactEmail + "\n" + sm.From + "\n" + body)
		if strings.Contains(hay, needle) {
			out = append(out, sm)
			if len(out) >= f.Limit {
				break
			}
		}
	}
	return out, "", rows.Err()
}

func (s *Store) scanSummaries(ws string, rows *sql.Rows) ([]Summary, error) {
	var out []Summary
	for rows.Next() {
		sm, _, err := s.scanSummaryRow(ws, rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

func (s *Store) scanSummaryRow(ws string, rows *sql.Rows, extra bool) (Summary, string, error) {
	var (
		sm                  Summary
		dir, kind           string
		profile, hby        sql.NullString
		to, labels          []string
		bt                  sql.NullString
		sent, recv, handled sql.NullTime
		att, dsn            []byte
		dummy               string
	)
	dest := []any{&sm.ID, &sm.Seq, &dir, &kind, &profile, &sm.ThreadID, &sm.ContactEmail, &sm.From, pq.Array(&to), &sm.Subject,
		&bt, &sm.OccurredAt, &sent, &recv, &att, &dsn, &handled, &hby, pq.Array(&labels)}
	if extra {
		dest = append(dest, &dummy)
	}
	if err := rows.Scan(dest...); err != nil {
		return sm, "", err
	}
	sm.Direction, sm.Kind, sm.ProfileID, sm.HandledBy, sm.To, sm.Labels = Direction(dir), Kind(kind), profile.String, hby.String, to, labels
	if sent.Valid {
		t := sent.Time
		sm.SentAt = &t
	}
	if recv.Valid {
		t := recv.Time
		sm.ReceivedAt = &t
	}
	if handled.Valid {
		t := handled.Time
		sm.HandledAt = &t
	}
	var atts []Attachment
	_ = json.Unmarshal(att, &atts)
	sm.AttachCount = 0
	for _, a := range atts {
		if !a.Inline {
			sm.AttachCount++
		}
	}
	sm.HasAttach = sm.AttachCount > 0
	if len(dsn) > 0 {
		var d DSN
		if json.Unmarshal(dsn, &d) == nil {
			sm.DSNReasonFR = d.ReasonFR
		}
	}
	body, err := s.open(ws, bt)
	if err != nil {
		return sm, "", fmt.Errorf("déchiffrement de l'aperçu: %w", err)
	}
	sm.Preview = Preview(body, 180)
	return sm, body, nil
}

// Preview donne un aperçu d'une ligne (citations et espaces retirés).
func Preview(body string, n int) string {
	var keep []string
	for _, l := range strings.Split(body, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, ">") {
			continue
		}
		keep = append(keep, t)
		if len(strings.Join(keep, " ")) > n*2 {
			break
		}
	}
	return truncate(spacesRe.ReplaceAllString(strings.Join(keep, " "), " "), n)
}

// ThreadMessages rend tous les messages d'un fil, dans l'ordre chronologique, corps déchiffrés.
func (s *Store) ThreadMessages(ctx context.Context, ws, threadID string) ([]Message, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+msgColumns+` FROM veridian_mail_message WHERE thread_id=$1 ORDER BY occurred_at ASC, seq ASC LIMIT 500`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanFull(rows, s)
		if err != nil {
			return nil, err
		}
		m.WorkspaceID = ws
		out = append(out, *m)
	}
	return out, rows.Err()
}

// ThreadsOfContact rend les fils d'un contact (résumés), le plus récent d'abord.
func (s *Store) ThreadsOfContact(ctx context.Context, ws, contact string, limit int) ([]Thread, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT thread_id, MIN(occurred_at), MAX(occurred_at), COUNT(*),
		(array_agg(subject ORDER BY occurred_at ASC))[1], (array_agg(kind ORDER BY occurred_at DESC))[1]
		FROM veridian_mail_message WHERE contact_email=$1 GROUP BY thread_id ORDER BY MAX(occurred_at) DESC LIMIT $2`,
		strings.ToLower(contact), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		t := Thread{ContactEmail: strings.ToLower(contact)}
		var k string
		if err := rows.Scan(&t.ThreadID, &t.FirstAt, &t.LastAt, &t.Count, &t.Subject, &k); err != nil {
			return nil, err
		}
		t.LastKind = Kind(k)
		out = append(out, t)
	}
	return out, rows.Err()
}

// Update pose « traité » et les libellés. handled nil : inchangé ; labels nil : inchangés.
func (s *Store) Update(ctx context.Context, ws, id string, handled *bool, by string, labels *[]string) error {
	db, err := s.db(ctx, ws)
	if err != nil {
		return err
	}
	sets := []string{"updated_at=NOW()"}
	var args []any
	if handled != nil {
		if *handled {
			args = append(args, by)
			sets = append(sets, fmt.Sprintf("handled_at=COALESCE(handled_at, NOW()), handled_by=$%d", len(args)))
		} else {
			sets = append(sets, "handled_at=NULL, handled_by=NULL")
		}
	}
	if labels != nil {
		args = append(args, pq.Array(nonNilStr(*labels)))
		sets = append(sets, fmt.Sprintf("labels=$%d", len(args)))
	}
	args = append(args, id)
	res, err := db.ExecContext(ctx, `UPDATE veridian_mail_message SET `+strings.Join(sets, ", ")+fmt.Sprintf(" WHERE id=$%d", len(args)), args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailboxCounts : compteurs par profil ou boîte.
type MailboxCounts struct {
	CountIn, CountOut, Unhandled int
	Last                         *time.Time
}

// Counts rend les compteurs par identifiant de boîte (profil et intégration IMAP confondus).
func (s *Store) Counts(ctx context.Context, ws string) (map[string]MailboxCounts, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(profile_id,''), COALESCE(mailbox_integration_id,''),
		COUNT(*) FILTER (WHERE direction='in'), COUNT(*) FILTER (WHERE direction='out'),
		COUNT(*) FILTER (WHERE kind IN ('reply_human') AND handled_at IS NULL), MAX(occurred_at)
		FROM veridian_mail_message GROUP BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]MailboxCounts{}
	merge := func(k string, in, o, un int, last sql.NullTime) {
		if k == "" {
			return
		}
		c := out[k]
		c.CountIn += in
		c.CountOut += o
		c.Unhandled += un
		if last.Valid && (c.Last == nil || last.Time.After(*c.Last)) {
			t := last.Time
			c.Last = &t
		}
		out[k] = c
	}
	for rows.Next() {
		var p, mi string
		var in, o, un int
		var last sql.NullTime
		if err := rows.Scan(&p, &mi, &in, &o, &un, &last); err != nil {
			return nil, err
		}
		merge(p, in, o, un, last)
		if mi != "" && mi != p {
			merge(mi, in, o, un, last)
		}
	}
	return out, rows.Err()
}

// MaxSeq rend le dernier numéro d'ordre (0 si la boîte est vide).
func (s *Store) MaxSeq(ctx context.Context, ws string) (int64, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return 0, err
	}
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(seq) FROM veridian_mail_message`).Scan(&v); err != nil {
		return 0, err
	}
	return v.Int64, nil
}

// ---------- repère de synchronisation ----------

type syncRow struct {
	UIDValidity uint32
	LastUID     uint32
	Cursor      string
	State       SyncState
}

// GetSync lit le repère d'une boîte (ou le curseur de reprise de l'historique).
func (s *Store) GetSync(ctx context.Context, ws, integrationID, folder string) (*syncRow, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	var r syncRow
	var ran, ok sql.NullTime
	var v, l int64
	err = db.QueryRowContext(ctx, `SELECT uid_validity, last_uid, cursor, last_run_at, last_ok_at, last_error, total_ingested
		FROM veridian_mail_sync_state WHERE integration_id=$1 AND folder=$2`, integrationID, folder).
		Scan(&v, &l, &r.Cursor, &ran, &ok, &r.State.LastError, &r.State.TotalIngested)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.UIDValidity, r.LastUID = uint32(v), uint32(l)
	r.State.UIDValidity, r.State.LastUID = r.UIDValidity, r.LastUID
	if ran.Valid {
		t := ran.Time
		r.State.LastRunAt = &t
	}
	if ok.Valid {
		t := ok.Time
		r.State.LastOKAt = &t
	}
	return &r, nil
}

// SaveSync enregistre le repère. ingested s'ajoute au total.
func (s *Store) SaveSync(ctx context.Context, ws, integrationID, folder string, validity, lastUID uint32, cursor string, ok bool, errMsg string, ingested int) error {
	db, err := s.db(ctx, ws)
	if err != nil {
		return err
	}
	if len(errMsg) > 300 {
		errMsg = errMsg[:300]
	}
	_, err = db.ExecContext(ctx, `INSERT INTO veridian_mail_sync_state (integration_id, folder, uid_validity, last_uid, cursor, last_run_at, last_ok_at, last_error, total_ingested)
		VALUES ($1,$2,$3,$4,$5,NOW(), CASE WHEN $6 THEN NOW() END, $7, $8)
		ON CONFLICT (integration_id, folder) DO UPDATE SET uid_validity=EXCLUDED.uid_validity, last_uid=EXCLUDED.last_uid,
		cursor=EXCLUDED.cursor, last_run_at=NOW(), last_ok_at=CASE WHEN $6 THEN NOW() ELSE veridian_mail_sync_state.last_ok_at END,
		last_error=EXCLUDED.last_error, total_ingested=veridian_mail_sync_state.total_ingested + $8`,
		integrationID, folder, int64(validity), int64(lastUID), cursor, ok, errMsg, int64(ingested))
	return err
}

// ListSync rend les repères de toutes les boîtes du workspace.
func (s *Store) ListSync(ctx context.Context, ws string) (map[string]SyncState, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT integration_id, uid_validity, last_uid, last_run_at, last_ok_at, last_error, total_ingested
		FROM veridian_mail_sync_state WHERE integration_id NOT LIKE '\_\_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SyncState{}
	for rows.Next() {
		var id string
		var st SyncState
		var v, l int64
		var ran, ok sql.NullTime
		if err := rows.Scan(&id, &v, &l, &ran, &ok, &st.LastError, &st.TotalIngested); err != nil {
			return nil, err
		}
		st.UIDValidity, st.LastUID = uint32(v), uint32(l)
		if ran.Valid {
			t := ran.Time
			st.LastRunAt = &t
		}
		if ok.Valid {
			t := ok.Time
			st.LastOKAt = &t
		}
		out[id] = st
	}
	return out, rows.Err()
}

// HistoryRow : un envoi de message_history à reprendre.
type HistoryRow struct {
	ID              string
	ContactEmail    string
	TemplateID      string
	TemplateVersion int64
	AutomationID    string
	BroadcastID     string
	ProfileID       string
	SenderEmail     string
	SentAt          time.Time
	DataEncrypted   []byte // message_data brut (jsonb)
	ChannelOptions  []byte
	SubjectMeta     string
}

// HistoryBatch lit les envois acceptés après le curseur (sent_at, id), dans l'ordre.
func (s *Store) HistoryBatch(ctx context.Context, ws string, afterAt time.Time, afterID string, limit int) ([]HistoryRow, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, contact_email, template_id, template_version, COALESCE(automation_id,''),
		COALESCE(broadcast_id,''), COALESCE(veridian_profile_id,''), COALESCE(veridian_sender_email,''), sent_at, message_data, channel_options
		FROM message_history WHERE sent_at IS NOT NULL AND failed_at IS NULL AND channel='email' AND (sent_at, id) > ($1, $2)
		ORDER BY sent_at ASC, id ASC LIMIT $3`, afterAt, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryRow
	for rows.Next() {
		var h HistoryRow
		var tv sql.NullInt64
		var opts []byte
		if err := rows.Scan(&h.ID, &h.ContactEmail, &h.TemplateID, &tv, &h.AutomationID, &h.BroadcastID, &h.ProfileID, &h.SenderEmail, &h.SentAt, &h.DataEncrypted, &opts); err != nil {
			return nil, err
		}
		h.TemplateVersion = tv.Int64
		h.ChannelOptions = opts
		out = append(out, h)
	}
	return out, rows.Err()
}

func nonNilStr(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilHeaders(h map[string][]string) map[string][]string {
	if h == nil {
		return map[string][]string{}
	}
	return h
}

func nonNilAtt(a []Attachment) []Attachment {
	if a == nil {
		return []Attachment{}
	}
	return a
}
