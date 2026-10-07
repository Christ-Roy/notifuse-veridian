package domain

import (
	"fmt"
	"strings"
)

// VeridianEmailProfile is one eligible marketing transport. IntegrationID is
// the stable profile identity used by queue payloads, quota counters and logs.
type VeridianEmailProfile struct {
	IntegrationID string
	Provider      *EmailProvider
}

// VeridianMarketingEmailProfiles resolves the configured pool in declared
// order. Invalid, duplicate and non-email IDs are ignored defensively. An empty
// explicit pool falls back to the upstream singleton setting.
func (w *Workspace) VeridianMarketingEmailProfiles() []VeridianEmailProfile {
	if w == nil {
		return nil
	}
	ids := w.Settings.VeridianMarketingEmailProviderIDs
	explicitPool := len(ids) > 0
	if len(ids) == 0 && strings.TrimSpace(w.Settings.MarketingEmailProviderID) != "" {
		ids = []string{w.Settings.MarketingEmailProviderID}
	}
	profiles := make([]VeridianEmailProfile, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		integration := w.GetIntegrationByID(id)
		if integration == nil || integration.Type != IntegrationTypeEmail || integration.EmailProvider.Kind == "" ||
			(explicitPool && integration.EmailProvider.VeridianTransportVerifiedAt == nil && id != w.Settings.MarketingEmailProviderID) {
			continue
		}
		seen[id] = struct{}{}
		profiles = append(profiles, VeridianEmailProfile{IntegrationID: id, Provider: &integration.EmailProvider})
	}
	return profiles
}

// ValidateVeridianMarketingEmailProfiles rejects dangling and duplicate IDs at
// the API/model boundary instead of silently degrading to a different sender.
func (w *Workspace) ValidateVeridianMarketingEmailProfiles() error {
	if w == nil || len(w.Settings.VeridianMarketingEmailProviderIDs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(w.Settings.VeridianMarketingEmailProviderIDs))
	for _, rawID := range w.Settings.VeridianMarketingEmailProviderIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return fmt.Errorf("profile integration ID is required")
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate profile integration ID: %s", id)
		}
		integration := w.GetIntegrationByID(id)
		if integration == nil || integration.Type != IntegrationTypeEmail || integration.EmailProvider.Kind == "" {
			return fmt.Errorf("email profile integration not found: %s", id)
		}
		// Grandfather exactly the already-active singleton when migrating to a
		// pool. Every additional/new profile still needs a successful test.
		if integration.EmailProvider.VeridianTransportVerifiedAt == nil && id != w.Settings.MarketingEmailProviderID {
			return fmt.Errorf("email profile transport is not verified: %s", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// --- Lot 2 « vérité d'un profil » (08/10/2026) : usage exclusif, lien IMAP ---

// Usages d'un profil d'envoi. Exclusifs : un profil sert au commercial
// (rotation, portes de réputation, plafonds) OU au transactionnel (envoi
// direct, aucune porte commerciale), jamais aux deux.
const (
	VeridianProfileUsageCommercial    = "commercial"
	VeridianProfileUsageTransactional = "transactional"
	VeridianProfileUsageUnassigned    = "unassigned"
)

// VeridianProfileUsageOf donne l'usage déclaré d'une intégration email. Le pool
// commercial explicite et le singleton marketing historique comptent comme
// commercial ; transactional_email_provider_id comme transactionnel. Si les deux
// règles se recoupent (violation de l'exclusivité), le transactionnel est
// signalé via VeridianUsageConflicts, pas ici : ici le commercial prime, car
// c'est lui qui engage la réputation.
func (w *Workspace) VeridianProfileUsageOf(integrationID string) string {
	if w == nil || integrationID == "" {
		return VeridianProfileUsageUnassigned
	}
	for _, id := range w.Settings.VeridianMarketingEmailProviderIDs {
		if strings.TrimSpace(id) == integrationID {
			return VeridianProfileUsageCommercial
		}
	}
	if len(w.Settings.VeridianMarketingEmailProviderIDs) == 0 && w.Settings.MarketingEmailProviderID == integrationID &&
		w.Settings.TransactionalEmailProviderID != integrationID {
		return VeridianProfileUsageCommercial
	}
	if w.Settings.TransactionalEmailProviderID == integrationID {
		return VeridianProfileUsageTransactional
	}
	return VeridianProfileUsageUnassigned
}

// VeridianUsageConflicts liste les profils déclarés à la fois dans le pool
// commercial explicite (veridian_marketing_email_provider_ids) et comme profil
// transactionnel. Lecture seule : sert la validation et l'inventaire des
// workspaces qui violent déjà la règle.
func (w *Workspace) VeridianUsageConflicts() []string {
	if w == nil || w.Settings.TransactionalEmailProviderID == "" {
		return nil
	}
	for _, id := range w.Settings.VeridianMarketingEmailProviderIDs {
		if strings.TrimSpace(id) == w.Settings.TransactionalEmailProviderID {
			return []string{w.Settings.TransactionalEmailProviderID}
		}
	}
	return nil
}

// ValidateVeridianUsageExclusivity refuse un profil à la fois dans le pool
// commercial et profil transactionnel (erreur 400 côté API). Appelée seulement
// quand une écriture touche l'un des deux réglages : un workspace qui viole déjà
// la règle n'est pas bloqué pour ses autres modifications.
func (w *Workspace) ValidateVeridianUsageExclusivity() error {
	if conflicts := w.VeridianUsageConflicts(); len(conflicts) > 0 {
		return NewValidationError(fmt.Sprintf(
			"profile %s cannot be both in the commercial rotation pool and the transactional profile: a profile serves one usage only",
			conflicts[0]))
	}
	return nil
}

// ValidateVeridianReturnIMAPLink vérifie qu'un lien profil vers IMAP désigne une
// intégration de type imap du même workspace. Chaîne vide = pas de lien.
func (w *Workspace) ValidateVeridianReturnIMAPLink(provider *EmailProvider) error {
	if provider == nil {
		return nil
	}
	id := strings.TrimSpace(provider.VeridianReturnIMAPIntegrationID)
	if id == "" {
		return nil
	}
	target := w.GetIntegrationByID(id)
	if target == nil || target.Type != IntegrationTypeIMAP {
		return NewValidationError(fmt.Sprintf("return inbox must be an imap integration of this workspace: %s", id))
	}
	return nil
}

// VeridianClearReturnIMAPLinks retire de chaque profil le lien vers une boîte IMAP
// supprimée. Évite un lien pendant après deleteIntegration.
func (w *Workspace) VeridianClearReturnIMAPLinks(imapIntegrationID string) {
	if w == nil || imapIntegrationID == "" {
		return
	}
	for i := range w.Integrations {
		if w.Integrations[i].EmailProvider.VeridianReturnIMAPIntegrationID == imapIntegrationID {
			w.Integrations[i].EmailProvider.VeridianReturnIMAPIntegrationID = ""
		}
	}
}
