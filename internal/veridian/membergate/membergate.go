// Package membergate ferme les routes amont qui ajoutent un humain à un espace sans passer par le Hub (règle de Robert, 06/11/2026 :
// un utilisateur ne se crée, ne se modifie et ne se supprime qu'au Hub). Fiche 86.
//
//	POST /api/workspaces.inviteMember      ajoutait un compte existant tout de suite, sinon créait une invitation locale
//	POST /api/workspaces.acceptInvitation  publique à jeton : CRÉAIT l'utilisateur et l'ajoutait
//
// Elles répondent 403 `members_managed_by_hub`, avec l'adresse du Hub. L'invitation passe par POST /api/veridian/workspaces.inviteMember.
// Le fichier amont n'est pas touché : un motif « POST /chemin » est plus précis que « /chemin » pour ServeMux, il gagne.
package membergate

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Code : le code stable du refus, lisible par une machine.
const Code = "members_managed_by_hub"

const defaultHubURL = "https://app.veridian.site"

// ClosedRoutes : les routes amont fermées quand l'application est gérée par le Hub.
var ClosedRoutes = []string{"/api/workspaces.inviteMember", "/api/workspaces.acceptInvitation"}

func init() {
	platform.Register(platform.Capability{
		Name: "membergate",
		Wire: func(h platform.Host) error {
			cfg := h.Config()
			if cfg == nil || h.Mux() == nil {
				return nil
			}
			Mount(h.Mux(), cfg.HubAPISecret != "", cfg.HubBaseURL, h.Logger())
			return nil
		},
	})
}

type refusal struct {
	Error  string `json:"error"`
	Code   string `json:"code"`
	HubURL string `json:"hub_url"`
}

// Mount pose le refus. Une installation sans Hub (managed faux) garde le comportement amont.
func Mount(mux *http.ServeMux, managed bool, hubURL string, log logger.Logger) {
	if !managed {
		return
	}
	if hubURL = strings.TrimRight(strings.TrimSpace(hubURL), "/"); hubURL == "" {
		hubURL = defaultHubURL
	}
	body := refusal{
		Error:  "Les membres d'un espace se gèrent au Hub Veridian (" + hubURL + "). Le propriétaire invite depuis l'écran Membres, qui passe par le Hub ; une ancienne invitation n'est plus valable, demandez-en une nouvelle.",
		Code:   Code,
		HubURL: hubURL,
	}
	for _, route := range ClosedRoutes {
		mux.HandleFunc("POST "+route, func(w http.ResponseWriter, r *http.Request) {
			if log != nil {
				log.WithField("route", r.URL.Path).Warn("membergate: ajout d'un membre hors Hub refusé")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(body)
		})
	}
}
