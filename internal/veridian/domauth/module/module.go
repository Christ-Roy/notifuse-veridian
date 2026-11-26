// Package module branche les domaines d'envoi authentifiés (capacité « domauth », VER-166, fiche 97) : routes sendingDomains.* et
// signature DKIM des mails sortants. Importé à blanc par internal/app/veridian_mod_domauth.go.
package module

import (
	"os"
	"strings"

	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/domauth"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox/mailapi"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/internal/veridian/standing"
)

func init() {
	platform.Register(platform.Capability{Name: "domauth", Wire: wire})
}

// Include : notre SPF (VERIDIAN_SPF_INCLUDE, défaut domauth.DefaultSPFInclude).
func Include(get func(string) string) string {
	if v := strings.TrimSpace(get("VERIDIAN_SPF_INCLUDE")); v != "" {
		return v
	}
	return domauth.DefaultSPFInclude
}

func wire(h platform.Host) error {
	if h.DB() == nil || h.Mux() == nil || h.AuthService() == nil || h.Config() == nil {
		return nil // assemblage de test
	}
	store := &domauth.Store{DB: h.DB(), Secret: h.Config().Security.SecretKey}
	log := h.Logger()
	signer := &domauth.Signer{Keys: store, OnError: func(ws, d string, err error) {
		log.WithFields(map[string]interface{}{"workspace_id": ws, "domain": d, "error": err.Error()}).Error("DKIM: signature impossible, mail envoyé sans notre signature")
	}}
	include := Include(os.Getenv)
	api := &domauth.Handler{Store: store, DNS: &domauth.Verifier{Include: include}, Include: include, Log: log, RequireAuth: h.RequireAuth(),
		Auth: mailapi.DomainAuthorizer{Auth: h.AuthService(), Standing: &standing.Store{DB: h.DB()}}, Changed: signer.Forget}
	api.RegisterRoutes(h.Mux())
	service.SetVeridianOutgoingSigner(signer.Sign)
	return nil
}
