package broadcast

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
	"github.com/Notifuse/notifuse/pkg/veridian_spintax"
	"github.com/google/uuid"
)

// queueMessageSender implements the MessageSender interface by enqueueing to the email queue
// instead of sending directly. This allows rate limiting to be handled by the queue workers
// and provides a unified queue for both broadcasts and automations.
type queueMessageSender struct {
	queueRepo          domain.EmailQueueRepository
	broadcastRepo      domain.BroadcastRepository
	messageHistoryRepo domain.MessageHistoryRepository
	templateRepo       domain.TemplateRepository
	dataFeedFetcher    DataFeedFetcher
	logger             logger.Logger
	config             *Config
	apiEndpoint        string

	// Veridian fork — repo optionnel pour le fallback workspace du pixel
	// d'ouverture par classe (cf. veridian_pixel_resolver.go). Injecté par la
	// factory via SetVeridianWorkspaceRepo. nil = fallback workspace inactif
	// (comportement upstream : le pixel suit broadcast metadata + défaut tunnel).
	veridianWorkspaceRepo domain.WorkspaceRepository

	// Veridian fork — rotator multi-SMTP partagé (round-robin sender ⇄ classe
	// destinataire, cold outbound). Injecté par la factory pour conserver l'état
	// des curseurs entre les batchs/recipients d'une même session. nil = rotation
	// désactivée (sender figé par GetSender, comportement upstream).
	// Cf. domain/veridian_sender_rotation.go + veridian_sender_rotation.go.
	veridianSenderRotator *domain.VeridianSenderRotator

	// Veridian fork — dédupliqueur anti-hash à l'enqueue (cold outbound). Injecté
	// par la factory (construit avec messageHistoryRepo). nil = anti-hash inactif
	// (comportement upstream : aucune détection de collision, aucun hash posé).
	// Cf. veridian_content_dedup.go.
	veridianContentDedup *veridianContentDedup
}

// SetVeridianWorkspaceRepo injecte le workspace repo utilisé pour le fallback
// pixel par classe au niveau workspace. DI optionnelle (post-construction) pour
// ne pas changer la signature du constructeur ni casser les tests existants.
func (s *queueMessageSender) SetVeridianWorkspaceRepo(repo domain.WorkspaceRepository) {
	s.veridianWorkspaceRepo = repo
}

// SetVeridianSenderRotator injecte le rotator multi-SMTP partagé (DI optionnelle).
// nil = rotation désactivée.
func (s *queueMessageSender) SetVeridianSenderRotator(r *domain.VeridianSenderRotator) {
	s.veridianSenderRotator = r
}

// SetVeridianContentDedup injecte le dédupliqueur anti-hash (DI optionnelle).
// nil = anti-hash inactif (comportement upstream).
func (s *queueMessageSender) SetVeridianContentDedup(d *veridianContentDedup) {
	s.veridianContentDedup = d
}

// NewQueueMessageSender creates a new message sender that enqueues to the email queue
func NewQueueMessageSender(
	queueRepo domain.EmailQueueRepository,
	broadcastRepo domain.BroadcastRepository,
	messageHistoryRepo domain.MessageHistoryRepository,
	templateRepo domain.TemplateRepository,
	dataFeedFetcher DataFeedFetcher,
	logger logger.Logger,
	config *Config,
	apiEndpoint string,
) MessageSender {
	if config == nil {
		config = DefaultConfig()
	}

	return &queueMessageSender{
		queueRepo:          queueRepo,
		broadcastRepo:      broadcastRepo,
		messageHistoryRepo: messageHistoryRepo,
		templateRepo:       templateRepo,
		dataFeedFetcher:    dataFeedFetcher,
		logger:             logger,
		config:             config,
		apiEndpoint:        apiEndpoint,
	}
}

