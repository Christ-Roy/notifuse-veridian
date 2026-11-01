package mailbox

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Notifuse/notifuse/internal/veridian/mailbox/dedupsql"
)

// Déduplication des mails entrants (L1c). Le contrat, la clé et le classement de l'original
// vivent dans le paquet dedupsql ; ce fichier les applique à chaque mail rangé.

// FallbackDedupKey est la clé d'un entrant sans Message-ID exploitable : contact, fil, date
// d'envoi et empreinte du corps. Vide quand les éléments manquent (pas de contact, pas de date
// d'envoi) : mieux vaut laisser passer un doublon que fusionner deux mails distincts.
func FallbackDedupKey(m *Message) string {
	if m.ContactEmail == "" || m.ThreadID == "" || m.SentAt == nil || m.SentAt.IsZero() {
		return ""
	}
	body := m.BodyText
	if strings.TrimSpace(body) == "" {
		body = HTMLToText(m.BodyHTMLSanitized)
	}
	body = strings.Join(strings.Fields(strings.ToLower(body)), " ")
	if body == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.ToLower(m.ContactEmail) + "\x00" + m.ThreadID + "\x00" +
		strconv.FormatInt(m.SentAt.Unix(), 10) + "\x00" + body))
	return "fp:" + hex.EncodeToString(sum[:16])
}

// ResolveDuplicates pose la clé de regroupement du mail id (Message-ID, sinon fallbackKey) puis
// recalcule son groupe : l'original reste visible, les copies pointent l'original. Rend l'id de
// l'original si le mail id est une copie, "" sinon. Ne dépend pas de l'ordre d'arrivée.
func (s *Store) ResolveDuplicates(ctx context.Context, ws, id, fallbackKey string) (string, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return "", err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var key sql.NullString
	err = tx.QueryRowContext(ctx, `UPDATE veridian_mail_message SET dedup_key = COALESCE(`+dedupsql.MidKeyExpr+`, NULLIF($2, ''))
		WHERE id = $1 AND direction = 'in' RETURNING dedup_key`, id, fallbackKey).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("clé de regroupement: %w", err)
	}
	if !key.Valid {
		return "", tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, dedupsql.ResolveSQL(`dedup_key = $1`), key.String); err != nil {
		return "", fmt.Errorf("marquage des copies: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dedupsql.PropagateHandledSQL(`dedup_key = $1`), key.String); err != nil {
		return "", fmt.Errorf("état traité du groupe: %w", err)
	}
	var dupOf sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT duplicate_of FROM veridian_mail_message WHERE id = $1`, id).Scan(&dupOf); err != nil {
		return "", err
	}
	return dupOf.String, tx.Commit()
}

// SettleDuplicates rattrape les mails rangés sans passer par ResolveDuplicates (déploiement
// glissant avec une version antérieure, résolution échouée) : pose les clés manquantes et
// remarque les groupes qui ont plus d'un mail visible. Rend le nombre de lignes modifiées.
func (s *Store) SettleDuplicates(ctx context.Context, ws string) (int64, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var total int64
	for _, q := range []string{
		dedupsql.AssignKeysSQL,
		dedupsql.ResolveSQL(dedupsql.UnsettledFilter),
		dedupsql.PropagateHandledSQL(dedupsql.UnsettledFilter),
	} {
		res, err := tx.ExecContext(ctx, q)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, tx.Commit()
}

// AllSchemaStatements : V63 puis V68, dans l'ordre des migrations (bases de test).
func AllSchemaStatements() []string {
	return append(append([]string{}, SchemaStatements...), dedupsql.Statements...)
}
