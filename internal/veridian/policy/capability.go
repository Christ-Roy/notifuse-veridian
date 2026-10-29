package policy

import (
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func init() {
	platform.Register(platform.Capability{Name: "policy", Wire: wire})
}

// wire branche le module : le résolveur devient le fournisseur de politique du worker et de
// priorité des enqueues, les routes sont montées sur le mux.
func wire(h platform.Host) error {
	ws := h.WorkspaceRepository()
	auth := h.AuthService()
	log := h.Logger()
	if ws == nil || auth == nil {
		if h.DB() == nil {
			// Assemblage sans base système (tests d'assemblage de l'application) : rien à servir.
			log.Warn("policy: capacité inactive, application assemblée sans base ni services")
			return nil
		}
		// Application réelle : l'absence d'une dépendance est un défaut d'assemblage, on échoue vite.
		return fmt.Errorf("policy: dépôt de workspaces ou service d'authentification absent")
	}
	store := NewStore(ws)
	resolver := NewResolver(store, log, 0)
	svc := NewService(auth, ws, store, repository.NewVeridianEngagementByClassRepository(ws), resolver, log)

	domain.SetVeridianPolicyProvider(resolver)
	domain.SetVeridianSendPriorityProvider(resolver)

	NewHandler(svc, h.JWTSecret(), log).RegisterRoutes(h.Mux())
	return nil
}
