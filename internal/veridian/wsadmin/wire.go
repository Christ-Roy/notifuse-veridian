package wsadmin

import (
	"errors"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func init() {
	platform.Register(platform.Capability{
		Name: "wsadmin",
		Wire: func(h platform.Host) error {
			if h.DB() == nil {
				h.Logger().Warn("wsadmin: capacité inactive, application assemblée sans base")
				return nil
			}
			if h.Mux() == nil {
				return errors.New("wsadmin: mux absent")
			}
			// Les gardes (retrait jamais défait, transfert sans coupure) lisent le registre par le dépôt : sans lui elles se tairaient.
			if _, ok := h.WorkspaceRepository().(domain.VeridianAdminLedger); !ok {
				h.Logger().Warn("wsadmin: dépôt des espaces sans registre des admins, capacité inactive")
				return nil
			}
			Mount(h.Mux(), &Handler{Store: &Store{DB: h.DB()}, Log: h.Logger()}, h.Config().HubAPISecret)
			return nil
		},
	})
}

// Mount pose les deux routes sous HMAC Hub. Sans secret configuré le middleware répond 503.
func Mount(mux *http.ServeMux, h *Handler, hubSecret string) {
	hmac := middleware.VeridianHMACMiddleware(hubSecret)
	mux.Handle("POST /api/hub/workspace-admins", hmac(http.HandlerFunc(h.Post)))
	mux.Handle("GET /api/hub/workspace-admins", hmac(http.HandlerFunc(h.Get)))
}
