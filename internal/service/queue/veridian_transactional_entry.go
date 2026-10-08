package queue

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/emailerror"
)

// Veridian fork, lot 4 (08/10/2026) : isolation du transactionnel dans le worker.
//
// Un mail transactionnel peut entrer dans la file : une sequence dont le modele est
// de categorie "transactional" (confirmation de commande, suivi...). Avant ce lot
// il traversait toutes les portes du commercial : rotation des profils, plafond du
// profil, chauffe, debit par classe, fenetre d'envoi, fusible de reputation. Une
// fenetre fermee la nuit ou un fusible declenche par la prospection retenait un
// mot de passe oublie.
//
// Quand le workspace reserve un profil transactionnel
// (Workspace.VeridianReservedTransactionalProfileID), une entree marquee
// Payload.VeridianTransactional :
//   - part UNIQUEMENT par ce profil (jamais la rotation commerciale, meme si le
//     profil memorise a la mise en file etait un autre) ;
//   - ne passe AUCUNE porte commerciale : ni exclusion de classe, fusible, debit par
//     classe, plafond (destinataire, adresse, classe, profil, chauffe), fenetre, pre-
//     filtre, anti-hash, ni reservation du quota atomique ;
//   - ne s'arrete que pour une panne reelle : disjoncteur du profil ouvert, rendu en
//     echec, sequence supprimee ou non vivante. Une reponse recue, un desabonnement
//     marketing ou l'etat de la liste ne retiennent pas un mail transactionnel ;
//   - s'ecrit dans message_history comme transactionnel (profil, type), donc ne compte
//     dans aucun compteur commercial.
//
// Sans profil transactionnel reserve, l'entree suit le chemin commercial natif,
// strictement comme avant (le marqueur est alors ignore).

// veridianTransactionalCircuitRetry : delai de report quand le disjoncteur du profil
// transactionnel est ouvert (le profil est en panne, reessayer vite).
const veridianTransactionalCircuitRetry = 30 * time.Second

// veridianProcessTransactionalEntry traite une entree transactionnelle. Retourne
// true si l'entree a ete prise en charge (envoyee, reportee ou en echec) : l'appelant
// s'arrete. Retourne false quand l'entree suit le chemin commercial.
func (w *EmailQueueWorker) veridianProcessTransactionalEntry(workspace *domain.Workspace, entry *domain.EmailQueueEntry) bool {
	if !entry.Payload.VeridianTransactional {
		return false
	}
	reserved := workspace.VeridianReservedTransactionalProfileID()
	if reserved == "" {
		// Aucun profil transactionnel reserve (supprime depuis la mise en file, ou
		// workspace natif) : chemin commercial, le marqueur ne doit plus rien changer
		// (type de ligne comprise).
		entry.Payload.VeridianTransactional = false
		return false
	}
	integration := workspace.GetIntegrationByID(reserved)
	if integration == nil {
		entry.Payload.VeridianTransactional = false
		return false
	}
	provider := &integration.EmailProvider

	// Le profil transactionnel est le seul transport possible. Une entree dont
	// l'integration memorisee est autre (profil change depuis la mise en file) est
	// reecrite sur lui, expediteur compris : integration, From, Message-ID et DKIM
	// vont ensemble.
	if entry.IntegrationID != reserved {
		sender := provider.GetSender("")
		if sender == nil {
			w.veridianFailTransactional(workspace, entry, fmt.Errorf("transactional profile %s has no sender", reserved))
			return true
		}
		w.logger.WithFields(map[string]interface{}{
			"entry_id":                entry.ID,
			"assigned_integration_id": entry.IntegrationID,
			"transactional_profile":   reserved,
		}).Info("Transactional entry rerouted to the reserved transactional profile")
		entry.IntegrationID = reserved
		entry.Payload.FromAddress, entry.Payload.FromName = sender.Email, sender.Name
	}
	entry.ProviderKind = provider.Kind

	// Seul le disjoncteur du profil (panne reelle du transport) retient l'entree.
	if w.circuitBreaker.IsOpen(reserved) {
		if err := w.queueRepo.SetNextRetry(w.ctx, workspace.ID, entry.ID, time.Now().Add(veridianTransactionalCircuitRetry)); err != nil {
			w.logger.WithFields(map[string]interface{}{"entry_id": entry.ID, "error": err.Error()}).Warn("Failed to reschedule transactional entry on open circuit")
		}
		return true
	}

	if !w.veridianRenderAtSend(workspace, entry) {
		return true
	}

	// Cadence technique du transport (anti-rafale vers le relais), la seule limite
	// qui reste : celle du profil transactionnel lui-meme.
	ratePerMinute := provider.VeridianEffectiveRateLimit()
	if ratePerMinute <= 0 {
		ratePerMinute = 60
	}
	if err := w.rateLimiter.Wait(w.ctx, reserved, ratePerMinute); err != nil {
		return true // arret du worker : rien a marquer
	}

	if !w.veridianTransactionalAutomationAllowed(workspace, entry) {
		return true
	}

	if err := w.queueRepo.MarkAsProcessing(w.ctx, workspace.ID, entry.ID); err != nil {
		w.logger.WithFields(map[string]interface{}{"entry_id": entry.ID, "error": err.Error()}).Warn("Failed to mark transactional entry as processing, may be processed by another worker")
		return true
	}

	request := entry.Payload.ToSendEmailProviderRequest(workspace.ID, reserved, entry.MessageID, entry.ContactEmail, provider)
	if err := w.emailService.SendEmail(w.ctx, *request, false); err != nil {
		classified := w.errorClassifier.Classify(err, provider.Kind)
		w.circuitBreaker.RecordFailure(reserved, classified)
		w.handleError(workspace, entry, err, classified)
		return true
	}

	w.circuitBreaker.RecordSuccess(reserved)
	if err := w.queueRepo.MarkAsSent(w.ctx, workspace.ID, entry.ID); err != nil {
		w.logger.WithFields(map[string]interface{}{"entry_id": entry.ID, "error": err.Error()}).Error("Failed to mark transactional email as sent")
		return true
	}
	w.upsertMessageHistory(w.ctx, workspace.ID, workspace.Settings.SecretKey, entry, nil)
	w.logger.WithFields(map[string]interface{}{
		"entry_id":       entry.ID,
		"integration_id": reserved,
		"message_id":     entry.MessageID,
		"recipient":      entry.ContactEmail,
		"source_id":      entry.SourceID,
	}).Debug("Transactional email sent")
	if w.onEmailSent != nil {
		w.onEmailSent(workspace.ID, entry.SourceType, entry.SourceID, entry.ContactEmail, entry.MessageID)
	}
	return true
}

