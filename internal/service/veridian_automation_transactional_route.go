package service

import "github.com/Notifuse/notifuse/internal/domain"

// veridianRouteTransactionalEmailNode choisit le transport d'un noeud email de
// sequence selon la categorie de son modele (lot 4, 08/10/2026). Un modele de
// categorie "transactional" part par le profil transactionnel RESERVE du workspace
// et le marque (le worker l'enverra sans aucune porte commerciale) ; tout autre
// modele, ou un workspace sans profil reserve, garde le transport decide par le
// noeud : comportement natif, strictement inchange.
func veridianRouteTransactionalEmailNode(
	workspace *domain.Workspace,
	templateCategory string,
	provider *domain.EmailProvider,
	integrationID string,
) (*domain.EmailProvider, string, bool) {
	if !domain.VeridianIsTransactionalCategory(templateCategory) {
		return provider, integrationID, false
	}
	reserved := workspace.VeridianReservedTransactionalProfileID()
	if reserved == "" {
		return provider, integrationID, false
	}
	integration := workspace.GetIntegrationByID(reserved)
	if integration == nil {
		return provider, integrationID, false
	}
	return &integration.EmailProvider, reserved, true
}
