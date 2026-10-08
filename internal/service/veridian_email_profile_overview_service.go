package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Veridian fork — lot 2 « vérité d'un profil » (08/10/2026).
//
// emailProfiles.overview : tous les profils email du workspace (commerciaux ET
// transactionnels) avec, pour chacun, EffectivePlan (queue.VeridianEffectivePlan,
// la fonction que partage le worker), le type, le statut vérifié, les
// expéditeurs, l'usage, la boîte IMAP liée. Même contrat d'accès que
// emailProfiles.usage : lecture de l'historique des messages, ce qui inclut une
// clé API scopée au workspace en lecture.

type veridianEmailProfileOverviewService struct {
	repo               domain.VeridianEmailProfileOverviewRepository
	messageHistoryRepo domain.MessageHistoryRepository
	workspaceRepo      domain.WorkspaceRepository
	authService        domain.AuthService
	logger             logger.Logger
}

func NewVeridianEmailProfileOverviewService(
	repo domain.VeridianEmailProfileOverviewRepository,
	messageHistoryRepo domain.MessageHistoryRepository,
	workspaceRepo domain.WorkspaceRepository,
	auth domain.AuthService,
	log logger.Logger,
) domain.VeridianEmailProfileOverviewService {
	return &veridianEmailProfileOverviewService{
		repo: repo, messageHistoryRepo: messageHistoryRepo, workspaceRepo: workspaceRepo,
		authService: auth, logger: log,
	}
}

func (s *veridianEmailProfileOverviewService) GetEmailProfilesOverview(ctx context.Context, workspaceID string) (*domain.VeridianEmailProfilesOverview, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}
	ctx, _, membership, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate user: %w", err)
	}
	if !membership.HasPermission(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead) {
		return nil, domain.NewPermissionError(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead, "Insufficient permissions: read access to message history required")
	}
	workspace, err := s.workspaceRepo.GetByID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}

	now := time.Now().UTC()

	// Lot 4 : chaque profil compte « aujourd'hui » dans le fuseau de sa fenetre
	// d'envoi. On lit une fois par jour de compte distinct (un seul le plus souvent).
	type observation struct {
		rows     []domain.VeridianPlanObservationRow
		counters []domain.VeridianPlanCounterRow
	}
	observed := map[string]observation{}
	for i := range workspace.Integrations {
		integration := &workspace.Integrations[i]
		if integration.Type != domain.IntegrationTypeEmail || integration.EmailProvider.Kind == "" {
			continue
		}
		day := domain.VeridianDayFor(workspace, &integration.EmailProvider, now)
		key := veridianOverviewDayKey(day)
		if _, done := observed[key]; done {
			continue
		}
		rows, counters, err := s.repo.GetPlanObservations(ctx, workspaceID, day)
		if err != nil {
			s.logger.WithField("error", err.Error()).Error("Failed to read email profile observations")
			return nil, fmt.Errorf("failed to read email profile observations: %w", err)
		}
		observed[key] = observation{rows: rows, counters: counters}
	}

	// Réputation PAR PROFIL des profils commerciaux : même calcul que le fusible du
	// worker (queue.VeridianComputeReputationStatus, contexte du profil, messages
	// transactionnels écartés). Une erreur de lecture fait échouer l'appel : un plan
	// « sain » faute de mesure serait faux.
	reputation := map[string]queue.VeridianReputationStatus{}
	for i := range workspace.Integrations {
		integration := &workspace.Integrations[i]
		if integration.Type != domain.IntegrationTypeEmail || integration.EmailProvider.Kind == "" {
			continue
		}
		if workspace.VeridianProfileUsageOf(integration.ID) != domain.VeridianProfileUsageCommercial {
			continue
		}
		sender := integration.EmailProvider.GetSender("")
		if sender == nil {
			continue
		}
		senderDomain := queue.VeridianEmailDomain(sender.Email)
		if senderDomain == "" {
			continue
		}
		scoped := domain.WithVeridianReputationProfile(ctx, integration.ID)
		status, err := queue.VeridianComputeReputationStatus(scoped, s.messageHistoryRepo, workspace.ID, senderDomain, &integration.EmailProvider, now)
		if err != nil {
			s.logger.WithFields(map[string]interface{}{"workspace_id": workspaceID, "sender_domain": senderDomain, "error": err.Error()}).Error("Failed to compute reputation for overview")
			return nil, fmt.Errorf("failed to compute reputation status: %w", err)
		}
		reputation[integration.ID] = status
	}

	overview := buildVeridianEmailProfilesOverview(workspace, func(day domain.VeridianDay) ([]domain.VeridianPlanObservationRow, []domain.VeridianPlanCounterRow) {
		o := observed[veridianOverviewDayKey(day)]
		return o.rows, o.counters
	}, reputation, now)
	overview.TransactionalWatch = s.transactionalWatch(ctx, workspace, overview, now)
	return overview, nil
}

