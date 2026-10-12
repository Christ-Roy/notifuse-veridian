package queue

import (
	"errors"
	"sort"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork (12/10/2026, demande Robert) : UNE RELANCE NE PART PAS SI LE PREMIER MAIL
// (J0) DE LA MEME AUTOMATION N'A JAMAIS ETE LIVRE A CE CONTACT.
//
// Constat du 12/10 : 9 relances sur 539 sont parties vers des contacts dont le J0 avait ete
// refuse par le serveur destinataire (refus de politique 5.7.x), et deux prospects ont repondu
// « je n'ai rien recu » (Vergers de Montdragon). Un « Re: » sans premier mail est absurde chez
// le prospect et coute en reputation d'expediteur.
//
// Regle, appliquee au depilage (tout debut de processEntry, avant les portes de debit) :
//   - l'entree est une RELANCE si son noeud (ou, pour une entree posee avant V62 sans noeud,
//     le noeud email du meme modele) est atteignable depuis un autre noeud email du graphe ;
//   - le J0 est le PLUS ANCIEN message de cette automation vers ce contact (message_history) ;
//   - J0 non livre = echec d'envoi (failed_at), rebond dur (bounced_at, bounce_type HardBounce :
//     5.1.x, 5.4.1 Exchange, etc.), refus de politique (bounce_type PolicyBounce, 5.7.x), envoi
//     jamais accepte (sent_at vide) ou aucun J0 trouve ;
//   - un rebond MOU (bounce_type SoftBounce : 4.x.x, 5.2.2 boite pleine, 5.4.x routage, classement
//     de domain.ClassifyBounce) ou un report temporaire NE sort PAS le contact : le mail a ete
//     accepte, la relance part ;
//   - sortie : la ligne de file est supprimee, l'executeur est prevenu (le contact sort de la
//     sequence avec la raison en exit_reason) et la decision est journalisee (fiche 62, issue
//     « exited », raison first_mail_*).
//
// Une lecture d'historique en erreur ne laisse PAS partir la relance (echec ferme, nouvelle
// tentative dans la minute, comme la garde finale). Automation en pause : on ne touche a rien
// (la garde finale reporte la ligne, le contact reste gele).

// veridianFirstMailVerdict est le resultat pur de l'examen du J0.
type veridianFirstMailVerdict struct {
	Delivered bool
	Reason    string // domain.VeridianReasonFirstMail* (vide si livre)
	Detail    string // lisible, pour le journal des decisions
	FirstID   string // id du J0 examine ("" si aucun)
}

// veridianFirstMailDelivery examine l'historique du contact : le J0 est le plus ancien envoi
// d'email non transactionnel de cette automation, hors l'entree courante.
func veridianFirstMailDelivery(history []*domain.MessageHistory, automationID, currentMessageID string) veridianFirstMailVerdict {
	cands := make([]*domain.MessageHistory, 0, len(history))
	for _, m := range history {
		if m == nil || m.AutomationID == nil || *m.AutomationID != automationID || m.ID == currentMessageID {
			continue
		}
		if m.Channel != "" && m.Channel != "email" {
			continue
		}
		if m.VeridianMessageType == domain.VeridianMessageTypeTransactional {
			continue
		}
		cands = append(cands, m)
	}
	if len(cands) == 0 {
		return veridianFirstMailVerdict{
			Reason: domain.VeridianReasonFirstMailMissing,
			Detail: "aucun premier mail (J0) trouve pour ce contact dans cette automation",
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].CreatedAt.Before(cands[j].CreatedAt) })
	j0 := cands[0]

	info := ""
	if j0.StatusInfo != nil {
		info = strings.TrimSpace(*j0.StatusInfo)
	}
	describe := func(label string) string {
		d := "premier mail (J0) " + label
		if info != "" {
			d += " : " + info
		}
		return d
	}

	switch {
	case j0.FailedAt != nil:
		return veridianFirstMailVerdict{Reason: domain.VeridianReasonFirstMailFailed, Detail: describe("en echec d'envoi"), FirstID: j0.ID}
	case j0.BounceType == domain.VeridianBounceTypePolicy:
		return veridianFirstMailVerdict{Reason: domain.VeridianReasonFirstMailRefused, Detail: describe("refuse par le serveur destinataire (politique)"), FirstID: j0.ID}
	case j0.BounceType == domain.VeridianBounceTypeHard,
		j0.BouncedAt != nil && j0.BounceType != domain.VeridianBounceTypeSoft:
		return veridianFirstMailVerdict{Reason: domain.VeridianReasonFirstMailBounced, Detail: describe("en rebond dur"), FirstID: j0.ID}
	case j0.SentAt == nil:
		return veridianFirstMailVerdict{Reason: domain.VeridianReasonFirstMailNotSent, Detail: describe("jamais parti"), FirstID: j0.ID}
	}
	// Accepte par le serveur destinataire. Un rebond mou (BounceType SoftBounce) ne change rien.
	return veridianFirstMailVerdict{Delivered: true, FirstID: j0.ID}
}

