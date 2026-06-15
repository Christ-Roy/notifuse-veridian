package service

import (
	"context"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/veridian_ndr"
)

// Veridian fork — Service stop-on-reply (Lot 3 sprint cold outbound, 2026-06-15).
//
// Cœur métier du "stop-on-reply" : à partir d'un message entrant capté par le poller
// IMAP (Lot 1), décider si c'est une réponse d'un prospect à un de nos envois, et si
// oui :
//   1. poser le signal durable 'replied' (table veridian_contact_reply, V51) — source
//      de vérité consommée par le gate d'exit de séquence du Lot 9 ;
//   2. créer un événement timeline (audit + visibilité console) ;
//   3. EXIT ACTIF : sortir immédiatement le contact de toutes ses automations actives
//      avec exit_reason='replied' (sans attendre le prochain tick du gate Lot 9).
//
// Ce service expose AUSSI HasReplied → il EST le ColdReplyChecker que le Lot 9 branche
// via AutomationExecutor.SetColdReplyChecker. Les deux mécanismes sont complémentaires :
//   - HasReplied (pull, Lot 9) : filet de sécurité vérifié à CHAQUE tick — sort un
//     contact même si l'exit actif a échoué (best-effort) ou s'il entre dans une
//     nouvelle séquence après avoir répondu.
//   - exit actif (push, ici) : réactivité immédiate à la détection de la réponse.
//
// Tout est BEST-EFFORT et IDEMPOTENT (le poller garantit at-most-once dispatch mais
// peut re-dispatcher si MarkSeen échoue). Une erreur ne fait jamais paniquer le poller
// (le message sera de toute façon marqué vu). Re-traiter la même réponse 2× → 1 seul
// signal (ON CONFLICT DO NOTHING) et au plus 1 exit par automation (un contact déjà
// 'exited' n'est pas ré-exité).

// VeridianReplyService détecte les réponses prospect et exécute le stop-on-reply.
type VeridianReplyService struct {
	replyRepo      domain.VeridianContactReplyRepository
	messageRepo    domain.MessageHistoryRepository
	contactRepo    domain.ContactRepository
	automationRepo domain.AutomationRepository
	timelineRepo   domain.ContactTimelineRepository
	logger         logger.Logger
}

// NewVeridianReplyService construit le service stop-on-reply.
func NewVeridianReplyService(
	replyRepo domain.VeridianContactReplyRepository,
	messageRepo domain.MessageHistoryRepository,
	contactRepo domain.ContactRepository,
	automationRepo domain.AutomationRepository,
	timelineRepo domain.ContactTimelineRepository,
	log logger.Logger,
) *VeridianReplyService {
	return &VeridianReplyService{
		replyRepo:      replyRepo,
		messageRepo:    messageRepo,
		contactRepo:    contactRepo,
		automationRepo: automationRepo,
		timelineRepo:   timelineRepo,
		logger:         log,
	}
}

// HasReplied implémente service.ColdReplyChecker (contrat du Lot 9) : true si le
// contact a déjà été marqué 'replied'. Lecture pure sur la table de signal.
func (s *VeridianReplyService) HasReplied(ctx context.Context, workspaceID, email string) (bool, error) {
	return s.replyRepo.HasReplied(ctx, workspaceID, domain.VeridianNormalizeEmail(email))
}

