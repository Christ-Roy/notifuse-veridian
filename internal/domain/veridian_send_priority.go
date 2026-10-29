package domain

import (
	"fmt"
	"sync/atomic"
)

// Veridian fork, lot L3 (29/10/2026) : priorité d'envoi PAR campagne ou PAR automation.
//
// Recommandation A du Livrable 4 de l'architecte. La file dépile priority ASC (1 d'abord),
// puis les relances avant les premiers contacts, puis un tour de rôle entre sources
// (fiche 58). Avant, un mail marketing partait toujours à 5. Maintenant une campagne ou une
// automation peut porter une priorité de 2 à 9 (défaut 5 = comportement d'avant) ; 1 reste
// réservé au transactionnel de séquence, sinon un envoi marketing pourrait dépasser un
// transactionnel. La priorité ne change l'ordre QUE si la file est engorgée entre sources :
// quand le plafond d'envoi est la limite, elle ne fait pas partir plus vite.
//
// La valeur vit dans la table veridian_send_priority (V66), lue à l'enqueue par le fournisseur
// branché par internal/veridian/policy. Sans fournisseur (tests, avant câblage) : 5.

const (
	// VeridianMinMarketingPriority : 1 est réservé au transactionnel de séquence.
	VeridianMinMarketingPriority = 2
	// VeridianMaxMarketingPriority : la plus basse priorité.
	VeridianMaxMarketingPriority = 9
)

// VeridianValidateMarketingPriority : 2 à 9, entiers.
func VeridianValidateMarketingPriority(p int) error {
	if p < VeridianMinMarketingPriority || p > VeridianMaxMarketingPriority {
		return fmt.Errorf("priority doit être entre %d et %d (1 est réservé au transactionnel de séquence), reçu %d",
			VeridianMinMarketingPriority, VeridianMaxMarketingPriority, p)
	}
	return nil
}

// VeridianSendPriorityProvider donne la priorité posée sur une source (campagne ou
// automation). 0 = aucune priorité posée (défaut). Appelé à chaque enqueue : une
// implémentation garde ses lectures en cache et ne bloque jamais.
type VeridianSendPriorityProvider interface {
	SendPriorityFor(workspaceID string, sourceType EmailQueueSourceType, sourceID string) int
}

type veridianSendPriorityHolder struct{ p VeridianSendPriorityProvider }

var veridianSendPriorityValue atomic.Value // veridianSendPriorityHolder

// SetVeridianSendPriorityProvider branche le fournisseur (nil = priorité 5 partout).
func SetVeridianSendPriorityProvider(p VeridianSendPriorityProvider) {
	veridianSendPriorityValue.Store(veridianSendPriorityHolder{p: p})
}

// VeridianSendPriority : la priorité de file d'un mail marketing de cette source. Toujours
// dans [2 ; 9] ; une valeur hors plage (donnée corrompue) retombe sur le défaut 5.
func VeridianSendPriority(workspaceID string, sourceType EmailQueueSourceType, sourceID string) int {
	h, ok := veridianSendPriorityValue.Load().(veridianSendPriorityHolder)
	if !ok || h.p == nil {
		return EmailQueuePriorityMarketing
	}
	p := h.p.SendPriorityFor(workspaceID, sourceType, sourceID)
	if VeridianValidateMarketingPriority(p) != nil {
		return EmailQueuePriorityMarketing
	}
	return p
}
