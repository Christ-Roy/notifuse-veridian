package mailbox

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Issue d'un mail écrit depuis la boîte (27/11/2026) : « un mail envoyé à la main part tout de suite », comme dans une
// vraie messagerie. L'API d'envoi attend quelques secondes cette issue au lieu de répondre « en file ».
const (
	SendStateSent    = "sent"    // le relais l'a accepté : il est rangé dans la boîte (sent_ref = id message_history)
	SendStateFailed  = "failed"  // échec définitif ou provisoire consigné dans message_history (failed_at)
	SendStatePending = "sending" // pas encore traité par le worker
)

// SendOutcome : état d'un envoi et, en échec, la raison consignée (jamais de secret : le texte de l'erreur SMTP).
type SendOutcome struct {
	State  string
	Reason string
}

// SendOutcome lit l'issue d'un envoi par son id message_history. status_info est lu par to_jsonb : la colonne est
// celle de Notifuse, absente des schémas de test réduits, et sa lecture ne doit pas en dépendre.
func (s *Store) SendOutcome(ctx context.Context, ws, historyID string) (SendOutcome, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return SendOutcome{}, err
	}
	var one int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM veridian_mail_message WHERE sent_ref=$1 LIMIT 1`, historyID).Scan(&one)
	if err == nil {
		return SendOutcome{State: SendStateSent}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SendOutcome{}, err
	}
	var failed sql.NullTime
	var info sql.NullString
	err = db.QueryRowContext(ctx, `SELECT failed_at, to_jsonb(h)->>'status_info' FROM message_history h WHERE id=$1`, historyID).Scan(&failed, &info)
	if errors.Is(err, sql.ErrNoRows) {
		return SendOutcome{State: SendStatePending}, nil
	}
	if err != nil {
		return SendOutcome{}, err
	}
	if failed.Valid {
		return SendOutcome{State: SendStateFailed, Reason: strings.TrimSpace(info.String)}, nil
	}
	return SendOutcome{State: SendStatePending}, nil
}