// veridianFailTransactional marque une entree transactionnelle en echec lisible.
func (w *EmailQueueWorker) veridianFailTransactional(workspace *domain.Workspace, entry *domain.EmailQueueEntry, cause error) {
	if err := w.queueRepo.MarkAsProcessing(w.ctx, workspace.ID, entry.ID); err != nil {
		w.logger.WithFields(map[string]interface{}{"entry_id": entry.ID, "error": err.Error()}).Warn("Failed to mark transactional entry as processing for failure")
		return
	}
	w.handleError(workspace, entry, cause, &emailerror.ClassifiedError{
		Original:  cause,
		Type:      emailerror.ErrorTypeRecipient, // ne compte jamais pour le disjoncteur
		Retryable: false,
	})
}

// veridianTransactionalAutomationAllowed : derniere verification avant l'envoi d'un
// mail transactionnel de sequence. Seule l'existence et l'etat vivant de la
// sequence comptent ; la reponse du contact, son desabonnement et l'etat de la
// liste (arrets du COMMERCIAL, cf. veridianAutomationSendAllowed) ne retiennent pas
// un transactionnel. Une erreur de lecture reporte l'entree, jamais d'envoi a
// l'aveugle.
func (w *EmailQueueWorker) veridianTransactionalAutomationAllowed(workspace *domain.Workspace, entry *domain.EmailQueueEntry) bool {
	if entry.SourceType != domain.EmailQueueSourceAutomation {
		return true
	}
	if w.automationRepo == nil {
		// Le constructeur amont (tests) n'a pas branche la garde : comportement historique.
		return !w.finalSendGuardsConfigured
	}
	automation, err := w.automationRepo.GetByID(w.ctx, workspace.ID, entry.SourceID)
	if err != nil {
		var notFound *domain.ErrAutomationNotFound
		if errors.As(err, &notFound) {
			w.discardAutomationEntry(workspace.ID, entry, "automation_deleted")
			return false
		}
		w.retryAutomationGuard(workspace.ID, entry, fmt.Errorf("automation lookup: %w", err))
		return false
	}
	if automation.Status != domain.AutomationStatusLive {
		w.discardAutomationEntry(workspace.ID, entry, "automation_not_live")
		return false
	}
	return true
}

// veridianDropReservedFromCandidates retire le profil transactionnel reserve d'une
// liste de candidats d'envoi commercial : la rotation commerciale ne l'emprunte
// jamais, ni comme profil assigne a la mise en file, ni comme ancre de sequence, ni
// comme repli de bascule.
func veridianDropReservedFromCandidates(workspace *domain.Workspace, candidates []veridianFailoverCandidate) []veridianFailoverCandidate {
	reserved := workspace.VeridianReservedTransactionalProfileID()
	if reserved == "" {
		return candidates
	}
	kept := make([]veridianFailoverCandidate, 0, len(candidates))
	for _, c := range candidates {
		if strings.TrimSpace(c.IntegrationID) == reserved {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}
