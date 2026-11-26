package activite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/activite/activitesql"
	"github.com/lib/pq"
)

// Outcomes d'une ligne close (colonne outcome).
const (
	OutcomeAccepted = "accepte" // 200 du Hub (acceptée, doublon ou ignorée : le Hub l'a lue)
	OutcomeRefused  = "refuse"  // 400, 409, 413 : lot abandonné, jamais rejoué
	OutcomeExpired  = "perime"  // plus de 7 jours : le Hub l'ignorerait
	OutcomeOverflow = "debordement"
	OutcomeOrphan   = "orphelin" // le message a disparu
)

// Stats : l'état de la file d'un espace.
type Stats struct {
	Pending int
	Ready   int
	Oldest  time.Time // création de la plus vieille ligne en attente ; zéro si aucune
}

// ReadStats : lignes en attente, celles prêtes à partir (posées depuis settle), et l'âge de la plus vieille.
func ReadStats(ctx context.Context, db *sql.DB, settle time.Duration) (Stats, error) {
	var st Stats
	var oldest sql.NullTime
	err := db.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE created_at <= NOW() - make_interval(secs => $1)), MIN(created_at)
		FROM veridian_activite_outbox WHERE done_at IS NULL`, settle.Seconds()).Scan(&st.Pending, &st.Ready, &oldest)
	if err != nil {
		return st, err
	}
	if oldest.Valid {
		st.Oldest = oldest.Time
	}
	return st, nil
}

// readySQL : les lignes prêtes, avec ce qu'il faut pour l'événement. Le corps du mail n'est jamais lu.
const readySQL = `SELECT o.id, o.source, o.ref_id, o.event, o.occurred_at, o.host,
	mh.id IS NOT NULL, COALESCE(mh.contact_email, ''), COALESCE(mh.list_id, ''), COALESCE(mh.automation_id, ''), COALESCE(mh.broadcast_id, ''),
	COALESCE(mh.bounce_type, ''), COALESCE(mh.veridian_sender_email, ''), COALESCE(mh.channel_options->>'from_name', ''),
	COALESCE(NULLIF(mh.channel_options->>'subject', ''), mh.message_data->'metadata'->>'veridian_subject', ''),
	vo.id IS NOT NULL, COALESCE(vo.message_id, ''), COALESCE(vo.thread_id, ''), COALESCE(vo.from_addr, ''), COALESCE(vo.from_name, ''), COALESCE(vo.subject, ''),
	vi.id IS NOT NULL, COALESCE(vi.message_id, ''), COALESCE(vi.thread_id, ''), COALESCE(vi.contact_email, ''), COALESCE(vi.from_addr, ''),
	COALESCE(vi.from_name, ''), COALESCE(vi.subject, ''), COALESCE(vi.kind, ''), COALESCE(vi.duplicate_of, '')
