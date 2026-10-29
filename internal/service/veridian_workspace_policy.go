package service

import (
	"context"
	"reflect"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Politique Veridian des workspaces (lot B du socle modulaire, 29/10/2026). Ce que workspace_service.go
// (fichier gele) faisait en ligne pour les reglages d'envoi, le cycle de vie des profils email et la
// creation de workspace vit ici ; workspace_service.go n'appelle plus que ces fonctions. Le code est
// celui d'avant l'extraction, dans le meme ordre d'execution.

// veridianGuardWorkspaceCreation : en mode gere par le Hub Veridian (HUB_API_SECRET), la creation de
// workspace est la responsabilite exclusive du Hub via /api/tenants/provision (billing, plans, lifetime
// overrides). Toute creation interactive est refusee, meme par le root, pour qu'un clic ne contourne pas
// le Hub. Les appelants internes (VeridianService.Provision) portent domain.SystemCallKey dans le contexte
// et passent ; les appelants HTTP n'en ont jamais.
func (s *WorkspaceService) veridianGuardWorkspaceCreation(ctx context.Context, user *domain.User) error {
	if s.config.HubAPISecret != "" && ctx.Value(domain.SystemCallKey) == nil {
		s.logger.WithField("user_email", user.Email).Warn("Veridian-managed mode: interactive workspace creation refused — use Hub /api/tenants/provision")
		return &domain.ErrUnauthorized{Message: "workspace creation is managed by Veridian Hub"}
	}
	return nil
}

// veridianSettingsSnapshot : l'etat des reglages AVANT ecriture, pour savoir apres coup ce qui a change.
type veridianSettingsSnapshot struct {
	policyChanged       bool
	poolBefore          []string
	transactionalBefore string
}

// veridianSnapshotSettings : a appeler avant toute modification de existing.
func veridianSnapshotSettings(existing, incoming domain.WorkspaceSettings) veridianSettingsSnapshot {
	return veridianSettingsSnapshot{
		policyChanged:       veridianWorkspaceSendingPolicyChanged(existing, incoming),
		poolBefore:          append([]string(nil), existing.VeridianMarketingEmailProviderIDs...),
		transactionalBefore: existing.TransactionalEmailProviderID,
	}
}

// veridianApplyWorkspaceSettings : le mapping de UpdateWorkspace est une allowlist explicite (les champs non
// recopies sont silencieusement droppes). On propage donc les reglages Veridian (pool commercial, debits par
// classe, pixel par classe, plafonds, fenetre d'envoi, anti-hash, fil, classes exclues), sinon l'UI les sauve
// sans les persister (bug vu en validation staging 2026-06-11). nil = la config est effacee (vider l'UI revient
// au defaut tunnel). Deux champs absents de la console ne sont pas remis a zero par une sauvegarde qui ne les
// porte pas : niveau du journal des decisions (fiche 62) et relances dans le fil (12/10/2026).
func veridianApplyWorkspaceSettings(dst *domain.WorkspaceSettings, settings domain.WorkspaceSettings) {
	dst.VeridianMarketingEmailProviderIDs = settings.VeridianMarketingEmailProviderIDs
	dst.VeridianProviderClassRates = settings.VeridianProviderClassRates
	dst.VeridianOpenPixelByClass = settings.VeridianOpenPixelByClass
	dst.VeridianProviderClassDailyCap = settings.VeridianProviderClassDailyCap
	dst.VeridianPerRecipientDailyCap = settings.VeridianPerRecipientDailyCap
	dst.VeridianPerSenderDailyCap = settings.VeridianPerSenderDailyCap
	dst.VeridianSendingWindow = settings.VeridianSendingWindow
	if settings.VeridianDecisionLogLevel != "" {
		dst.VeridianDecisionLogLevel = domain.VeridianNormalizeDecisionLogLevel(settings.VeridianDecisionLogLevel)
	}
	dst.VeridianJitterPct = settings.VeridianJitterPct
	dst.VeridianAntiHashEnabled = settings.VeridianAntiHashEnabled
	dst.VeridianAntiHashWindowHours = settings.VeridianAntiHashWindowHours
	if settings.VeridianThreadFollowups != nil {
		dst.VeridianThreadFollowups = settings.VeridianThreadFollowups
	}
	dst.VeridianExcludedProviderClasses = settings.VeridianExcludedProviderClasses
}

// veridianValidateUsageExclusivity (lot 2) : exclusivite commercial / transactionnel, verifiee seulement si
// cette ecriture change le pool ou le profil transactionnel : un workspace qui viole deja la regle reste
// modifiable pour tout le reste.
func (s *WorkspaceService) veridianValidateUsageExclusivity(workspaceID string, ws *domain.Workspace, before veridianSettingsSnapshot) error {
	if veridianUsageSettingsChanged(before.poolBefore, before.transactionalBefore, ws.Settings) {
		if err := ws.ValidateVeridianUsageExclusivity(); err != nil {
			s.logger.WithField("workspace_id", workspaceID).WithField("error", err.Error()).Warn("Refused workspace update: profile usage exclusivity")
			return err
		}
	}
	return nil
}

// veridianWakeAfterPolicyChange : apres une ecriture qui change la politique d'envoi, reveille les entrees
// de file en attente de chaque profil email (best-effort, journalise).
func (s *WorkspaceService) veridianWakeAfterPolicyChange(ctx context.Context, workspaceID string, ws *domain.Workspace, before veridianSettingsSnapshot) {
	if before.policyChanged && s.emailIntegrationPolicyRepo != nil {
		for _, integration := range ws.GetIntegrationsByType(domain.IntegrationTypeEmail) {
			if _, err := s.emailIntegrationPolicyRepo.WakePendingByIntegration(ctx, workspaceID, integration.ID); err != nil {
				s.logger.WithField("workspace_id", workspaceID).
					WithField("integration_id", integration.ID).
					WithField("error", err.Error()).
					Warn("Workspace sending policy updated but pending queue wake failed")
			}
		}
	}
}

// veridianUsageSettingsChanged dit si une ecriture de settings touche le pool
// commercial ou le profil transactionnel (declenche la validation d'exclusivite).
func veridianUsageSettingsChanged(poolBefore []string, transactionalBefore string, after domain.WorkspaceSettings) bool {
	return transactionalBefore != after.TransactionalEmailProviderID ||
		!reflect.DeepEqual(poolBefore, after.VeridianMarketingEmailProviderIDs)
}

func veridianWorkspaceSendingPolicyChanged(before, after domain.WorkspaceSettings) bool {
	return !reflect.DeepEqual(before.VeridianProviderClassRates, after.VeridianProviderClassRates) ||
		!reflect.DeepEqual(before.VeridianProviderClassDailyCap, after.VeridianProviderClassDailyCap) ||
		before.VeridianPerRecipientDailyCap != after.VeridianPerRecipientDailyCap ||
		before.VeridianPerSenderDailyCap != after.VeridianPerSenderDailyCap ||
		!reflect.DeepEqual(before.VeridianSendingWindow, after.VeridianSendingWindow) ||
		before.VeridianJitterPct != after.VeridianJitterPct ||
		!reflect.DeepEqual(before.VeridianExcludedProviderClasses, after.VeridianExcludedProviderClasses)
}

// veridianPrepareNewEmailProvider : a la creation d'un profil email, la verification du transport est
// delivree par le serveur seulement, apres un test reussi du profil enregistre.
func veridianPrepareNewEmailProvider(p *domain.EmailProvider) {
	p.VeridianTransportVerifiedAt = nil
	veridianClearEmailProviderResponseFlags(p)
}

// veridianPrepareUpdatedEmailProvider : a la mise a jour d'un profil email, les secrets existants sont
// preserves, les drapeaux de reponse effaces, et la verification du transport n'est conservee que si le
// transport n'a pas change.
func veridianPrepareUpdatedEmailProvider(next, current *domain.EmailProvider) {
	transportChanged := veridianEmailProviderTransportChanged(next, current)
	veridianPreserveEmailProviderSecrets(next, *current)
	veridianClearEmailProviderResponseFlags(next)
	if transportChanged {
		next.VeridianTransportVerifiedAt = nil
	} else {
		next.VeridianTransportVerifiedAt = current.VeridianTransportVerifiedAt
	}
}

// veridianMergeIMAPSettings (Lot 8, IMAP self-service) : preserve le mot de passe chiffre existant si l'admin
// re-sauve sans le retaper (meme pattern que les cles d'API Supabase/LLM/Firecrawl) ; sans reglages fournis,
// conserve les existants.
func veridianMergeIMAPSettings(incoming, existing *domain.IMAPSettings) *domain.IMAPSettings {
	if incoming == nil {
		return existing
	}
	if incoming.Password == "" && incoming.EncryptedPassword == "" && existing != nil {
		incoming.EncryptedPassword = existing.EncryptedPassword
	}
	return incoming
}

// veridianValidateEmailIntegration : un profil email ne peut pas se lier a une boite IMAP de retour invalide.
func veridianValidateEmailIntegration(ws *domain.Workspace, integration *domain.Integration) error {
	if integration.Type == domain.IntegrationTypeEmail {
		return ws.ValidateVeridianReturnIMAPLink(&integration.EmailProvider)
	}
	return nil
}

// veridianForgetIntegration : a la suppression d'une integration, retire ses references dans les reglages
// (pool commercial, profil marketing) et ses liens vers une boite IMAP de retour.
func veridianForgetIntegration(ws *domain.Workspace, integrationID string) {
	veridianRemoveEmailProfileReference(&ws.Settings, integrationID)
	ws.VeridianClearReturnIMAPLinks(integrationID)
}

func veridianRemoveEmailProfileReference(settings *domain.WorkspaceSettings, integrationID string) {
	if settings == nil {
		return
	}
	removedFromPool := false
	remaining := make([]string, 0, len(settings.VeridianMarketingEmailProviderIDs))
	for _, id := range settings.VeridianMarketingEmailProviderIDs {
		if id == integrationID {
			removedFromPool = true
			continue
		}
		remaining = append(remaining, id)
	}
	settings.VeridianMarketingEmailProviderIDs = remaining
	if removedFromPool {
		settings.MarketingEmailProviderID = ""
		if len(remaining) > 0 {
			settings.MarketingEmailProviderID = remaining[0]
		}
	} else if settings.MarketingEmailProviderID == integrationID {
		settings.MarketingEmailProviderID = ""
	}
}
