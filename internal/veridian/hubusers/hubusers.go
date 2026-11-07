// Package hubusers expose au Hub les deux routes d'identité du CONTRAT-HUB §3.8 :
//
//	GET  /api/hub/users        {users:[{hub_user_id,email,name}]}  humains seulement
//	POST /api/hub/users/sync   {hub_user_id,name?,email?,deleted?} 200 | 404 user_not_in_app
//
// HMAC Hub obligatoire (une route sans signature valide ne répond pas). Le compte visé est retrouvé
// par hub_user_id uniquement, jamais par email ; seuls les champs présents sont appliqués. Fiche 80.
package hubusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/google/uuid"
)

var (
	// ErrNotFound : aucun compte local ne porte ce hub_user_id.
	ErrNotFound = errors.New("hubusers: user not in app")
	// ErrEmailTaken : l'adresse appartient déjà à un autre compte local.
	ErrEmailTaken = errors.New("hubusers: email already used by another account")
)

// Account : un humain local, vu par le Hub.
type Account struct {
	ID        string // users.id, interne, jamais exposé
	HubUserID string
	Email     string
	Name      string
	Deleted   bool // users.hub_deleted_at posé
}

// Store : accès aux comptes locaux (base système).
type Store interface {
	ListHumans(ctx context.Context) ([]Account, error)
	FindByHubUserID(ctx context.Context, hubUserID string) (*Account, error)
	// UpdateIdentity n'applique que ce qui est demandé : setName avec name nil efface le nom, email nil
	// laisse l'adresse. ErrEmailTaken si l'adresse est prise.
	UpdateIdentity(ctx context.Context, userID string, setName bool, name *string, email *string) error
	// Revoke coupe toutes les sessions du compte et le marque supprimé côté Hub. Aucune donnée supprimée.
	Revoke(ctx context.Context, userID string) error
}

// SQLStore : Store sur la base système.
type SQLStore struct{ DB *sql.DB }

func (s *SQLStore) ListHumans(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT hub_user_id, email, COALESCE(name, '')
		FROM users WHERE type = 'user' AND hub_deleted_at IS NULL ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var hub sql.NullString
		var a Account
		if err := rows.Scan(&hub, &a.Email, &a.Name); err != nil {
			return nil, err
		}
		a.HubUserID = hub.String
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLStore) FindByHubUserID(ctx context.Context, hubUserID string) (*Account, error) {
	var a Account
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, email, COALESCE(name, ''), hub_deleted_at IS NOT NULL FROM users
		WHERE hub_user_id = $1 AND type = 'user'`, hubUserID).Scan(&a.ID, &a.Email, &a.Name, &a.Deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.HubUserID = hubUserID
	return &a, nil
}

func (s *SQLStore) UpdateIdentity(ctx context.Context, userID string, setName bool, name *string, email *string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE users SET
			name = CASE WHEN $2::boolean THEN $3 ELSE name END,
			email = COALESCE($4, email),
			updated_at = NOW()
		WHERE id = $1`, userID, setName, name, email)
	if err != nil && strings.Contains(err.Error(), "users_email_key") {
		return ErrEmailTaken
	}
	return err
}

func (s *SQLStore) Revoke(ctx context.Context, userID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET hub_deleted_at = NOW(), updated_at = NOW() WHERE id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// Handler : les deux routes. Le middleware HMAC est posé par l'appelant (wire.go).
type Handler struct{ Store Store }

type listResponse struct {
	Users []userJSON `json:"users"`
}

type userJSON struct {
	HubUserID *string `json:"hub_user_id"`
	Email     string  `json:"email"`
	Name      *string `json:"name"`
}

// List : GET /api/hub/users.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.Store.ListHumans(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	resp := listResponse{Users: make([]userJSON, 0, len(accounts))}
	for _, a := range accounts {
		u := userJSON{Email: a.Email}
		if a.Name != "" {
			n := a.Name
			u.Name = &n
		}
		if a.HubUserID != "" {
			id := a.HubUserID
			u.HubUserID = &id
		}
		resp.Users = append(resp.Users, u)
	}
	writeJSON(w, http.StatusOK, resp)
}

type syncRequest struct {
	HubUserID string          `json:"hub_user_id"`
	Name      json.RawMessage `json:"name"` // absent = intact, null = effacé
	Email     *string         `json:"email"`
	Deleted   bool            `json:"deleted"`
}

// Sync : POST /api/hub/users/sync.
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	var req syncRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_payload"})
		return
	}
	req.HubUserID = strings.TrimSpace(req.HubUserID)
	if _, err := uuid.Parse(req.HubUserID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_payload", "hint": "hub_user_id must be a uuid"})
		return
	}
	var email *string
	if req.Email != nil {
		e := strings.ToLower(strings.TrimSpace(*req.Email))
		if _, err := mail.ParseAddress(e); err != nil || len(e) > 255 || !strings.Contains(e, "@") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_payload", "hint": "email is invalid"})
			return
		}
		email = &e
	}
	setName := len(req.Name) > 0
	var name *string
	if setName && string(req.Name) != "null" {
		var n string
		if err := json.Unmarshal(req.Name, &n); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_payload", "hint": "name must be a string or null"})
			return
		}
		n = strings.TrimSpace(n)
		if len(n) > 255 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_payload", "hint": "name too long"})
			return
		}
		name = &n
	}

	acct, err := h.Store.FindByHubUserID(r.Context(), req.HubUserID)
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user_not_in_app"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	// Un compte supprimé ne reprend pas vie : nom ou email poussés après coup = compte absent.
	if acct.Deleted && (setName || email != nil) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user_not_in_app"})
		return
	}

	// Idempotent : "updated" dit si quelque chose a réellement changé.
	updated := false
	nameChanges := setName && ((name == nil && acct.Name != "") || (name != nil && *name != acct.Name))
	emailChanges := email != nil && *email != strings.ToLower(acct.Email)
	if nameChanges || emailChanges {
		var emailArg *string
		if emailChanges {
			emailArg = email
		}
		if err := h.Store.UpdateIdentity(r.Context(), acct.ID, nameChanges, name, emailArg); err != nil {
			if errors.Is(err, ErrEmailTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "email_conflict"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		updated = true
	}
	if req.Deleted && !acct.Deleted {
		if err := h.Store.Revoke(r.Context(), acct.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		updated = true
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": updated})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