// DetectReply applique la LOGIQUE de détection (pure I/O de lecture, pas d'effet de
// bord) sur un message entrant : retourne le verdict (réponse ou non, par quel signal,
// pour quel contact). Séparé de l'action pour être testable et réutilisable.
//
// Priorité au MATCH FORT (Message-ID) : on cherche d'abord si un In-Reply-To/References
// cité correspond à un de NOS envois. Si oui, on confirme que c'est bien le contact
// expéditeur (le contact_email de l'envoi == From de la réponse, après normalisation) —
// défense contre un thread où plusieurs adresses se répondent.
//
// FALLBACK FAIBLE (expéditeur) seulement si aucun match fort ET le message n'est pas un
// NDR ET le From est un contact connu du workspace.
func (s *VeridianReplyService) DetectReply(ctx context.Context, msg *domain.VeridianIMAPMessage) (domain.VeridianReplyDetection, error) {
	none := domain.VeridianReplyDetection{IsReply: false, MatchType: domain.VeridianReplyMatchNone}
	if msg == nil {
		return none, nil
	}

	fromEmail := domain.VeridianNormalizeEmail(msg.From)

	// Garde-fou EXPÉDITEUR daemon (avant tout) : un MAILER-DAEMON/postmaster n'est
	// jamais une réponse humaine. Bloqué ici même si le RawBody est absent (fetch
	// tronqué) — cas où le parseur NDR ci-dessous retournerait IsNDR=false et où un
	// match fort par Message-ID classerait à tort le NDR comme réponse.
	if domain.VeridianFromLooksLikeDaemon(msg.From) {
		return none, nil
	}

	// Un NDR n'est JAMAIS une réponse (il est traité par le bounce-loop, Lot 2).
	// On réutilise le MÊME parseur canonique que le Lot 2 (pkg/veridian_ndr) pour
	// qu'un message classé NDR côté bounce-loop le soit identiquement ici — pas de
	// divergence d'heuristique entre les deux consumers du même poller. Le parseur
	// inspecte From + Subject + le corps DSN (Final-Recipient, Diagnostic-Code),
	// donc plus fiable qu'un simple test sur From.
	if veridian_ndr.Parse(msg.RawBody, msg.From, msg.Subject).IsNDR {
		return none, nil
	}

	// --- 1. MATCH FORT : Message-ID cité → notre envoi ---
	localParts := domain.VeridianExtractMessageIDLocalParts(msg.InReplyTo, msg.References)
	for _, id := range localParts {
		contactEmail, found, err := s.messageRepo.FindContactEmailByMessageID(ctx, msg.WorkspaceID, id)
		if err != nil {
			// Best-effort : on log et on tente le candidat suivant / le fallback.
			s.logger.WithFields(map[string]interface{}{
				"workspace_id": msg.WorkspaceID,
				"message_id":   id,
				"error":        err.Error(),
			}).Warn("VeridianReply: lookup message_id failed, continuing")
			continue
		}
		if !found {
			continue
		}
		// L'envoi cité existe. On exige que le From de la réponse corresponde au
		// destinataire de cet envoi : c'est bien CE prospect qui répond à CE mail.
		// Si From est absent/illisible, on fait confiance au Message-ID (signal le
		// plus fort) et on retient le contact de l'envoi.
		matchedContact := domain.VeridianNormalizeEmail(contactEmail)
		if fromEmail != "" && fromEmail != matchedContact {
			// Message-ID à nous mais expéditeur différent (transfert, thread tiers) :
			// pas une réponse de notre prospect. On continue de chercher.
			continue
		}
		return domain.VeridianReplyDetection{
			IsReply:          true,
			MatchType:        domain.VeridianReplyMatchMessageID,
			ContactEmail:     matchedContact,
			MatchedMessageID: id,
		}, nil
	}

	// --- 2. FALLBACK FAIBLE : From = contact connu du workspace ---
	if fromEmail == "" {
		return none, nil
	}
	contact, err := s.contactRepo.GetContactByEmail(ctx, msg.WorkspaceID, fromEmail)
	if err != nil {
		// Erreur de lookup (DB) ou contact inexistant : pas de fallback exploitable.
		// On ne propage pas l'erreur (best-effort) — un échec ne doit pas crasher le
		// consumer ; on log et on considère "pas une réponse".
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": msg.WorkspaceID,
			"from":         fromEmail,
			"error":        err.Error(),
		}).Debug("VeridianReply: fallback contact lookup miss")
		return none, nil
	}
	if contact == nil {
		return none, nil
	}
	return domain.VeridianReplyDetection{
		IsReply:      true,
		MatchType:    domain.VeridianReplyMatchSenderFallback,
		ContactEmail: fromEmail,
	}, nil
}

// ProcessInboundMessage est le point d'entrée appelé par le reply-consumer pour chaque
// message neuf. Best-effort : retourne une erreur (loggée par le consumer) mais le
// message est de toute façon marqué vu par le poller. Idempotent de bout en bout.
func (s *VeridianReplyService) ProcessInboundMessage(ctx context.Context, msg *domain.VeridianIMAPMessage) error {
	detection, err := s.DetectReply(ctx, msg)
	if err != nil {
		return err
	}
	if !detection.IsReply {
		return nil
	}

	// Fast-path idempotent : déjà marqué replied → rien à refaire (évite un exit
	// actif redondant sur un re-dispatch).
	already, err := s.replyRepo.HasReplied(ctx, msg.WorkspaceID, detection.ContactEmail)
	if err != nil {
		// On log mais on continue : MarkReplied est lui-même idempotent (ON CONFLICT),
		// l'exit actif l'est aussi. Mieux vaut re-tenter que rater un stop-on-reply.
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": msg.WorkspaceID,
			"contact":      detection.ContactEmail,
			"error":        err.Error(),
		}).Warn("VeridianReply: HasReplied pre-check failed, proceeding")
	}
	if already {
		return nil
	}

	repliedAt := s.resolveRepliedAt(msg.Date)

	// 1. Signal durable (source de vérité).
	if err := s.replyRepo.MarkReplied(ctx, msg.WorkspaceID, &domain.VeridianContactReply{
		ContactEmail:     detection.ContactEmail,
		RepliedAt:        repliedAt,
		MatchType:        detection.MatchType,
		MatchedMessageID: detection.MatchedMessageID,
	}); err != nil {
		// Échec du signal : on log et on s'arrête là (sans signal, le gate Lot 9 ne
		// verra rien ; l'exit actif sans signal serait incohérent). Le message sera
		// re-dispatché si MarkSeen échoue, sinon perdu — best-effort assumé.
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": msg.WorkspaceID,
			"contact":      detection.ContactEmail,
			"error":        err.Error(),
		}).Warn("VeridianReply: MarkReplied failed")
		return err
	}

	s.logger.WithFields(map[string]interface{}{
		"workspace_id": msg.WorkspaceID,
		"contact":      detection.ContactEmail,
		"match_type":   string(detection.MatchType),
	}).Info("VeridianReply: prospect replied, stopping cadence")

	// 2. Timeline event (best-effort, ne bloque pas l'exit).
	s.createReplyTimelineEvent(ctx, msg.WorkspaceID, detection, repliedAt)

	// 3. Exit actif des automations en cours.
	s.exitActiveAutomations(ctx, msg.WorkspaceID, detection.ContactEmail)

	return nil
}

