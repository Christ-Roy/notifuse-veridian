package actions

import (
	"fmt"
	"net/http"
	"os"

	"github.com/Notifuse/notifuse/internal/veridian/mcp"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

const defaultAudience = "https://notifuse.app.veridian.site/api/veridian/actions"

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func init() {
	platform.Register(platform.Capability{
		Name: "actions",
		Wire: func(h platform.Host) error {
			if h.DB() == nil {
				h.Logger().Warn("actions: capacité inactive, application assemblée sans base")
				return nil
			}
			if h.Mux() == nil || h.AuthService() == nil {
				return fmt.Errorf("actions: mux ou service d'authentification absent")
			}
			Mount(h.Mux(), &Handler{
				Verifier: &mcp.Verifier{
					JWKSURL:  envOr("VERIDIAN_HUB_JWKS_URL", "https://app.veridian.site/.well-known/jwks.json"),
					Issuer:   envOr("VERIDIAN_HUB_ISSUER", "https://app.veridian.site"),
					Audience: envOr("VERIDIAN_ACTIONS_AUDIENCE", defaultAudience),
				},
				Store:      &SQLStore{DB: h.DB()},
				Dispatcher: &mcp.MuxDispatcher{Mux: h.Mux(), Auth: h.AuthService(), DB: h.DB()},
				Log:        h.Logger(),
			})
			return nil
		},
	})
}

// Mount pose la route. L'authentification est le jeton d'action du Hub.
func Mount(mux *http.ServeMux, h *Handler) {
	mux.Handle("POST /api/veridian/actions/{action}", h)
}