// SendToRecipient enqueues a message for a single recipient
func (s *queueMessageSender) SendToRecipient(
	ctx context.Context,
	workspaceID string,
	integrationID string,
	endpoint string,
	trackingEnabled bool,
	broadcast *domain.Broadcast,
	messageID string,
	email string,
	template *domain.Template,
	data map[string]interface{},
	emailProvider *domain.EmailProvider,
	timeoutAt time.Time,
	contactLanguage string,
	workspaceDefaultLanguage string,
) error {
	// Build the email payload (contact nil ici : envoi single sans contact
	// chargé ; le pixel résout par classification de l'email si tunnel actif).
	// Veridian fork — resolver pixel mémoïsé (fallback workspace, nil-safe).
	pixelResolver := newVeridianWorkspacePixelResolver(s.veridianWorkspaceRepo, s.logger)
	entry, err := s.buildQueueEntry(ctx, workspaceID, integrationID, endpoint, trackingEnabled, broadcast, messageID, email, template, data, emailProvider, contactLanguage, workspaceDefaultLanguage, nil, pixelResolver)
	if err != nil {
		return err
	}

	// Veridian fork: attach provider-class throttle config (no contact here,
	// the worker classifies from the recipient email). No-op without config.
	domain.VeridianApplyProviderThrottle(entry, broadcast, nil)

	// Enqueue the email
	if err := s.queueRepo.Enqueue(ctx, workspaceID, []*domain.EmailQueueEntry{entry}); err != nil {
		s.logger.WithFields(map[string]interface{}{
			"broadcast_id": broadcast.ID,
			"workspace_id": workspaceID,
			"recipient":    email,
			"error":        err.Error(),
		}).Error("Failed to enqueue email")
		return NewBroadcastError(ErrCodeSendFailed, "failed to enqueue email", true, err)
	}

	return nil
}

