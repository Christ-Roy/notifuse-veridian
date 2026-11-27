package liensclics

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MaxURL : longueur retenue d'une URL cliquée. Au-delà, la clé de la carte n'apporte rien et gonfle la ligne.
const MaxURL = 2048

// Store écrit le clic dans la colonne message_history.clicked_links d'une base workspace.
// Écriture atomique : un seul UPDATE, qui incrémente le compteur en SQL. Mail tourne en deux exemplaires :
// jamais de lecture puis écriture en Go, sinon deux clics simultanés se perdent.
type Store struct{}

// Lier enregistre un clic sur url pour le message messageID. Renvoie (false, nil) si le message n'existe
// pas dans cette base ; l'appelant n'en fait rien, la redirection n'en dépend pas.
func (Store) Lier(ctx context.Context, db *sql.DB, messageID, url string, at time.Time) (bool, error) {
	if len(url) > MaxURL {
		url = url[:MaxURL]
	}
	if url == "" || messageID == "" || db == nil {
		return false, nil
	}
	instant := at.UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `
		UPDATE message_history
		SET clicked_links = jsonb_set(
			COALESCE(clicked_links, '{}'::jsonb),
			ARRAY[$2::text],
			COALESCE(clicked_links -> $2::text, '{}'::jsonb) || jsonb_build_object(
				'count', COALESCE((clicked_links -> $2::text ->> 'count')::int, 0) + 1,
				'first_at', COALESCE(clicked_links -> $2::text ->> 'first_at', $3::text),
				'last_at', $3::text
			),
			true
		)
		WHERE id = $1`, messageID, url, instant)
	if err != nil {
		return false, fmt.Errorf("liensclics: écriture du clic : %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("liensclics: lignes modifiées : %w", err)
	}
	return n > 0, nil
}
