package domain

// VeridianSuspendReasonOrganisation : le motif que le Hub pose quand il suspend une ORGANISATION (VER-245, CONTRAT-HUB
// « Suspension »). Tout l'espace se ferme, lecture comprise. Les autres motifs (billing_past_due, quota_exceeded) restent
// une suspension de facturation : seuls les envois sont bloqués (§5.4).
const VeridianSuspendReasonOrganisation = "admin_action"

// VeridianOrganisationSuspended dit si l'espace est fermé par une suspension d'organisation.
func (p *VeridianPlan) VeridianOrganisationSuspended() bool {
	return p != nil && p.DeletedAt == nil && p.Status == PlanStatusSuspended && p.SuspendedReason == VeridianSuspendReasonOrganisation
}
