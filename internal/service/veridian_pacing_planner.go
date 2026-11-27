package service

import (
	"context"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service/queue"
)

// Veridian fork (27/11/2026) : planificateur de l'étalement des envois commerciaux (queue/veridian_pacing.go).
// Il lit la capacité du jour de chaque profil dans le MÊME calcul que l'écran Profils d'envoi (fiche 60 :
// veridianComputeProfilesOverview, plans de queue.VeridianEffectivePlan), sans la porte d'authentification de
// l'API : c'est un appel interne du worker.

type veridianPacingPlanner struct {
	s *veridianEmailProfileOverviewService
}

// NewVeridianPacingPlanner rend le planificateur, ou nil si svc n'est pas le service d'aperçu de ce paquet.
func NewVeridianPacingPlanner(svc domain.VeridianEmailProfileOverviewService) queue.PacingPlanner {
	inner, ok := svc.(*veridianEmailProfileOverviewService)
	if !ok || inner == nil {
		return nil
	}
	return veridianPacingPlanner{s: inner}
}

// ProfilePace : pour chaque profil, envois utiles restants aujourd'hui et minutes d'ouverture restantes.
func (p veridianPacingPlanner) ProfilePace(ctx context.Context, workspaceID string) (map[string]queue.VeridianPaceInput, error) {
	overview, _, _, err := p.s.veridianComputeProfilesOverview(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return veridianPaceFromOverview(overview), nil
}

// veridianPaceFromOverview : extraction PURE (testée sans base).
func veridianPaceFromOverview(overview *domain.VeridianEmailProfilesOverview) map[string]queue.VeridianPaceInput {
	out := map[string]queue.VeridianPaceInput{}
	if overview == nil {
		return out
	}
	for _, pr := range overview.Profiles {
		c := pr.Plan.Capacity
		in := queue.VeridianPaceInput{MinutesLeft: float64(c.WindowMinutesLeft)}
		if c.Applicable && c.RemainingUseful != nil {
			in.Remaining, in.Known = *c.RemainingUseful, true
		}
		out[pr.IntegrationID] = in
	}
	return out
}