FROM veridian_activite_outbox o
LEFT JOIN message_history mh ON o.source = 'history' AND mh.id = o.ref_id
LEFT JOIN veridian_mail_message vo ON o.source = 'history' AND vo.sent_ref = o.ref_id
LEFT JOIN veridian_mail_message vi ON o.source = 'mail' AND vi.id = o.ref_id
WHERE o.done_at IS NULL AND o.created_at <= NOW() - make_interval(secs => $1)
ORDER BY o.id
LIMIT $2`

// ReadReady lit au plus limit lignes prêtes. orphans : lignes dont le message a disparu (ou une copie d'un reçu), à clore.
func ReadReady(ctx context.Context, db *sql.DB, settle time.Duration, limit int) (rows []Pending, orphans []int64, err error) {
	rs, err := db.QueryContext(ctx, readySQL, settle.Seconds(), limit)
	if err != nil {
		return nil, nil, err
	}
	defer rs.Close()
	for rs.Next() {
		var (
			id                                                         int64
			source, ref, event, host                                   string
			at                                                         time.Time
			hasMH                                                      bool
			mhContact, mhList, mhAuto, mhBroadcast, mhBounce, mhSender string
			mhFromName, mhSubject                                      string
			hasVO                                                      bool
			voMID, voThread, voFrom, voFromName, voSubject             string
			hasVI                                                      bool
			viMID, viThread, viContact, viFrom, viFromName, viSubject  string
			viKind, viDup                                              string
		)
		if err := rs.Scan(&id, &source, &ref, &event, &at, &host,
			&hasMH, &mhContact, &mhList, &mhAuto, &mhBroadcast, &mhBounce, &mhSender, &mhFromName, &mhSubject,
			&hasVO, &voMID, &voThread, &voFrom, &voFromName, &voSubject,
			&hasVI, &viMID, &viThread, &viContact, &viFrom, &viFromName, &viSubject, &viKind, &viDup); err != nil {
			return nil, nil, err
		}
		ev := Event{Event: event, OccurredAt: at.UTC().Format(time.RFC3339)}
		switch source {
		case activitesql.SourceHistory:
			if !hasMH {
				orphans = append(orphans, id)
				continue
			}
			from, fromName, subject, mid := mhSender, mhFromName, mhSubject, ""
			if hasVO { // le rendu exact rangé dans la boîte unifiée fait foi
				from, fromName, subject, mid = firstNonEmpty(voFrom, from), firstNonEmpty(voFromName, fromName), firstNonEmpty(voSubject, subject), voMID
				ev.ThreadID = voThread
			}
			if mid == "" {
				mid = domain.VeridianRFCMessageID(ref, from)
			}
			if mid == "" {
				mid = domain.VeridianBareMessageUUID(ref)
			}
			ev.RFCMessageID, ev.RecipientEmail, ev.FromEmail = bareMsgID(mid), cleanEmail(mhContact), cleanEmail(from)
			ev.FromName, ev.Subject = clip(fromName, maxSubject), clip(subject, maxSubject)
			ev.Nature, ev.ListID, ev.AutomationID, ev.BroadcastID = historyNature(mhBroadcast, mhAuto), mhList, mhAuto, mhBroadcast
			if event == "message.bounced" {
				ev.BounceType = bounceTypeOf(mhBounce)
			}
			if event == "message.clicked" {
				ev.Host = HostOf("https://" + host) // déjà réduit à l'hôte à l'entrée ; relu par la même règle
			}
		case activitesql.SourceMail:
			nature, bounce := mailNature(viKind)
			if !hasVI || viDup != "" || nature == "" || viContact == "" {
				orphans = append(orphans, id)
				continue
			}
			mid := bareMsgID(viMID)
			if mid == "" {
				mid = ref // sans Message-ID exploitable : l'identifiant Notifuse du mail, stable
			}
			ev.RFCMessageID, ev.ThreadID, ev.RecipientEmail, ev.FromEmail = mid, viThread, cleanEmail(viContact), cleanEmail(viFrom)
			ev.FromName, ev.Subject, ev.Nature, ev.BounceType = clip(viFromName, maxSubject), clip(viSubject, maxSubject), nature, bounce
		default:
			orphans = append(orphans, id)
			continue
		}
		rows = append(rows, Pending{ID: id, Event: ev})
	}
	return rows, orphans, rs.Err()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// MarkDone clôt des lignes (outcome, lot).
func MarkDone(ctx context.Context, db *sql.DB, ids []int64, outcome, lotID string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, `UPDATE veridian_activite_outbox SET done_at = NOW(), outcome = $2, lot_id = NULLIF($3, ''), attempts = attempts + 1
		WHERE id = ANY($1) AND done_at IS NULL`, pq.Array(ids), outcome, lotID)
	return err
}

// MarkAttempt compte un essai sans clore (rejeu à venir).
func MarkAttempt(ctx context.Context, db *sql.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, `UPDATE veridian_activite_outbox SET attempts = attempts + 1 WHERE id = ANY($1) AND done_at IS NULL`, pq.Array(ids))
	return err
}

// Bounds : rétention et taille de la file d'un espace.
type Bounds struct {
	MaxAge     time.Duration // une ligne en attente plus vieille (date de l'événement) est close « périmée »
	KeepDone   time.Duration // une ligne close est retirée après
	MaxPending int           // au-delà, les plus vieilles lignes en attente sont closes « débordement »
	MaxDone    int           // au plus autant de lignes closes gardées
}

// DefaultBounds : 7 jours (le Hub ignore au-delà), 100 000 en attente par espace, 2 jours et 50 000 lignes closes.
var DefaultBounds = Bounds{MaxAge: 7 * 24 * time.Hour, KeepDone: 48 * time.Hour, MaxPending: 100_000, MaxDone: 50_000}

// Purge tient les bornes. Rend le nombre de lignes closes sans être parties (périmées, débordement).
func Purge(ctx context.Context, db *sql.DB, b Bounds) (expired, overflow int, err error) {
	r, err := db.ExecContext(ctx, `UPDATE veridian_activite_outbox SET done_at = NOW(), outcome = $1
		WHERE done_at IS NULL AND occurred_at < NOW() - make_interval(secs => $2)`, OutcomeExpired, b.MaxAge.Seconds())
	if err != nil {
		return 0, 0, err
	}
	n, _ := r.RowsAffected()
	expired = int(n)
	r, err = db.ExecContext(ctx, `UPDATE veridian_activite_outbox SET done_at = NOW(), outcome = $1
		WHERE id IN (SELECT id FROM veridian_activite_outbox WHERE done_at IS NULL ORDER BY id DESC OFFSET $2)`, OutcomeOverflow, b.MaxPending)
	if err != nil {
		return expired, 0, err
	}
	n, _ = r.RowsAffected()
	overflow = int(n)
	if _, err = db.ExecContext(ctx, `DELETE FROM veridian_activite_outbox WHERE done_at IS NOT NULL AND done_at < NOW() - make_interval(secs => $1)`, b.KeepDone.Seconds()); err != nil {
		return expired, overflow, err
	}
	_, err = db.ExecContext(ctx, `DELETE FROM veridian_activite_outbox WHERE id IN (SELECT id FROM veridian_activite_outbox WHERE done_at IS NOT NULL ORDER BY id DESC OFFSET $1)`, b.MaxDone)
	return expired, overflow, err
}

// SetClickHost pose l'hôte du premier clic d'un message, tant que l'événement n'est pas parti.
func SetClickHost(ctx context.Context, db *sql.DB, messageID, host string) error {
	if messageID == "" || host == "" {
		return nil
	}
	_, err := db.ExecContext(ctx, `UPDATE veridian_activite_outbox SET host = $2
		WHERE source = 'history' AND ref_id = $1 AND event = 'message.clicked' AND host = '' AND done_at IS NULL`, messageID, host)
	return err
}

// Subscription : l'abonnement du Hub sur l'espace (adresse, secret). found=false : espace non relié.
type Subscription struct {
	URL     string
	Secret  string
	Enabled bool
}

// ReadSubscription lit l'abonnement du Hub.
func ReadSubscription(ctx context.Context, db *sql.DB) (Subscription, bool, error) {
	var s Subscription
	var enabled sql.NullBool
	err := db.QueryRowContext(ctx, `SELECT url, secret, enabled FROM webhook_subscriptions WHERE id = $1`, activitesql.SubscriptionID).Scan(&s.URL, &s.Secret, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	s.Enabled = enabled.Valid && enabled.Bool
	return s, err == nil, err
}
