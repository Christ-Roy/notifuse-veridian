package mailbox

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// Vue « client mail » de la boîte d'un profil (fiche 84) : mêmes messages que l'API, rangés en dossiers, avec des drapeaux
// propres au profil (lu, répondu, suivi). Un message retiré par le client est masqué, jamais supprimé.

// BoxFolder : dossier vu d'un client mail.
type BoxFolder string

const (
	BoxInbox   BoxFolder = "INBOX"
	BoxSent    BoxFolder = "Sent"
	BoxRejects BoxFolder = "Rejets"
	BoxTrash   BoxFolder = "Trash"
)

// BoxMaxMessages : un dossier montre au plus ses N messages les plus récents.
const BoxMaxMessages = 20000

// boxPurgedFlag marque un message vidé de la corbeille (invisible, conservé).
const boxPurgedFlag = "$purged"

var boxFolderWhere = map[BoxFolder]string{
	BoxInbox:   `m.direction = 'in' AND m.kind IN ('reply_human','reply_auto','other') AND NOT COALESCE(f.hidden, FALSE)`,
	BoxRejects: `m.direction = 'in' AND m.kind IN ('bounce_hard','bounce_soft','dsn') AND NOT COALESCE(f.hidden, FALSE)`,
	BoxSent:    `m.direction = 'out' AND NOT COALESCE(f.hidden, FALSE)`,
	BoxTrash:   `COALESCE(f.hidden, FALSE) AND NOT ('` + boxPurgedFlag + `' = ANY(COALESCE(f.flags, '{}')))`,
}

// BoxEntry : une ligne d'un dossier, sans corps.
type BoxEntry struct {
	Seq   int64
	ID    string
	At    time.Time
	Size  int64 // 0 : à mesurer sur le message rendu (envoi reconstitué, message brut absent)
	Flags []string
}

// BoxIndex rend les messages d'un dossier, par numéro croissant. Les lignes de moins de 2 s attendent le passage suivant :
// le numéro est attribué avant la validation, un numéro plus petit peut encore apparaître.
func (s *Store) BoxIndex(ctx context.Context, ws, profileID string, folder BoxFolder) ([]BoxEntry, error) {
	where, ok := boxFolderWhere[folder]
	if !ok {
		return nil, ErrNotFound
	}
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT * FROM (
		SELECT m.seq, m.id, m.occurred_at,
		       CASE WHEN m.direction = 'in' AND m.raw_eml IS NOT NULL AND m.raw_eml <> '' AND NOT m.truncated THEN m.size_bytes ELSE 0 END,
		       COALESCE(f.flags, '{}')
		FROM veridian_mail_message m
		LEFT JOIN veridian_mail_box_flag f ON f.profile_id = $1 AND f.message_id = m.id
		WHERE m.profile_id = $1 AND m.duplicate_of IS NULL AND m.created_at <= NOW() - INTERVAL '2 seconds' AND `+where+`
		ORDER BY m.seq DESC LIMIT $2) t ORDER BY 1`, profileID, BoxMaxMessages)
	if err != nil {
		return nil, fmt.Errorf("lecture du dossier: %w", err)
	}
	defer rows.Close()
	var out []BoxEntry
	for rows.Next() {
		var e BoxEntry
		if err := rows.Scan(&e.Seq, &e.ID, &e.At, &e.Size, pq.Array(&e.Flags)); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// BoxMessages lit des messages entiers d'un profil. Un id d'un autre profil est ignoré : le cloisonnement se fait ici aussi.
func (s *Store) BoxMessages(ctx context.Context, ws, profileID string, ids []string) (map[string]*Message, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+msgColumns+` FROM veridian_mail_message WHERE profile_id = $1 AND id = ANY($2)`, profileID, pq.Array(nonNilStr(ids)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*Message{}
	for rows.Next() {
		m, err := scanFull(rows, s)
		if err != nil {
			return nil, err
		}
		m.WorkspaceID = ws
		out[m.ID] = m
	}
	return out, rows.Err()
}

// BoxSetFlags remplace les drapeaux d'un message du profil ; hidden nil = inchangé. Un message d'un autre profil : sans effet.
func (s *Store) BoxSetFlags(ctx context.Context, ws, profileID, id string, flags []string, hidden *bool) error {
	db, err := s.db(ctx, ws)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO veridian_mail_box_flag (profile_id, message_id, flags, hidden, updated_at)
		SELECT $1::varchar, m.id, $3, COALESCE($4, FALSE), NOW() FROM veridian_mail_message m WHERE m.id = $2 AND m.profile_id = $1::text
		ON CONFLICT (profile_id, message_id) DO UPDATE SET flags = EXCLUDED.flags,
			hidden = COALESCE($4, veridian_mail_box_flag.hidden), updated_at = NOW()`,
		profileID, id, pq.Array(nonNilStr(flags)), hidden)
	return err
}

// BoxPurge vide un message de la corbeille du client (il reste en base et dans l'API).
func (s *Store) BoxPurge(ctx context.Context, ws, profileID, id string, flags []string) error {
	yes := true
	return s.BoxSetFlags(ctx, ws, profileID, id, append(append([]string{}, flags...), boxPurgedFlag), &yes)
}

// BoxSentKnown dit si un envoi du profil porte l'un de ces Message-ID (copie « Envoyés » déposée par un client mail).
func (s *Store) BoxSentKnown(ctx context.Context, ws, profileID string, messageIDs []string) (bool, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return false, err
	}
	var known bool
	err = db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM veridian_mail_message
		WHERE profile_id = $1 AND direction = 'out' AND message_id = ANY($2))`, profileID, pq.Array(nonNilStr(messageIDs))).Scan(&known)
	return known, err
}

// BoxRaw rend le message tel qu'un client mail le lit : les octets reçus, sinon le rendu reconstitué (identique d'une
// lecture à l'autre), fins de ligne CRLF.
func BoxRaw(m *Message) []byte {
	raw := m.RawEML
	if len(raw) == 0 {
		raw = Reconstruct(m)
	}
	return toCRLF(raw)
}

func toCRLF(b []byte) []byte {
	bare := 0
	for i, c := range b {
		if c == '\n' && (i == 0 || b[i-1] != '\r') {
			bare++
		}
	}
	if bare == 0 {
		return b
	}
	out := make([]byte, 0, len(b)+bare)
	for i, c := range b {
		if c == '\n' && (i == 0 || b[i-1] != '\r') {
			out = append(out, '\r')
		}
		out = append(out, c)
	}
	return out
}
