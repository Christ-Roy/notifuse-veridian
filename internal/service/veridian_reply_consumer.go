package service

import (
	"context"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// replyProcessTimeout borne le traitement d'un message (détection réponse +
// signal + exit séquence = un lookup DB + quelques updates). Sans borne, un
// ProcessInboundMessage qui traîne sur une DB lente bloquerait la goroutine du
// poller indéfiniment (OnNewMessage est synchrone). Aligné sur bounceProcessTimeout.
const replyProcessTimeout = 20 * time.Second

// Veridian fork — Consumer IMAP stop-on-reply (Lot 3 sprint cold outbound, 2026-06-15).
//
// Implémente domain.VeridianIMAPConsumer : enregistré sur le poller IMAP (Lot 1) via
// RegisterConsumer, il reçoit chaque message NEUF de chaque boîte IMAP pollée et le
// délègue au VeridianReplyService (détection + signal + exit). Mince adaptateur : toute
// la logique vit dans le service (testé à part) ; le consumer se borne au contrat IMAP.
//
// Garanties (cf. doc VeridianIMAPConsumer) : OnNewMessage est synchrone dans la
// goroutine du poller (travail rapide attendu — un lookup DB + éventuels updates), et
// l'UID n'est acquitté que si tous les consumers réussissent. D'où
// l'IDEMPOTENCE MÉTIER du service (ON CONFLICT DO NOTHING + exit idempotent) :
// un re-dispatch après erreur ne double aucun effet.

// veridianReplyProcessor est le contrat minimal que le consumer attend du service
// (découplage / testabilité). Implémenté par *VeridianReplyService.
type veridianReplyProcessor interface {
	ProcessInboundMessage(ctx context.Context, msg *domain.VeridianIMAPMessage) error
}

// VeridianReplyConsumer adapte le VeridianReplyService au contrat VeridianIMAPConsumer.
type VeridianReplyConsumer struct {
	processor veridianReplyProcessor
	logger    logger.Logger
}

// NewVeridianReplyConsumer construit le consumer stop-on-reply.
func NewVeridianReplyConsumer(processor veridianReplyProcessor, log logger.Logger) *VeridianReplyConsumer {
	return &VeridianReplyConsumer{
		processor: processor,
		logger:    log,
	}
}

// Name identifie le consumer dans les logs du poller.
func (c *VeridianReplyConsumer) Name() string {
	return "stop-on-reply"
}

// OnNewMessage traite un message entrant : délègue au service. L'erreur éventuelle est
// remontée au poller (qui la loggue) et empêche l'acquittement de l'UID. Le
// service est idempotent côté métier, un re-dispatch ne double rien.
func (c *VeridianReplyConsumer) OnNewMessage(msg *domain.VeridianIMAPMessage) error {
	if msg == nil {
		return nil
	}
	// Le poller appelle OnNewMessage sans contexte (contrat domain) : on borne
	// nous-mêmes par un timeout, sinon un traitement lent bloque sa goroutine.
	ctx, cancel := context.WithTimeout(context.Background(), replyProcessTimeout)
	defer cancel()
	return c.processor.ProcessInboundMessage(ctx, msg)
}
