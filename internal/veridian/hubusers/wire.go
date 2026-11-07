package hubusers

import (
	"fmt"
	"net/http"

	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func init() {
	platform.Register(platform.Capability{
		Name: "hubusers",
		Wire: func(h platform.Host) error {
			if h.DB() == nil {
				// Assemblage sans base système (tests d'assemblage de l'application) : rien à servir.
				h.Logger().Warn("hubusers: capacité inactive, application assemblée sans base")
				return nil
			}
			if h.Mux() == nil {
				return fmt.Errorf("hubusers: mux absent")
			}
			Mount(h.Mux(), &Handler{Store: &SQLStore{DB: h.DB()}}, h.Config().HubAPISecret)
			return nil
		},
	})
}

// Mount pose les deux routes sous HMAC Hub. Sans secret configuré le middleware répond 503.
func Mount(mux *http.ServeMux, h *Handler, hubSecret string) {
	hmac := middleware.VeridianHMACMiddleware(hubSecret)
	mux.Handle("GET /api/hub/users", hmac(http.HandlerFunc(h.List)))
	mux.Handle("POST /api/hub/users/sync", hmac(http.HandlerFunc(h.Sync)))
}