// veridianNodeSuccessors liste les noeuds suivants d'un noeud : next_node_id et toute cible
// portee par la config (variantes d'un A/B, branches, etc. : cle terminee par « node_id »).
func veridianNodeSuccessors(n *domain.AutomationNode) []string {
	var out []string
	if n.NextNodeID != nil && *n.NextNodeID != "" {
		out = append(out, *n.NextNodeID)
	}
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case map[string]interface{}:
			for k, val := range t {
				if s, ok := val.(string); ok && s != "" && strings.HasSuffix(strings.ToLower(k), "node_id") {
					out = append(out, s)
					continue
				}
				walk(val)
			}
		case []interface{}:
			for _, val := range t {
				walk(val)
			}
		}
	}
	walk(n.Config)
	return out
}

// veridianEntryIsFollowUp : l'entree est-elle une relance, c'est-a-dire envoyee par un noeud
// email atteignable depuis un AUTRE noeud email ? Faux au moindre doute (noeud ou modele
// introuvable, premier mail, ambiguite) : on ne supprime jamais un premier mail.
func veridianEntryIsFollowUp(a *domain.Automation, entry *domain.EmailQueueEntry) bool {
	if a == nil || entry == nil {
		return false
	}
	var cands []string
	if entry.NodeID != "" {
		if n := a.GetNodeByID(entry.NodeID); n != nil && n.Type == domain.NodeTypeEmail {
			cands = append(cands, n.ID)
		}
	}
	if len(cands) == 0 && entry.TemplateID != "" {
		for _, n := range a.Nodes {
			if n != nil && n.Type == domain.NodeTypeEmail {
				if tid, _ := n.Config["template_id"].(string); tid == entry.TemplateID {
					cands = append(cands, n.ID)
				}
			}
		}
	}
	if len(cands) == 0 {
		return false
	}

	// Noeuds atteignables depuis les successeurs de chaque noeud email.
	reached := map[string]bool{}
	var queue []string
	for _, n := range a.Nodes {
		if n != nil && n.Type == domain.NodeTypeEmail {
			queue = append(queue, veridianNodeSuccessors(n)...)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if reached[id] {
			continue
		}
		reached[id] = true
		if n := a.GetNodeByID(id); n != nil {
			queue = append(queue, veridianNodeSuccessors(n)...)
		}
	}
	for _, id := range cands {
		if !reached[id] {
			return false // au moins un noeud candidat est un premier mail
		}
	}
	return true
}

// veridianFirstMailGate rend true si l'entree peut continuer son chemin, false si elle a ete
// traitee ici (supprimee, contact sorti) ou reportee faute de pouvoir lire l'historique.
func (w *EmailQueueWorker) veridianFirstMailGate(workspace *domain.Workspace, entry *domain.EmailQueueEntry) bool {
	if workspace == nil || entry == nil ||
		entry.SourceType != domain.EmailQueueSourceAutomation || entry.SourceID == "" || entry.ContactEmail == "" ||
		w.automationRepo == nil || w.messageHistoryRepo == nil {
		return true
	}

	history, _, err := w.messageHistoryRepo.GetByContact(w.ctx, workspace.ID, workspace.Settings.SecretKey, entry.ContactEmail, veridianThreadLookupLimit, 0)
	if err != nil {
		w.retryAutomationGuard(workspace.ID, entry, errors.New("first mail lookup: "+err.Error()))
		return false
	}
	verdict := veridianFirstMailDelivery(history, entry.SourceID, entry.MessageID)
	if verdict.Delivered {
		return true
	}

	// J0 non livre ou absent : on ne sort le contact que si l'entree est BIEN une relance
	// (le premier mail lui-meme n'a evidemment pas de J0) et si l'automation tourne.
	automation, err := w.automationRepo.GetByID(w.ctx, workspace.ID, entry.SourceID)
	if err != nil || automation == nil || automation.Status != domain.AutomationStatusLive {
		return true // la garde finale gere l'automation absente, en pause ou illisible
	}
	if !veridianEntryIsFollowUp(automation, entry) {
		return true
	}

	w.veridianExitFirstMailUndelivered(workspace, entry, verdict)
	return false
}

// veridianExitFirstMailUndelivered supprime la relance, fait sortir le contact et journalise.
func (w *EmailQueueWorker) veridianExitFirstMailUndelivered(workspace *domain.Workspace, entry *domain.EmailQueueEntry, v veridianFirstMailVerdict) {
	if err := w.queueRepo.Delete(w.ctx, workspace.ID, entry.ID); err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID,
			"reason":   v.Reason,
			"error":    err.Error(),
		}).Error("First mail not delivered: failed to delete the follow-up queue row")
		return
	}
	w.logger.WithFields(map[string]interface{}{
		"entry_id":      entry.ID,
		"automation_id": entry.SourceID,
		"recipient":     entry.ContactEmail,
		"reason":        v.Reason,
		"first_mail_id": v.FirstID,
	}).Info("First mail not delivered: follow-up dropped, contact leaves the sequence")

	// Sans ce rappel le contact resterait parque en « sending » sans entree en file.
	if w.onEmailFailed != nil {
		w.onEmailFailed(workspace.ID, entry.SourceType, entry.SourceID, entry.ContactEmail, entry.MessageID, errors.New(v.Reason), true)
	}
	w.veridianRecordTerminal(workspace, entry, domain.VeridianOutcomeExited, v.Reason, v.Detail, entry.IntegrationID, nil)
}
