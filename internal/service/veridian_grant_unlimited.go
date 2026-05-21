package service

// === Veridian patch ===
// GrantUnlimited : passe un tenant en plan=enterprise + quota=-1 +
// plan_source=lifetime_partner (par defaut). Reservé à l'équipe interne
// Veridian et aux clients fideles / partenaires (cf. CLAUDE.md "comptes
// privilegies").
//
// Convention : reside dans son propre fichier veridian_grant_unlimited.go
// (et pas dans veridian_service.go) car c'est une feature Veridian-only qui
// n'a aucun equivalent upstream. La methode est attachee a *veridianService
// (defini dans veridian_service.go), donc le receiver est dispo sans
// modification de la struct.

import (
	"context"
	"errors"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// ErrInvalidPlanSourceForGrant : plan_source non immune fournie (ex: stripe).
// Un grant ne peut etre attribue qu'a un plan_source immune.
var ErrInvalidPlanSourceForGrant = errors.New("invalid plan_source for grant: must be immune (lifetime_partner, lifetime_site_vitrine, manual, internal)")

// ErrGrantReasonRequired : reason vide refuse pour audit GDPR/compta.
var ErrGrantReasonRequired = errors.New("reason is required for grant audit trail")

// GrantUnlimited offre un acces illimite au tenant. Idempotent.
func (s *veridianService) GrantUnlimited(ctx context.Context, input domain.GrantUnlimitedInput) (*domain.GrantUnlimitedResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.Reason == "" {
		return nil, ErrGrantReasonRequired
	}

	// Default plan_source = lifetime_partner. Refuse stripe explicitement
	// (un grant manuel ne peut pas etre attribue a une subscription Stripe).
	source := input.PlanSource
	if source == "" {
		source = domain.PlanSourceLifetimePartner
	}
	if !source.IsValid() {
		return nil, ErrInvalidPlanSourceForGrant
	}
	if !source.IsImmune() {
		return nil, ErrInvalidPlanSourceForGrant
	}

	// Lire l'etat actuel pour audit + decider si on doit Resume.
	existing, err := s.planRepo.Get(ctx, input.TenantID)
	if err != nil {
		return nil, err // sql.ErrNoRows → handler mappe 404
	}

	const unlimitedQuota = int64(-1)
	const enterprisePlan = "enterprise"

	// UpdatePlan ecrit plan + quota + plan_source. Le repo gere atomiquement.
	if err := s.planRepo.UpdatePlan(ctx, input.TenantID, enterprisePlan, unlimitedQuota, source); err != nil {
		return nil, err
	}

	// Si le tenant etait suspended, le resume pour que le grant ait un effet
	// visible immediat (le paywall debloque sur status=active + quota=-1).
	if existing.Status == domain.PlanStatusSuspended {
		if err := s.planRepo.Resume(ctx, input.TenantID); err != nil {
			// Best-effort : on log mais on continue (le plan a deja ete mis a jour).
			if s.logger != nil {
				s.logger.WithFields(map[string]interface{}{
					"tenant_id": input.TenantID,
					"error":     err.Error(),
				}).Warn("grant_unlimited: resume failed but plan updated")
			}
		}
	}

	// Emit event pour audit trail Hub.
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantPlanChanged, input.TenantID, map[string]interface{}{
			"plan":          enterprisePlan,
			"previous_plan": existing.Plan,
			"plan_source":   string(source),
			"quota":         unlimitedQuota,
			"reason":        input.Reason,
			"grant_type":    "unlimited",
		})
	}

	// Marquer le sync Hub réussi (best-effort).
	s.touchHubSync(ctx, input.TenantID)

	return &domain.GrantUnlimitedResponse{
		TenantID:     input.TenantID,
		Plan:         enterprisePlan,
		PreviousPlan: existing.Plan,
		PlanSource:   source,
		Quota:        unlimitedQuota,
		GrantedAt:    time.Now().UTC(),
		Reason:       input.Reason,
	}, nil
}
