package reconcile

import (
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// Câblage : une capacité enregistrée auprès de la plateforme (L0). Elle pose les trois routes
// /api/veridian/reconcile.{run,report,resolve}. Aucune boucle de fond ni tâche planifiée : le
// passage se lance à la demande (API, CLI, console).
func init() {
	platform.Register(platform.Capability{
		Name: "reconcile",
		Wire: func(h platform.Host) error {
			// Assemblage de test ou sans base : ce dont dépend la capacité manque, on ne
			// pose aucune route (jamais de panne au démarrage pour un hôte incomplet).
			if h.Mux() == nil || h.AuthService() == nil || h.WorkspaceRepository() == nil {
				return nil
			}
			svc := &Service{
				Auth:  h.AuthService(),
				Conn:  h.WorkspaceRepository(),
				Stats: repository.NewVeridianProspectionStatsRepository(h.WorkspaceRepository()),
				Log:   h.Logger(),
				Pause: confirmPause,
			}
			(&Handler{Svc: svc, RequireAuth: h.RequireAuth(), Log: h.Logger()}).RegisterRoutes(h.Mux())
			return nil
		},
	})
}
