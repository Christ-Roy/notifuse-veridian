package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

const (
	defaultIssuer   = "https://app.veridian.site"
	defaultJWKSURL  = "https://app.veridian.site/.well-known/jwks.json"
	defaultAudience = "https://notifuse.app.veridian.site/api/mcp"
	callSessionTTL  = 2 * time.Minute
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func init() {
	platform.Register(platform.Capability{
		Name: "mcp",
		Wire: func(h platform.Host) error {
			if h.DB() == nil {
				h.Logger().Warn("mcp: capacite inactive, application assemblee sans base")
				return nil
			}
			if h.Mux() == nil || h.AuthService() == nil {
				return fmt.Errorf("mcp: mux ou service d'authentification absent")
			}
			Mount(h.Mux(), &Handler{
				Verifier: &Verifier{
					JWKSURL:  envOr("VERIDIAN_HUB_JWKS_URL", defaultJWKSURL),
					Issuer:   envOr("VERIDIAN_HUB_ISSUER", defaultIssuer),
					Audience: envOr("VERIDIAN_MCP_AUDIENCE", defaultAudience),
				},
				Store:      &SQLStore{DB: h.DB()},
				Dispatcher: &MuxDispatcher{Mux: h.Mux(), Auth: h.AuthService(), DB: h.DB()},
				Logger:     h.Logger(),
			})
			return nil
		},
	})
}

// Mount pose POST /api/mcp. L'authentification est celle du Hub (JWT ES256), pas celle des utilisateurs.
func Mount(mux *http.ServeMux, h *Handler) {
	mux.Handle("/api/mcp", h)
}

// SQLStore : utilisateurs et espaces dans la base systeme.
type SQLStore struct{ DB *sql.DB }

// Resolve : l'humain actif porteur de hub_user_id, puis son espace (proprietaire d'abord, le plus ancien
// ensuite ; jamais un espace supprime).
func (s *SQLStore) Resolve(ctx context.Context, hubUserID string) (*Member, error) {
	m := &Member{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, email FROM users WHERE hub_user_id = $1 AND type = 'user' AND hub_deleted_at IS NULL`, hubUserID).
		Scan(&m.UserID, &m.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownUser
	}
	if err != nil {
		return nil, err
	}
	var perms domain.UserPermissions
	uw := &domain.UserWorkspace{UserID: m.UserID}
	err = s.DB.QueryRowContext(ctx, `
		SELECT uw.workspace_id, uw.role, uw.permissions
		FROM user_workspaces uw
		JOIN workspaces w ON w.id = uw.workspace_id
		LEFT JOIN veridian_plan p ON p.workspace_id = uw.workspace_id
		WHERE uw.user_id = $1 AND p.deleted_at IS NULL
		ORDER BY (uw.role = 'owner') DESC, uw.created_at ASC, uw.workspace_id ASC LIMIT 1`, m.UserID).
		Scan(&uw.WorkspaceID, &uw.Role, &perms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoWorkspace
	}
	if err != nil {
		return nil, err
	}
	uw.Permissions = perms
	m.Workspace = uw
	return m, nil
}

// MuxDispatcher rejoue l'appel sur le routeur de l'app avec une session de 2 minutes supprimee aussitot :
// les droits, la validation et les gardes sont ceux de l'upstream.
type MuxDispatcher struct {
	Mux  http.Handler
	Auth *service.AuthService
	DB   *sql.DB
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedResponse) WriteHeader(code int)        { b.status = code }

func (d *MuxDispatcher) Do(ctx context.Context, m *Member, method, path string, query map[string][]string, body []byte) (int, []byte, error) {
	sessionID := uuid.NewString()
	expires := time.Now().UTC().Add(callSessionTTL)
	if _, err := d.DB.ExecContext(ctx,
		`INSERT INTO user_sessions (id, user_id, expires_at, created_at) VALUES ($1, $2, $3, NOW())`, sessionID, m.UserID, expires); err != nil {
		return 0, nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = d.DB.ExecContext(cleanup, `DELETE FROM user_sessions WHERE id = $1`, sessionID)
	}()
	token := d.Auth.GenerateUserAuthToken(&domain.User{ID: m.UserID, Email: m.Email}, sessionID, expires)
	if token == "" {
		return 0, nil, errors.New("mcp: jeton interne non emis")
	}

	target := path
	if len(query) > 0 {
		target += "?" + url.Values(query).Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := &bufferedResponse{header: http.Header{}, status: http.StatusOK}
	d.Mux.ServeHTTP(rec, req)
	return rec.status, rec.body.Bytes(), nil
}
