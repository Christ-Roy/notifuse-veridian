package iafils

import (
	"fmt"
	"net/http"

	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// DefaultHubURL : la racine du Hub en prod (même défaut que le client d'invitation ; HUB_BASE_URL le surcharge). La CSP de la console
// n'autorise le cadre que vers cette origine (frame-src, fiche 96).
const DefaultHubURL = "https://app.veridian.site"

func init() {
	platform.Register(platform.Capability{
		Name: "iafils",
		Wire: func(h platform.Host) error {
			if h.DB() == nil {
				h.Logger().Warn("iafils: capacité inactive, application assemblée sans base")
				return nil
			}
			if h.Mux() == nil || h.AuthService() == nil {
				return fmt.Errorf("iafils: mux ou authentification absent")
			}
			cfg := h.Config()
			hub := cfg.HubBaseURL
			if hub == "" {
				hub = DefaultHubURL
			}
			st := &Store{DB: h.DB()}
			Mount(h.Mux(), &Handler{Store: st, Log: h.Logger()}, &EtatHandler{
				Store: st, Auth: h.AuthService(), Client: &EtatClient{BaseURL: hub, Secret: cfg.HubAPISecret}, HubURL: hub, Log: h.Logger(),
			}, cfg.HubAPISecret, h.RequireAuth())
			return nil
		},
	})
}

// Mount pose les trois routes. Les fils : signature Hub v2 (sans signature valide, 401 ; sans secret, 503), jamais d'origine navigateur.
func Mount(mux *http.ServeMux, f *Handler, e *EtatHandler, hubSecret string, requireAuth func(http.Handler) http.Handler) {
	signed := func(h http.HandlerFunc) http.Handler {
		return RefuseBrowser(middleware.VeridianHMACMiddleware(hubSecret)(h))
	}
	mux.Handle("GET /api/hub/ia/fils/{session}", signed(f.Get))
	mux.Handle("PUT /api/hub/ia/fils/{session}", signed(f.Put))
	mux.Handle("GET /api/veridian/ia/etat", requireAuth(http.HandlerFunc(e.Get)))
}