// SendBatch enqueues messages for a batch of recipients
func (s *queueMessageSender) SendBatch(
	ctx context.Context,
	workspaceID string,
	integrationID string,
	workspaceSecretKey string,
	endpoint string,
	websiteURL string,
	trackingEnabled bool,
	broadcastID string,
	recipients []*domain.ContactWithList,
	templates map[string]*domain.Template,
	emailProvider *domain.EmailProvider,
	timeoutAt time.Time,
	workspaceDefaultLanguage string,
) (sent int, failed int, err error) {
	if len(recipients) == 0 {
		return 0, 0, nil
	}

	// Get broadcast for context
	broadcast, err := s.broadcastRepo.GetBroadcast(ctx, workspaceID, broadcastID)
	if err != nil {
		return 0, len(recipients), fmt.Errorf("failed to get broadcast: %w", err)
	}

	// Veridian fork — resolver pixel par classe avec fallback workspace, mémoïsé
	// pour ce batch (un seul GetByID workspace, zéro I/O par recipient). nil-safe
	// si le repo n'est pas injecté.
	pixelResolver := newVeridianWorkspacePixelResolver(s.veridianWorkspaceRepo, s.logger)

	// Build queue entries
	var entries []*domain.EmailQueueEntry
	var buildErrors int

	for _, recipient := range recipients {
		// Check timeout
		if time.Now().After(timeoutAt) {
			s.logger.WithFields(map[string]interface{}{
				"broadcast_id": broadcastID,
				"workspace_id": workspaceID,
			}).Debug("Timeout reached during batch build")
			break
		}

		// Select template (for A/B testing, use first template or random selection)
		template := s.selectTemplate(templates, broadcast)
		if template == nil {
			buildErrors++
			continue
		}

		// Generate message ID
		messageID := fmt.Sprintf("%s_%s", workspaceID, uuid.New().String())

		// Ensure UTM parameters object is present
		if broadcast.UTMParameters == nil {
			broadcast.UTMParameters = &domain.UTMParameters{}
		}

		if broadcast.UTMParameters.Content == "" {
			broadcast.UTMParameters.Content = template.ID
		}

		// Build tracking settings for BuildTemplateData
		trackingSettings := notifuse_mjml.TrackingSettings{
			Endpoint:       endpoint,
			EnableTracking: trackingEnabled,
			UTMSource:      broadcast.UTMParameters.Source,
			UTMMedium:      broadcast.UTMParameters.Medium,
			UTMCampaign:    broadcast.UTMParameters.Campaign,
			UTMContent:     broadcast.UTMParameters.Content,
			UTMTerm:        broadcast.UTMParameters.Term,
			WorkspaceID:    workspaceID,
			MessageID:      messageID,
		}

		// Build template data with all system variables (unsubscribe_url, notification_center_url, etc.)
		req := domain.TemplateDataRequest{
			WorkspaceID:         workspaceID,
			WorkspaceSecretKey:  workspaceSecretKey,
			WorkspaceWebsiteURL: websiteURL,
			ContactWithList:     *recipient,
			MessageID:           messageID,
			TrackingSettings:    trackingSettings,
			Broadcast:           broadcast,
		}
		data, err := domain.BuildTemplateData(req)
		if err != nil {
			s.logger.WithFields(map[string]interface{}{
				"broadcast_id": broadcastID,
				"workspace_id": workspaceID,
				"recipient":    recipient.Contact.Email,
				"error":        err.Error(),
			}).Warn("Failed to build template data")
			buildErrors++
			continue
		}

		// Fetch recipient feed if configured and enabled
		if broadcast.DataFeed != nil && broadcast.DataFeed.RecipientFeed != nil &&
			broadcast.DataFeed.RecipientFeed.Enabled && s.dataFeedFetcher != nil {

			payload := &domain.RecipientFeedRequestPayload{
				Contact:   domain.BuildRecipientFeedContact(recipient.Contact),
				List:      domain.RecipientFeedList{ID: recipient.ListID, Name: recipient.ListName},
				Broadcast: domain.RecipientFeedBroadcast{ID: broadcast.ID, Name: broadcast.Name},
				Workspace: domain.RecipientFeedWorkspace{ID: workspaceID},
			}

			feedData, feedErr := s.dataFeedFetcher.FetchRecipient(ctx, broadcast.DataFeed.RecipientFeed, payload)
			if feedErr != nil {
				s.logger.WithFields(map[string]interface{}{
					"broadcast_id": broadcastID,
					"workspace_id": workspaceID,
					"recipient":    recipient.Contact.Email,
					"error":        feedErr.Error(),
				}).Error("Recipient feed fetch failed, pausing broadcast")
				// Return 0,0 — no entries were enqueued (batch Enqueue happens after loop)
				// The broadcast will be paused and the entire batch re-processed on resume
				return 0, 0, fmt.Errorf("%w: recipient feed failed for %s: %v",
					ErrBroadcastShouldPause, recipient.Contact.Email, feedErr)
			}

			data["recipient_feed"] = feedData
		}

		// Extract contact language for variant resolution
		contactLanguage := ""
		if recipient.Contact.Language != nil && !recipient.Contact.Language.IsNull {
			contactLanguage = recipient.Contact.Language.String
		}

		// Build queue entry
		entry, err := s.buildQueueEntry(ctx, workspaceID, integrationID, endpoint, trackingEnabled, broadcast, messageID, recipient.Contact.Email, template, data, emailProvider, contactLanguage, workspaceDefaultLanguage, recipient.Contact, pixelResolver)
		if err != nil {
			s.logger.WithFields(map[string]interface{}{
				"broadcast_id": broadcastID,
				"workspace_id": workspaceID,
				"recipient":    recipient.Contact.Email,
				"error":        err.Error(),
			}).Warn("Failed to build queue entry")
			buildErrors++
			continue
		}

		// Veridian fork: attach provider-class throttle config + contact tag
		// (provider_class posé par l'export batch). No-op without config.
		domain.VeridianApplyProviderThrottle(entry, broadcast, recipient.Contact)

		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		return 0, buildErrors, nil
	}

	// Enqueue all entries in batch
	if err := s.queueRepo.Enqueue(ctx, workspaceID, entries); err != nil {
		s.logger.WithFields(map[string]interface{}{
			"broadcast_id": broadcastID,
			"workspace_id": workspaceID,
			"batch_size":   len(entries),
			"error":        err.Error(),
		}).Error("Failed to enqueue batch")
		return 0, len(recipients), NewBroadcastError(ErrCodeSendFailed, "failed to enqueue batch", true, err)
	}

	s.logger.WithFields(map[string]interface{}{
		"broadcast_id": broadcastID,
		"workspace_id": workspaceID,
		"enqueued":     len(entries),
		"build_errors": buildErrors,
	}).Debug("Batch enqueued successfully")

	// Return enqueued as "sent" since from the orchestrator's perspective, the job is done
	return len(entries), buildErrors, nil
}

