package reservoir

import (
	"fmt"

	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func init() {
	platform.Register(platform.Capability{Name: "reservoir", Wire: wire})
}

func wire(h platform.Host) error {
	ws, auth, contacts := h.WorkspaceRepository(), h.AuthService(), h.ContactRepository()
	if ws == nil || auth == nil || contacts == nil {
		if h.DB() == nil {
			h.Logger().Warn("reservoir: capacité inactive, application assemblée sans base ni services")
			return nil
		}
		return fmt.Errorf("reservoir: dépôt ou service d'authentification absent")
	}
	svc := &Service{Auth: auth, Store: &Store{WS: ws}, Contacts: contacts, Log: h.Logger()}
	(&Handler{Svc: svc, RequireAuth: h.RequireAuth(), Log: h.Logger()}).RegisterRoutes(h.Mux())
	return nil
}
