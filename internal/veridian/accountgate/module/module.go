// Package module pose la date de la garde des comptes supprimés au Hub (capacité « accountgate », fiche 91).
package module

import (
	"context"
	"os"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/accountgate"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// countTimeout borne le comptage du démarrage.
const countTimeout = 10 * time.Second

func init() { platform.Register(platform.Capability{Name: "accountgate", Wire: wire}) }

// wire lit la date. Une erreur empêche le démarrage : la version d'avant reste servie, rien ne tourne sans garde.
func wire(h platform.Host) error {
	cfg := h.Config()
	t, err := accountgate.ReadSince(os.Getenv(accountgate.SinceVar), cfg.IsProduction() && cfg.HubAPISecret != "")
	if err != nil {
		return err
	}
	accountgate.SetSince(t)
	if h.DB() == nil {
		return nil // assemblage de test
	}
	ctx, cancel := context.WithTimeout(context.Background(), countTimeout)
	defer cancel()
	n, err := accountgate.CountOpen(ctx, h.DB())
	log := h.Logger().WithField("since", accountgate.Since().Format(time.RFC3339))
	switch {
	case err != nil:
		log.WithField("error", err.Error()).Warn("Account gate: accounts left open not counted")
	case n > 0:
		log.WithField("left_open", n).Warn("Account gate: accounts deleted at the Hub before the date are still open")
	}
	return nil
}
