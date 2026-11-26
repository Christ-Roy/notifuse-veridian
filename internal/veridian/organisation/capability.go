package organisation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func init() {
	platform.Register(platform.Capability{
		Name: "organisation",
		Wire: func(h platform.Host) error {
			if h.DB() == nil {
				h.Logger().Warn("organisation: capacité inactive, application assemblée sans base")
				return nil
			}
			if h.Mux() == nil || h.AuthService() == nil || h.WorkspaceRepository() == nil {
				return fmt.Errorf("organisation: mux, authentification ou dépôt absent")
			}
			secret := h.Config().HubAPISecret
			Mount(h.Mux(), &Handler{
				Store:     &SQLStore{DB: h.DB()},
				Auth:      h.AuthService(),
				Subs:      repository.NewWebhookSubscriptionRepository(h.WorkspaceRepository()),
				AddMember: syncMember(h.Mux(), secret),
				HubURL:    h.Config().HubBaseURL,
				Log:       h.Logger(),
			}, secret, h.RequireAuth())
			return nil
		},
	})
}

// Mount pose les routes. Sans secret Hub, les routes signées répondent 503 (middleware).
func Mount(mux *http.ServeMux, h *Handler, hubSecret string, requireAuth func(http.Handler) http.Handler) {
	signed := middleware.VeridianHMACMiddleware(hubSecret)
	mux.Handle("POST /api/hub/organisations/sync", signed(http.HandlerFunc(h.Sync)))
	mux.Handle("GET /api/hub/organisations/sync", signed(http.HandlerFunc(h.Version)))
	mux.Handle("POST /api/hub/evenements/abonnement", signed(http.HandlerFunc(h.Subscription)))
	mux.Handle("GET /api/veridian/organisation", requireAuth(http.HandlerFunc(h.Get)))
}

// syncMember rejoue la route signée existante (CONTRAT-HUB §5.18) : un seul chemin d'ajout, ses gardes et son journal.
func syncMember(mux http.Handler, secret string) func(context.Context, string, Member) error {
	return func(ctx context.Context, ws string, m Member) error {
		if m.Email == "" || m.HubUserID == "" {
			return fmt.Errorf("email et hub_user_id requis")
		}
		role := "member"
		if m.Role == "admin" {
			role = "admin"
		}
		body, err := json.Marshal(map[string]any{"email": m.Email, "hub_user_id": m.HubUserID, "email_proven": m.EmailProven, "role": role})
		if err != nil {
			return err
		}
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/tenants/"+url.PathEscape(ws)+"/sync-member", bytes.NewReader(body))
		if err != nil {
			return err
		}
		r.Header.Set("Content-Type", "application/json")
		middleware.SignVeridianHubRequest(r, secret, body) // v2 : liée à la méthode et au chemin (26/11)
		rec := &statusOnly{header: http.Header{}, code: http.StatusOK}
		mux.ServeHTTP(rec, r)
		if rec.code >= 300 {
			return fmt.Errorf("sync-member : %d", rec.code)
		}
		return nil
	}
}

// statusOnly : la réponse n'est lue que par son statut.
type statusOnly struct {
	header http.Header
	code   int
}

func (s *statusOnly) Header() http.Header         { return s.header }
func (s *statusOnly) Write(p []byte) (int, error) { return len(p), nil }
func (s *statusOnly) WriteHeader(c int)           { s.code = c }