// transactionalWatch mesure le volume et la réputation du profil transactionnel
// (lot 5). Mesurée et jamais bloquante : une lecture qui échoue donne le niveau
// « unknown » (jamais « ok »), sans faire échouer l'overview ni toucher à l'envoi.
func (s *veridianEmailProfileOverviewService) transactionalWatch(ctx context.Context, workspace *domain.Workspace, overview *domain.VeridianEmailProfilesOverview, now time.Time) *domain.VeridianTransactionalWatch {
	for i := range overview.Profiles {
		profile := overview.Profiles[i]
		if profile.Usage != domain.VeridianProfileUsageTransactional {
			continue
		}
		var provider *domain.EmailProvider
		for j := range workspace.Integrations {
			if workspace.Integrations[j].ID == profile.IntegrationID {
				provider = &workspace.Integrations[j].EmailProvider
			}
		}
		day := domain.VeridianDayFor(workspace, provider, now)
		loc := domain.VeridianDayLocation(workspace, provider)
		previousStart := day.Start.In(loc).AddDate(0, 0, -7).UTC()
		input, err := s.repo.GetTransactionalWatchInput(ctx, workspace.ID, profile.IntegrationID, day, previousStart, now.AddDate(0, 0, -7))
		if err != nil {
			s.logger.WithField("error", err.Error()).Error("Failed to measure transactional profile watch")
			w := domain.VeridianUnknownTransactionalWatch(profile.IntegrationID, profile.Name, "measure failed")
			return &w
		}
		w := domain.VeridianEvaluateTransactionalWatch(profile.IntegrationID, profile.Name, input)
		return &w
	}
	return nil
}

// veridianOverviewDayKey identifie un jour de compte (date civile + début exact).
func veridianOverviewDayKey(day domain.VeridianDay) string {
	return day.LabelDate() + "|" + day.Start.Format(time.RFC3339)
}