// resolveRepliedAt retient la Date du mail si elle est plausible, sinon now(). Un
// client mail peut envoyer une Date absente ou aberrante (loin dans le futur, ou avant
// l'epoch) — on borne pour ne pas polluer la timeline.
func (s *VeridianReplyService) resolveRepliedAt(date time.Time) time.Time {
	now := time.Now().UTC()
	if date.IsZero() || date.After(now.Add(24*time.Hour)) || date.Year() < 2000 {
		return now
	}
	return date.UTC()
}

// exitActiveAutomations sort le contact de toutes ses automations actives avec
// exit_reason='replied'. Idempotent : on ne touche que les ContactAutomation en statut
// 'active' ; un re-passage ne trouve plus rien à exiter. Best-effort sur chaque erreur.
func (s *VeridianReplyService) exitActiveAutomations(ctx context.Context, workspaceID, contactEmail string) {
	cas, _, err := s.automationRepo.ListContactAutomations(ctx, workspaceID, domain.ContactAutomationFilter{
		ContactEmail: contactEmail,
		Status:       []domain.ContactAutomationStatus{domain.ContactAutomationStatusActive},
		Limit:        1000,
	})
	if err != nil {
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"contact":      contactEmail,
			"error":        err.Error(),
		}).Warn("VeridianReply: list active automations failed, gate Lot 9 will catch up")
		return
	}

	reason := domain.ExitReasonReplied
	for _, ca := range cas {
		if ca.Status != domain.ContactAutomationStatusActive {
			continue
		}
		ca.Status = domain.ContactAutomationStatusExited
		ca.ExitReason = &reason
		ca.CurrentNodeID = nil
		ca.ScheduledAt = nil

		if err := s.automationRepo.UpdateContactAutomation(ctx, workspaceID, ca); err != nil {
			s.logger.WithFields(map[string]interface{}{
				"workspace_id":  workspaceID,
				"contact":       contactEmail,
				"automation_id": ca.AutomationID,
				"error":         err.Error(),
			}).Warn("VeridianReply: exit automation failed, gate Lot 9 will catch up")
			continue
		}
		_ = s.automationRepo.IncrementAutomationStat(ctx, workspaceID, ca.AutomationID, "exited")
		s.createAutomationEndEvent(ctx, workspaceID, ca, reason)
	}
}

// createReplyTimelineEvent pose un événement timeline 'email.replied' (cohérent avec le
// style des autres events contact). Best-effort.
func (s *VeridianReplyService) createReplyTimelineEvent(ctx context.Context, workspaceID string, detection domain.VeridianReplyDetection, repliedAt time.Time) {
	changes := map[string]interface{}{
		"match_type": map[string]interface{}{"new": string(detection.MatchType)},
	}
	if detection.MatchedMessageID != "" {
		changes["matched_message_id"] = map[string]interface{}{"new": detection.MatchedMessageID}
	}
	entry := &domain.ContactTimelineEntry{
		Email:      detection.ContactEmail,
		Operation:  "insert",
		EntityType: "contact",
		Kind:       "email.replied",
		Changes:    changes,
		CreatedAt:  repliedAt,
	}
	if err := s.timelineRepo.Create(ctx, workspaceID, entry); err != nil {
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"contact":      detection.ContactEmail,
			"error":        err.Error(),
		}).Warn("VeridianReply: create email.replied timeline event failed")
	}
}

// createAutomationEndEvent pose l'événement 'automation.end' (même Kind que l'executor
// upstream pour cohérence console) lors de l'exit actif. Best-effort.
func (s *VeridianReplyService) createAutomationEndEvent(ctx context.Context, workspaceID string, ca *domain.ContactAutomation, exitReason string) {
	entry := &domain.ContactTimelineEntry{
		Email:      ca.ContactEmail,
		Operation:  "update",
		EntityType: "automation",
		Kind:       "automation.end",
		EntityID:   &ca.AutomationID,
		Changes: map[string]interface{}{
			"automation_id": map[string]interface{}{"new": ca.AutomationID},
			"exit_reason":   map[string]interface{}{"new": exitReason},
			"status":        map[string]interface{}{"new": string(ca.Status)},
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := s.timelineRepo.Create(ctx, workspaceID, entry); err != nil {
		s.logger.WithFields(map[string]interface{}{
			"workspace_id":  workspaceID,
			"contact":       ca.ContactEmail,
			"automation_id": ca.AutomationID,
			"error":         err.Error(),
		}).Warn("VeridianReply: create automation.end timeline event failed")
	}
}
