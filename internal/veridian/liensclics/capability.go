package liensclics

import (
	"context"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/http"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// delaiClic borne l'écriture du clic : la redirection ne doit jamais attendre la base plus longtemps.
const delaiClic = 300 * time.Millisecond

func init() {
	platform.Register(platform.Capability{Name: "liensclics", Wire: wire})
}

// wire branche le hook de clic par lien sur la redirection (internal/http, veridian_link_click.go).
// Le hook ne renvoie jamais d'erreur ni de panique : il journalise, borne son délai, puis rend la main.
func wire(h platform.Host) error {
	ws := h.WorkspaceRepository()
	if ws == nil {
		if h.DB() == nil {
			h.Logger().Warn("liensclics: capacité inactive, application assemblée sans base")
			return nil
		}
		return fmt.Errorf("liensclics: dépôt des espaces absent")
	}
	log := h.Logger()
	var store Store
	http.SetVeridianOnLinkClick(func(ctx context.Context, workspaceID, messageID, url string, at time.Time) {
		defer func() {
			if r := recover(); r != nil {
				log.Warn(fmt.Sprintf("liensclics: clic ignoré (panique) : %v", r))
			}
		}()
		bornee, annuler := context.WithTimeout(ctx, delaiClic)
		defer annuler()
		db, err := ws.GetConnection(bornee, workspaceID)
		if err != nil {
			log.Warn(fmt.Sprintf("liensclics: base de l'espace %s indisponible : %v", workspaceID, err))
			return
		}
		if _, err := store.Lier(bornee, db, messageID, url, at); err != nil {
			log.Warn(err.Error())
		}
	})
	if h.Mux() != nil {
		(&Handler{Svc: &Rapport{Auth: h.AuthService(), Bases: ws}, RequireAuth: h.RequireAuth()}).RegisterRoutes(h.Mux())
	}
	return nil
}