// buildQueueEntry creates an EmailQueueEntry for a recipient
func (s *queueMessageSender) buildQueueEntry(
	ctx context.Context,
	workspaceID string,
	integrationID string,
	endpoint string,
	trackingEnabled bool,
	broadcast *domain.Broadcast,
	messageID string,
	email string,
	template *domain.Template,
	data map[string]interface{},
	emailProvider *domain.EmailProvider,
	contactLanguage string,
	workspaceDefaultLanguage string,
	// Veridian fork — contact destinataire (nil pour SendToRecipient single)
	// pour résoudre le pixel d'ouverture par classe de provider.
	contact *domain.Contact,
	// Veridian fork — resolver pixel par classe avec fallback workspace (mémoïsé
	// par batch). Jamais nil (créé par l'appelant).
	pixelResolver *veridianWorkspacePixelResolver,
) (*domain.EmailQueueEntry, error) {
	// Ensure UTM parameters object is present
	if broadcast.UTMParameters == nil {
		broadcast.UTMParameters = &domain.UTMParameters{}
	}

	if broadcast.UTMParameters.Content == "" {
		broadcast.UTMParameters.Content = template.ID
	}

	// Build tracking settings.
	// Veridian fork — custom tracking domain par infra d'envoi (Lot 5 cold) :
	// l'endpoint des liens /t/ et /r/ est aligné au domaine d'envoi de cette infra
	// (EmailProvider.VeridianTrackingDomain) quand configuré, sinon fallback strict
	// sur `endpoint` (déjà résolu workspace CustomEndpointURL > API endpoint global).
	// Cf. domain.VeridianResolveTrackingEndpoint — best-effort, non-régression.
	trackingSettings := notifuse_mjml.TrackingSettings{
		Endpoint:       domain.VeridianResolveTrackingEndpoint(emailProvider, endpoint),
		EnableTracking: trackingEnabled,
		UTMSource:      broadcast.UTMParameters.Source,
		UTMMedium:      broadcast.UTMParameters.Medium,
		UTMCampaign:    broadcast.UTMParameters.Campaign,
		UTMContent:     broadcast.UTMParameters.Content,
		UTMTerm:        broadcast.UTMParameters.Term,
		WorkspaceID:    workspaceID,
		MessageID:      messageID,
	}

	// Veridian fork — découple le pixel d'ouverture (email.opened) de la
	// réécriture de liens, par classe de provider destinataire. Le contexte
	// tunnel est porté par le broadcast metadata (rates/pixel), le tag contact
	// custom_string_5, la config pixel de l'INFRA (EmailProvider), OU les settings
	// workspace (fallback résolu par le pixelResolver, mémoïsé par batch). Cascade
	// broadcast > infra > workspace > défaut. Hors tunnel → nil → comportement
	// upstream (pixel suit EnableTracking).
	trackingSettings.EnableOpenPixel = pixelResolver.resolveOpenPixel(ctx, workspaceID, contact, email, broadcast, emailProvider)

	// Resolve language variant
	emailContent := template.ResolveEmailContent(contactLanguage, workspaceDefaultLanguage)
	if emailContent == nil {
		return nil, fmt.Errorf("email content not available after language resolution")
	}

	// Get sender. Veridian fork — round-robin multi-SMTP par classe destinataire
	// en contexte cold (sinon GetSender upstream figé). Le SenderID explicite du
	// template garde la priorité. Cf. veridian_sender_rotation.go.
	sender := veridianResolveSender(ctx, s.veridianSenderRotator, pixelResolver,
		workspaceID, integrationID, emailProvider, emailContent.SenderID, contact, email, broadcast)
	if sender == nil {
		return nil, fmt.Errorf("no sender configured for email provider")
	}

	// Compile template with the provided data.
	// Veridian fork (cold outbound) — graine spintax = email du destinataire :
	// chaque contact reçoit une variante déterministe du corps, cassant
	// l'empreinte de contenu commune. Sans groupe spintax dans le template = no-op.
	compileReq := notifuse_mjml.CompileTemplateRequest{
		WorkspaceID:         workspaceID,
		MessageID:           messageID,
		VisualEditorTree:    emailContent.VisualEditorTree,
		TemplateData:        data,
		TrackingSettings:    trackingSettings,
		VeridianSpintaxSeed: email,
	}
	compileReq.MjmlSource = emailContent.GetCodeModeMjmlSource()
	compiledTemplate, err := notifuse_mjml.CompileTemplate(compileReq)
	if err != nil {
		return nil, fmt.Errorf("failed to compile template: %w", err)
	}
	if !compiledTemplate.Success || compiledTemplate.HTML == nil {
		errMsg := "template compilation failed"
		if compiledTemplate.Error != nil {
			errMsg = compiledTemplate.Error.Message
		}
		return nil, fmt.Errorf("%s", errMsg)
	}
	htmlContent := *compiledTemplate.HTML

	// Process subject line through Liquid templating
	subject, err := notifuse_mjml.ProcessLiquidTemplate(
		emailContent.Subject,
		data,
		"email_subject",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to process subject: %w", err)
	}
	// Veridian fork (cold outbound) — spintax du sujet (même graine que le corps).
	// Le sujet est rendu hors CompileTemplate, donc résolu explicitement ici.
	// subjectLiquid = sujet post-Liquid AVANT spintax, conservé pour la re-spin
	// du dédupliqueur anti-hash (il doit pouvoir ré-appliquer spintax avec un seed
	// perturbé sur la base post-Liquid, pas sur le sujet déjà spintaxé).
	subjectLiquid := subject
	subject = veridian_spintax.ResolveSpintax(subject, email)

	// Veridian fork (cold outbound) — ANTI-HASH IDENTIQUE par classe de provider
	// destinataire. Détecte si ce rendu final (subject + body) a déjà été envoyé
	// vers la même classe dans la fenêtre glissante ; si oui, RE-SPIN avec un seed
	// perturbé (re-compile body + re-spintaxe subject) jusqu'à obtenir un rendu
	// neuf. Template sans variété → pas de perte de mail (envoi du rendu courant +
	// warning). No-op strict hors contexte cold / anti-hash désactivé / dedup non
	// injecté. Le hash retenu est posé sur le payload (persisté en message_history
	// pour alimenter la fenêtre, relu par le filet worker). Cf.
	// veridian_content_dedup.go + veridian_content_hash.go.
	var contentHash string
	if s.veridianContentDedup != nil {
		respin := func(seed string) (string, string) {
			rs := veridian_spintax.ResolveSpintax(subjectLiquid, seed)
			rb := htmlContent
			respinReq := compileReq
			respinReq.VeridianSpintaxSeed = seed
			if rc, rerr := notifuse_mjml.CompileTemplate(respinReq); rerr == nil && rc.Success && rc.HTML != nil {
				rb = *rc.HTML
			}
			return rs, rb
		}
		dedupRes := s.veridianContentDedup.Resolve(ctx, veridianContentDedupParams{
			WorkspaceID:    workspaceID,
			Email:          email,
			Contact:        contact,
			Broadcast:      broadcast,
			Provider:       emailProvider,
			Workspace:      pixelResolver.workspace(ctx, workspaceID),
			InitialSubject: subject,
			InitialBody:    htmlContent,
			Respin:         respin,
		})
		subject, htmlContent, contentHash = dedupRes.Subject, dedupRes.Body, dedupRes.ContentHash
	}

	// Build the queue entry
	entry := &domain.EmailQueueEntry{
		ID:            uuid.New().String(),
		Status:        domain.EmailQueueStatusPending,
		Priority:      domain.EmailQueuePriorityMarketing,
		SourceType:    domain.EmailQueueSourceBroadcast,
		SourceID:      broadcast.ID,
		IntegrationID: integrationID,
		ProviderKind:  emailProvider.Kind,
		ContactEmail:  email,
		MessageID:     messageID,
		TemplateID:    template.ID,
		Payload: domain.EmailQueuePayload{
			FromAddress:        sender.Email,
			FromName:           sender.Name,
			Subject:            subject,
			HTMLContent:        htmlContent,
			RateLimitPerMinute: emailProvider.RateLimitPerMinute,
			EmailOptions: domain.EmailOptions{
				ReplyTo: emailContent.ReplyTo,
			},
			TemplateVersion: int(template.Version),
			ListID:          broadcast.Audience.List,
			TemplateData:    data, // Store template data for message history
			// Veridian fork — anti-hash : hash du rendu final retenu (après re-spin
			// éventuelle). Vide hors contexte cold / anti-hash off. Persisté en
			// message_history pour la fenêtre glissante. Cf. veridian_content_dedup.go.
			VeridianContentHash: contentHash,
		},
		MaxAttempts: 3,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	// Extract List-Unsubscribe URL from template data for RFC-8058 compliance (broadcast emails only)
	// Veridian fork (cold outbound) — EN CONTEXTE TUNNEL COLD, on NE propage PAS
	// l'unsubscribe (ni header RFC-8058, ni footer) : le cold B2B se présente comme
	// du 1-to-1 ; un List-Unsubscribe = signal "mailing de masse" qui tue la
	// délivrabilité. Hors tunnel → comportement upstream inchangé (broadcasts
	// marketing gardent leur unsubscribe). Workspace mémoïsé via le pixelResolver
	// (zéro I/O supplémentaire). Cf. domain.VeridianSuppressUnsubscribe.
	suppressUnsubscribe := domain.VeridianSuppressUnsubscribe(contact, broadcast, pixelResolver.workspace(ctx, workspaceID))
	if !suppressUnsubscribe {
		if unsubscribeURL, ok := data["oneclick_unsubscribe_url"].(string); ok && unsubscribeURL != "" {
			entry.Payload.EmailOptions.ListUnsubscribeURL = unsubscribeURL
		}
	}

	return entry, nil
}

// selectTemplate selects a template for sending
// For A/B testing, this uses random selection; for normal sends, uses the first template
func (s *queueMessageSender) selectTemplate(templates map[string]*domain.Template, broadcast *domain.Broadcast) *domain.Template {
	if len(templates) == 0 {
		return nil
	}

	// If only one template, use it
	if len(templates) == 1 {
		for _, t := range templates {
			return t
		}
	}

	// For A/B testing, randomly select a template
	// Get template IDs in a consistent order
	var templateIDs []string
	for id := range templates {
		templateIDs = append(templateIDs, id)
	}

	// Secure random selection
	n, err := crand.Int(crand.Reader, big.NewInt(int64(len(templateIDs))))
	if err != nil {
		// Fallback to first template if random fails
		return templates[templateIDs[0]]
	}

	return templates[templateIDs[n.Int64()]]
}
