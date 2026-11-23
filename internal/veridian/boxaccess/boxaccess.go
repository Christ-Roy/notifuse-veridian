// Package boxaccess porte les accès d'une boîte louée (fiche 84) : un identifiant (l'adresse du profil) et un mot de
// passe propre à la boîte, créés et révoqués dans Notifuse, valables en IMAP et en SMTP. Jamais les accès de Notifuse
// vers le relais. Le mot de passe est aléatoire (160 bits), montré une seule fois ; la base n'en garde que l'empreinte.
package boxaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Bornes des accès.
const (
	MaxPerWorkspace = 200
	DefaultDailyCap = 200
	MaxDailyCap     = 1000
)

// Erreurs rendues à l'appelant.
var (
	// ErrAuth : identifiant ou mot de passe faux, accès révoqué, adresse retirée du profil. Une seule erreur pour tous les
	// cas : rien ne dit à un inconnu si l'adresse existe.
	ErrAuth     = errors.New("identifiants de boîte refusés")
	ErrNotFound = errors.New("accès introuvable")
	ErrTooMany  = fmt.Errorf("nombre maximal d'accès atteint (%d par workspace)", MaxPerWorkspace)
	ErrCap      = errors.New("plafond d'envoi du jour atteint pour cet accès")
)

// Access : un accès de boîte. Jamais le mot de passe ni son empreinte.
type Access struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	ProfileID   string     `json:"profile_id"`
	Address     string     `json:"address"`
	Label       string     `json:"label"`
	CanSend     bool       `json:"can_send"`
	DailyCap    int        `json:"daily_cap"`
	CreatedBy   string     `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// Store lit et écrit les accès (base système).
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore construit le dépôt.
func NewStore(db *sql.DB) *Store { return &Store{db: db, now: time.Now} }

// NewSecret rend un mot de passe de 32 caractères lisibles (base32, 160 bits).
func NewSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

// hashSecret : empreinte du mot de passe. SHA-256 suffit (secret aléatoire de 160 bits, pas un mot choisi par un humain) et
// ne donne pas prise à une saturation par essais répétés.
func hashSecret(s string) string {
	h := sha256.Sum256([]byte("veridian-box:" + s))
	return hex.EncodeToString(h[:])
}

// NormalizeAddress met l'adresse sous sa forme de comparaison.
func NormalizeAddress(a string) string { return strings.ToLower(strings.TrimSpace(a)) }

const columns = `id, workspace_id, profile_id, address, label, can_send, daily_cap, created_by, created_at, last_used_at, revoked_at`

func scan(row interface{ Scan(...any) error }, extra ...any) (*Access, error) {
	var a Access
	var used, revoked sql.NullTime
	if err := row.Scan(append([]any{&a.ID, &a.WorkspaceID, &a.ProfileID, &a.Address, &a.Label, &a.CanSend, &a.DailyCap, &a.CreatedBy, &a.CreatedAt, &used, &revoked}, extra...)...); err != nil {
		return nil, err
	}
	if used.Valid {
		a.LastUsedAt = &used.Time
	}
	if revoked.Valid {
		a.RevokedAt = &revoked.Time
	}
	return &a, nil
}

// Create enregistre un accès et rend le mot de passe, montré une seule fois.
func (s *Store) Create(ctx context.Context, a Access) (*Access, string, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM veridian_box_access WHERE workspace_id = $1 AND revoked_at IS NULL`, a.WorkspaceID).Scan(&n); err != nil {
		return nil, "", err
	}
	if n >= MaxPerWorkspace {
		return nil, "", ErrTooMany
	}
	secret, err := NewSecret()
	if err != nil {
		return nil, "", err
	}
	a.ID, a.Address = uuid.NewString(), NormalizeAddress(a.Address)
	if a.DailyCap <= 0 {
		a.DailyCap = DefaultDailyCap
	}
	row := s.db.QueryRowContext(ctx, `INSERT INTO veridian_box_access (id, workspace_id, profile_id, address, label, secret_hash, can_send, daily_cap, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+columns,
		a.ID, a.WorkspaceID, a.ProfileID, a.Address, a.Label, hashSecret(secret), a.CanSend, a.DailyCap, a.CreatedBy)
	out, err := scan(row)
	if err != nil {
		return nil, "", err
	}
	return out, secret, nil
}

// List rend les accès d'un workspace, les révoqués compris, du plus récent au plus ancien.
func (s *Store) List(ctx context.Context, ws string) ([]Access, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM veridian_box_access WHERE workspace_id = $1 ORDER BY created_at DESC, id LIMIT 500`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Access{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// Revoke coupe un accès. Idempotent ; un accès d'un autre workspace est introuvable.
func (s *Store) Revoke(ctx context.Context, ws, id string) (*Access, error) {
	row := s.db.QueryRowContext(ctx, `UPDATE veridian_box_access SET revoked_at = COALESCE(revoked_at, NOW())
		WHERE workspace_id = $1 AND id = $2 RETURNING `+columns, ws, id)
	a, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// Verify retrouve l'accès vivant qui correspond à l'adresse et au mot de passe. Plusieurs accès par adresse (un par
// appareil) : tous sont comparés à temps constant.
func (s *Store) Verify(ctx context.Context, address, secret string) (*Access, error) {
	address = NormalizeAddress(address)
	if address == "" || secret == "" || len(secret) > 200 || len(address) > 255 {
		return nil, ErrAuth
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+`, secret_hash FROM veridian_box_access WHERE address = $1 AND revoked_at IS NULL LIMIT 1000`, address)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	want := []byte(hashSecret(strings.TrimSpace(secret)))
	var found *Access
	for rows.Next() {
		var h string
		a, err := scan(rows, &h)
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare(want, []byte(h)) == 1 {
			found = a
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrAuth
	}
	return found, nil
}

// Alive relit un accès : révoqué ou supprimé = ErrAuth (coupe une session IMAP déjà ouverte).
func (s *Store) Alive(ctx context.Context, id string) (*Access, error) {
	a, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM veridian_box_access WHERE id = $1 AND revoked_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAuth
	}
	return a, err
}

// Touch note la dernière utilisation (au plus une écriture par minute).
func (s *Store) Touch(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE veridian_box_access SET last_used_at = NOW()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')`, id)
	return err
}

// ReserveSend prend n places sous le plafond du jour (UTC), de façon atomique. ErrCap si le plafond est atteint.
func (s *Store) ReserveSend(ctx context.Context, id string, n int) error {
	day := s.now().UTC().Format("2006-01-02")
	res, err := s.db.ExecContext(ctx, `UPDATE veridian_box_access
		SET sent_count = CASE WHEN sent_day = $2::date THEN sent_count ELSE 0 END + $3, sent_day = $2::date
		WHERE id = $1 AND revoked_at IS NULL AND can_send
		  AND CASE WHEN sent_day = $2::date THEN sent_count ELSE 0 END + $3 <= daily_cap`, id, day, n)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return ErrCap
	}
	return nil
}
