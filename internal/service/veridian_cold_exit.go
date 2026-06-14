package service

import (
	"context"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian cold outreach — gate d'exit souverain de la cadence de relance.
//
// Point CRUCIAL du Lot 9 : garantir qu'un prospect SORT de la séquence dès qu'il a
// répondu ou bouncé, où qu'il soit dans la cadence — y compris au beau milieu d'un
// délai de 4 jours. L'engine upstream ne vérifie le statut bounced qu'au moment où le
// contact atteint un email node marketing ; ça ne suffit pas pour une cadence cold,
// car entre deux relances le contact "dort" dans un node delay et continuerait d'être
// relancé même après avoir répondu. Ce gate est appelé par l'executor à CHAQUE tick,
// AVANT de processer le node courant, donc il intercepte le contact pendant les delays.
//
// Best-effort par construction : une erreur de check (DB, repo Lot 3 indispo) ne bloque
// JAMAIS l'avancée du contact — on log et on continue (cf. executor). On ne veut pas
// qu'une panne du checker fige toutes les cadences.

// ColdReplyChecker est le contrat que le Lot 3 (stop-on-reply) branchera pour signaler
// qu'un prospect a répondu. Tant qu'aucune implémentation n'est injectée, le check reply
// est un no-op (la cadence se déroule normalement, l'exit-on-bounce reste actif).
//
// Interface attendue par le Lot 3 :
//   - HasReplied retourne true si le contact `email` a répondu à un envoi de ce workspace.
//   - L'implémentation Lot 3 décidera de la source de vérité (table de réponses IMAP,
//     événement timeline "email.replied", flag sur contact, etc.). Le gate ne présume rien
//     de cette source : il consomme juste un booléen.
//   - Une erreur (best-effort) ne doit PAS exiter le contact : le gate la propage à
//     l'executor qui log et laisse le contact avancer.
type ColdReplyChecker interface {
	HasReplied(ctx context.Context, workspaceID, email string) (bool, error)
}

// SetColdReplyChecker injecte (optionnellement) le checker de réponse du Lot 3.
// DI volontairement optionnelle : nil = exit-on-reply désactivé, exit-on-bounce conservé.
// Branché par app.go une fois le service Lot 3 disponible.
func (e *AutomationExecutor) SetColdReplyChecker(checker ColdReplyChecker) {
	e.coldReplyChecker = checker
}

// veridianColdExitReason inspecte les signaux d'arrêt cold pour un contact et retourne
// la raison d'exit (ExitReasonReplied / ExitReasonBounced) si le contact doit sortir,
// ou "" s'il peut continuer la cadence.
//
// Ordre de priorité (le plus "humain" d'abord) :
//  1. replied — le prospect a engagé, on ne le relance plus (via ColdReplyChecker du Lot 3).
//  2. bounced — l'adresse est morte/refusée, on la protège (statut contact_lists existant).
//
// Le check bounced réutilise le contactListRepo DÉJÀ injecté dans l'executor (aucune
// nouvelle dépendance) et la liste rattachée à l'automation. Pas de liste sur l'automation
// (cas event-based sans email) = pas de check bounced possible → on ne bloque pas.
//
// Retourne aussi l'erreur éventuelle pour que l'appelant la traite en best-effort.
func (e *AutomationExecutor) veridianColdExitReason(
	ctx context.Context,
	workspaceID string,
	automation *domain.Automation,
	contactEmail string,
) (string, error) {
	// 1. Réponse du prospect (Lot 3). No-op si non branché.
	if e.coldReplyChecker != nil {
		replied, err := e.coldReplyChecker.HasReplied(ctx, workspaceID, contactEmail)
		if err != nil {
			return "", err
		}
		if replied {
			return domain.ExitReasonReplied, nil
		}
	}

	// 2. Bounce / complaint : statut terminal sur la liste de l'automation.
	// Sans liste, pas de notion d'abonnement → rien à vérifier.
	if automation == nil || automation.ListID == "" {
		return "", nil
	}

	contactList, err := e.contactListRepo.GetContactListByIDs(ctx, workspaceID, contactEmail, automation.ListID)
	if err != nil {
		// "Pas dans la liste" n'est pas un bounce : le contact continue.
		if _, ok := err.(*domain.ErrContactListNotFound); ok {
			return "", nil
		}
		return "", err
	}

	// IsTerminalContactListStatus couvre bounced ET complained — deux signaux d'arrêt
	// définitif pour une cadence cold (réputation). On unifie sous ExitReasonBounced.
	if domain.IsTerminalContactListStatus(contactList.Status) {
		return domain.ExitReasonBounced, nil
	}

	return "", nil
}
