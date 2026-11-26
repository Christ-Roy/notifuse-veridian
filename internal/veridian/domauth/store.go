package domauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Notifuse/notifuse/pkg/crypto"
)

// Store : table système veridian_sending_domain (V83). La clé privée y est chiffrée par SECRET_KEY (AES-GCM, même mécanisme que les
// autres secrets du fork) ; elle n'en sort que déchiffrée en mémoire, pour signer.
type Store struct {
	DB     *sql.DB
	Secret string
	Now    func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

const cols = `id, workspace_id, domain, selector, public_key, status, checks, created_by, created_at, last_checked_at, verified_at, lost_at, lost_reason`

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (*Domain, error) {
	var d Domain
	var checks []byte
	var last, ver, lost sql.NullTime
	if err := r.Scan(&d.ID, &d.WorkspaceID, &d.Domain, &d.Selector, &d.PublicKey, &d.Status, &checks, &d.CreatedBy, &d.CreatedAt, &last, &ver, &lost, &d.LostReason); err != nil {
		return nil, err
	}
	if len(checks) > 0 {
		_ = json.Unmarshal(checks, &d.Checks)
	}
	if d.Checks == nil {
		d.Checks = []Check{}
	}
	if last.Valid {
		t := last.Time
		d.LastCheckedAt = &t
	}
	if ver.Valid {
		t := ver.Time
		d.VerifiedAt = &t
	}
	if lost.Valid {
		t := lost.Time
		d.LostAt = &t
	}
	return &d, nil
}

// Create génère la clé, la chiffre et enregistre le domaine (statut en attente).
func (s *Store) Create(ctx context.Context, ws, rawDomain, createdBy string) (*Domain, error) {
	if s.Secret == "" {
		return nil, errors.New("SECRET_KEY requis pour chiffrer la clé DKIM")
	}
	name, err := NormalizeDomain(rawDomain)
	if err != nil {
		return nil, err
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM veridian_sending_domain WHERE workspace_id = $1`, ws).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxPerWorkspace {
		return nil, ErrTooMany
	}
	now := s.now()
	sel, err := NewSelector(now)
	if err != nil {
		return nil, err
	}
	priv, pub, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	enc, err := crypto.EncryptString(priv, s.Secret)
	if err != nil {
		return nil, fmt.Errorf("chiffrement de la clé DKIM: %w", err)
	}
	row := s.DB.QueryRowContext(ctx, `INSERT INTO veridian_sending_domain (id, workspace_id, domain, selector, public_key, encrypted_private_key, status, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (workspace_id, domain) DO NOTHING RETURNING `+cols,
		uuid.NewString(), ws, name, sel, pub, enc, StatusPending, createdBy, now)
	d, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExists
	}
	return d, err
}

// List : les domaines d'un espace, par nom.
func (s *Store) List(ctx context.Context, ws string) ([]Domain, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+cols+` FROM veridian_sending_domain WHERE workspace_id = $1 ORDER BY domain`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// Get : un domaine de CET espace (un id d'un autre espace répond introuvable).
func (s *Store) Get(ctx context.Context, ws, id string) (*Domain, error) {
	d, err := scan(s.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM veridian_sending_domain WHERE workspace_id = $1 AND id = $2`, ws, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// SaveCheck enregistre le relevé DNS et le statut qui en découle. Un domaine qui passe de vérifié à non vérifié garde la date du constat
// et le verdict DKIM qui l'a fait tomber (lost_at, lost_reason) ; redevenu vérifié, les deux sont effacés.
func (s *Store) SaveCheck(ctx context.Context, ws, id, status string, checks []Check) (*Domain, error) {
	raw, err := json.Marshal(checks)
	if err != nil {
		return nil, err
	}
	reason := ""
	for _, c := range checks {
		if c.Kind == KindDKIM {
			reason = c.Verdict
		}
	}
	// Dans un UPDATE, « status » à droite est l'ancienne valeur : c'est elle qui dit si le domaine vient de PERDRE sa vérification.
	d, err := scan(s.DB.QueryRowContext(ctx, `UPDATE veridian_sending_domain SET status = $3::varchar, checks = $4::jsonb, last_checked_at = $5::timestamptz,
		verified_at = CASE WHEN $3::varchar = 'verified' THEN COALESCE(verified_at, $5::timestamptz) ELSE NULL END,
		lost_at = CASE WHEN $3::varchar = 'verified' THEN NULL WHEN status = 'verified' THEN $5::timestamptz ELSE lost_at END,
		lost_reason = CASE WHEN $3::varchar = 'verified' THEN '' WHEN status = 'verified' THEN $6::varchar ELSE lost_reason END
		WHERE workspace_id = $1 AND id = $2 RETURNING `+cols, ws, id, status, raw, s.now(), reason))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// Delete retire le domaine et sa clé : les mails suivants ne sont plus signés pour lui.
func (s *Store) Delete(ctx context.Context, ws, id string) (*Domain, error) {
	d, err := scan(s.DB.QueryRowContext(ctx, `DELETE FROM veridian_sending_domain WHERE workspace_id = $1 AND id = $2 RETURNING `+cols, ws, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// SigningKey : clé du domaine VÉRIFIÉ de cet espace, avec son id et la date de son dernier contrôle ; nil si le domaine n'y est pas vérifié.
func (s *Store) SigningKey(ctx context.Context, ws, domain string) (*SigningKey, error) {
	var k SigningKey
	var enc string
	var last sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT id, selector, encrypted_private_key, last_checked_at FROM veridian_sending_domain
		WHERE workspace_id = $1 AND domain = $2 AND status = 'verified'`, ws, strings.ToLower(domain)).Scan(&k.ID, &k.Selector, &enc, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	plain, err := crypto.DecryptFromHexString(enc, s.Secret)
	if err != nil {
		return nil, errors.New("déchiffrement de la clé DKIM impossible (SECRET_KEY)")
	}
	if k.Key, err = ParsePrivateKey(plain); err != nil {
		return nil, err
	}
	if last.Valid {
		k.LastChecked = last.Time
	}
	return &k, nil
}
