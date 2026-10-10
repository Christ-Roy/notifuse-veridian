package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork (fiche 62, lot 1) : reconciliation des contacts PARQUES en envoi dont
// l'entree de file a disparu (cf. domain/veridian_parked_contact.go).
//
// Tourne dans le tick EXISTANT du planificateur d'automations (ProcessBatch), un
// workspace par tick en tourniquet : aucune tache planifiee nouvelle, charge bornee.
// Idempotent : un contact reconcilie n'est plus « sending », il ne reapparait pas.
//
// Regle pour chaque orphelin (jamais de mail en double) :
//  1. le mail mis en file est PARTI (sent_at)         -> on avance, comme le callback perdu ;
//  2. le mail a ECHOUE definitivement                -> le contact sort (echec) ;
//  3. aucun mail connu ET le contact a deja recu un mail -> il sort (jamais de « premier
//     mail » a quelqu'un deja contacte) ;
//  4. aucun mail connu et jamais contacte             -> le contact est REARME sur son
//     noeud email : le noeud rejoue ses controles (reponse, statut de liste) puis remet
//     en file UN mail. Aucun mail n'etait parti, il n'y a donc rien a doubler.
//  5. un message_history existe mais sans etat (ni envoye ni echoue) -> on n'y touche pas.

const (
	veridianOrphanGrace = 15 * time.Minute
	veridianOrphanBatch = 100
)

// veridianOrphanState : curseur de tourniquet entre workspaces.
type veridianOrphanState struct {
	mu     sync.Mutex
	cursor int
}

// veridianReconcileOneWorkspace reconcilie les orphelins du PROCHAIN workspace.
// Rend le nombre de contacts remis en etat.
func (e *AutomationExecutor) veridianReconcileOneWorkspace(ctx context.Context) int {
	repo, ok := e.automationRepo.(domain.VeridianOrphanParkedRepository)
	if !ok || e.workspaceRepo == nil {
		return 0
	}
	workspaces, err := e.workspaceRepo.List(ctx)
	if err != nil || len(workspaces) == 0 {
		return 0
	}
	e.orphanState.mu.Lock()
	ws := workspaces[e.orphanState.cursor%len(workspaces)]
	e.orphanState.cursor++
	e.orphanState.mu.Unlock()
	return e.VeridianReconcileOrphans(ctx, repo, ws.ID)
}

// VeridianReconcileOrphans traite jusqu'a veridianOrphanBatch orphelins d'un workspace.
func (e *AutomationExecutor) VeridianReconcileOrphans(ctx context.Context, repo domain.VeridianOrphanParkedRepository, workspaceID string) int {
	orphans, err := repo.ListOrphanParked(ctx, workspaceID, veridianOrphanGrace, veridianOrphanBatch)
	if err != nil {
		e.logger.WithField("workspace_id", workspaceID).WithField("error", err.Error()).Warn("orphan reconcile: listing failed")
		return 0
	}
	handled := 0
	for _, p := range orphans {
		if e.veridianReconcileOrphan(ctx, workspaceID, p) {
			handled++
		}
	}
	if handled > 0 {
		e.logger.WithField("workspace_id", workspaceID).WithField("count", handled).Info("orphan reconcile: parked contacts without queue entry put back in a coherent state")
	}
	return handled
}

func (e *AutomationExecutor) veridianReconcileOrphan(ctx context.Context, workspaceID string, p domain.VeridianParkedContact) bool {
	switch {
	case p.MessageSent:
		e.HandleEmailSent(workspaceID, domain.EmailQueueSourceAutomation, p.AutomationID, p.ContactEmail, p.MessageID)
		return true
	case p.MessageFailed:
		e.HandleEmailFailed(workspaceID, domain.EmailQueueSourceAutomation, p.AutomationID, p.ContactEmail, p.MessageID, errors.New("orphan_send_failed"), true)
		return true
	case p.MessageFound:
		return false // etat inconnu : ne rien deviner
	}

	ca, err := e.automationRepo.GetContactAutomationByEmail(ctx, workspaceID, p.AutomationID, p.ContactEmail)
	if err != nil || ca.ID != p.ContactAutomationID || ca.Status != domain.ContactAutomationStatusSending {
		return false // a bouge entre-temps
	}
	if p.AlreadyContacted {
		return e.markAsExited(ctx, workspaceID, ca, "orphan_already_contacted") == nil
	}
	now := time.Now().UTC()
	ca.Status = domain.ContactAutomationStatusActive
	ca.ScheduledAt = &now
	return e.automationRepo.UpdateContactAutomation(ctx, workspaceID, ca) == nil
}