// buildVeridianEmailProfilesOverview assemble la réponse. Fonction pure (aucun
// I/O) : testée sans base.
func buildVeridianEmailProfilesOverview(
	workspace *domain.Workspace,
	observe func(day domain.VeridianDay) ([]domain.VeridianPlanObservationRow, []domain.VeridianPlanCounterRow),
	reputation map[string]queue.VeridianReputationStatus,
	now time.Time,
) *domain.VeridianEmailProfilesOverview {
	out := &domain.VeridianEmailProfilesOverview{
		// Lot 4 : la date du workspace est son jour de compte par défaut (fuseau de la
		// fenêtre d'envoi du workspace, sinon son fuseau) ; chaque profil porte la sienne.
		Date:           domain.VeridianDayFor(workspace, nil, now).LabelDate(),
		GeneratedAt:    now.UTC(),
		Timezone:       workspace.Settings.Timezone,
		Profiles:       []domain.VeridianEmailProfileOverview{},
		GlobalInboxes:  []domain.VeridianOverviewInbox{},
		UsageConflicts: workspace.VeridianUsageConflicts(),
	}
	if out.UsageConflicts == nil {
		out.UsageConflicts = []string{}
	}

	// Boîtes IMAP et liens profil -> IMAP.
	inboxes := map[string]*domain.VeridianOverviewInbox{}
	inboxOrder := []string{}
	for i := range workspace.Integrations {
		integration := &workspace.Integrations[i]
		if integration.Type != domain.IntegrationTypeIMAP || integration.IMAPSettings == nil {
			continue
		}
		inboxes[integration.ID] = &domain.VeridianOverviewInbox{
			IntegrationID: integration.ID, Name: integration.Name,
			Host: integration.IMAPSettings.Host, Address: integration.IMAPSettings.Username,
			Folder: integration.IMAPSettings.Folder, LinkedProfiles: []string{},
		}
		inboxOrder = append(inboxOrder, integration.ID)
	}
	for i := range workspace.Integrations {
		integration := &workspace.Integrations[i]
		if integration.Type != domain.IntegrationTypeEmail {
			continue
		}
		if inbox, ok := inboxes[integration.EmailProvider.VeridianReturnIMAPIntegrationID]; ok {
			inbox.LinkedProfiles = append(inbox.LinkedProfiles, integration.ID)
		}
	}

	var capacity int
	capacityKnown := true
	for i := range workspace.Integrations {
		integration := &workspace.Integrations[i]
		if integration.Type != domain.IntegrationTypeEmail || integration.EmailProvider.Kind == "" {
			continue
		}
		provider := &integration.EmailProvider
		rows, counters := observe(domain.VeridianDayFor(workspace, provider, now))
		obs := domain.VeridianBuildPlanObserved(integration.ID, rows, counters)
		plan := queue.VeridianEffectivePlan(queue.VeridianPlanInput{
			Workspace: workspace, IntegrationID: integration.ID, Now: now,
			Observed: obs, Reputation: reputation,
		})
		profile := domain.VeridianEmailProfileOverview{
			IntegrationID:         integration.ID,
			Name:                  integration.Name,
			Kind:                  string(provider.Kind),
			Type:                  provider.VeridianProfileType(),
			Usage:                 plan.Mode,
			InRotation:            veridianProfileInRotation(workspace, integration.ID),
			Paused:                provider.VeridianPaused,
			Verified:              provider.VeridianTransportVerifiedAt != nil,
			VerifiedAt:            provider.VeridianTransportVerifiedAt,
			CredentialsConfigured: veridianCredentialsConfigured(provider),
			Senders:               []domain.VeridianOverviewSender{},
			Plan:                  plan,
		}
		for _, sender := range provider.Senders {
			profile.Senders = append(profile.Senders, domain.VeridianOverviewSender{Email: sender.Email, Name: sender.Name, IsDefault: sender.IsDefault})
		}
		if inbox, ok := inboxes[provider.VeridianReturnIMAPIntegrationID]; ok {
			copied := *inbox
			profile.ReturnInbox = &copied
		}
		out.Profiles = append(out.Profiles, profile)

		switch profile.Usage {
		case domain.VeridianProfileUsageCommercial:
			out.Totals.CommercialSentToday += plan.SentToday
			if profile.Paused {
				out.Totals.PausedProfiles++
			} else if profile.InRotation {
				out.Totals.ActiveCommercialProfiles++
				if plan.DailyCapToday == nil {
					capacityKnown = false
				} else {
					capacity += *plan.DailyCapToday
				}
			}
		case domain.VeridianProfileUsageTransactional:
			out.Totals.TransactionalSentToday += plan.SentToday
			if profile.Paused {
				out.Totals.PausedProfiles++
			}
		}
	}
	if capacityKnown && out.Totals.ActiveCommercialProfiles > 0 {
		out.Totals.CommercialCapacityToday = &capacity
	}
	for _, id := range inboxOrder {
		if len(inboxes[id].LinkedProfiles) == 0 {
			out.GlobalInboxes = append(out.GlobalInboxes, *inboxes[id])
		}
	}
	return out
}

func veridianProfileInRotation(workspace *domain.Workspace, integrationID string) bool {
	for _, p := range workspace.VeridianMarketingEmailProfiles() {
		if p.IntegrationID == integrationID {
			return true
		}
	}
	return false
}

// veridianCredentialsConfigured dit si le profil porte un identifiant exploitable,
// sans jamais exposer de secret (même règle que la redaction des réponses
// workspace : internal/http/veridian_workspace_redaction.go).
func veridianCredentialsConfigured(provider *domain.EmailProvider) bool {
	has := func(values ...string) bool {
		for _, v := range values {
			if strings.TrimSpace(v) != "" {
				return true
			}
		}
		return false
	}
	switch {
	case provider.SMTP != nil:
		smtp := provider.SMTP
		oauthSecret := has(smtp.OAuth2ClientSecret, smtp.EncryptedOAuth2ClientSecret)
		oauthRefresh := has(smtp.OAuth2RefreshToken, smtp.EncryptedOAuth2RefreshToken)
		return has(smtp.Password, smtp.EncryptedPassword) || (oauthSecret && (smtp.OAuth2Provider != "google" || oauthRefresh))
	case provider.SES != nil:
		return has(provider.SES.SecretKey, provider.SES.EncryptedSecretKey)
	case provider.SparkPost != nil:
		return has(provider.SparkPost.APIKey, provider.SparkPost.EncryptedAPIKey)
	case provider.Postmark != nil:
		return has(provider.Postmark.ServerToken, provider.Postmark.EncryptedServerToken)
	case provider.Mailgun != nil:
		return has(provider.Mailgun.APIKey, provider.Mailgun.EncryptedAPIKey)
	case provider.Mailjet != nil:
		return has(provider.Mailjet.APIKey, provider.Mailjet.EncryptedAPIKey, provider.Mailjet.SecretKey, provider.Mailjet.EncryptedSecretKey)
	case provider.SendGrid != nil:
		return has(provider.SendGrid.APIKey, provider.SendGrid.EncryptedAPIKey)
	}
	return false
}
