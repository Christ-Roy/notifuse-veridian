package domain

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Notifuse/notifuse/pkg/netguard"
)

// ValidateTenantBaseURL valide une URL de base choisie par un locataire pour un
// service appele PAR LE SERVEUR (Firecrawl auto-heberge, endpoint compatible
// OpenAI). Fiche 67/68 : l'erreur doit etre claire A LA SAISIE (avant, l'URL
// interne etait acceptee puis echouait en silence a l'usage). Vide = defaut du
// fournisseur, accepte. Controle de destination au moment de la connexion : voir
// service.NewTenantOutboundClient.
func ValidateTenantBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("base URL is not a valid URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("base URL must start with https:// (or http://)")
	}
	if u.User != nil {
		return fmt.Errorf("base URL must not contain credentials")
	}
	port := 443
	if u.Scheme == "http" {
		port = 80
	}
	if p := u.Port(); p != "" {
		if n, perr := strconv.Atoi(p); perr == nil {
			port = n
		}
	}
	if err := netguard.ValidateHost(u.Hostname(), port, nil); err != nil {
		return fmt.Errorf("base URL refused: %w", err)
	}
	return nil
}
