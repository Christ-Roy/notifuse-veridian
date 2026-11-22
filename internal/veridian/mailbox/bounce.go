package mailbox

import (
	"context"
	"strings"
)

// Bounce : un rapport de non-remise lu dans une boîte, tel que la chaîne des rebonds le consomme.
//
// VER-220 (22/11/2026) : la boîte unifiée est la SEULE source de la chaîne des rebonds. Avant, un
// second lecteur IMAP (consommateur « bounce-loop » du poller historique) la nourrissait : en prod
// il n'avait vu en 30 jours qu'un message de la boîte des rapports d'un relais et aucun de l'autre,
// pendant que la boîte unifiée en rangeait 101. 126 envois rebondis restaient « partis », les
// relances continuaient.
type Bounce struct {
	// ProfileID : le profil d'envoi d'où est parti le mail rebondi (intégration d'envoi), si connu.
	ProfileID string
	// SentMessageID : l'id message_history de l'envoi, quand le rapport le cite (lien posé par l'ingestion).
	SentMessageID string
	// OriginalMessageID : le Message-ID cité par le rapport, sinon.
	OriginalMessageID string
	Recipient         string
	Code              string
	Diagnostic        string
	Severity          string
}

// BounceSink applique un rebond (historique, suppression du contact, sortie des séquences).
type BounceSink interface {
	BounceReceived(ctx context.Context, workspaceID string, b Bounce) error
}

// SetBounceSink branche la chaîne des rebonds sur l'ingestion (nil : aucun effet).
func (g *Ingestor) SetBounceSink(s BounceSink) { g.bounces = s }

// bounceOf rend le rebond porté par un mail classé, ou nil s'il n'y en a pas.
func bounceOf(v Verdict, link *Link, profile string) *Bounce {
	if v.DSN == nil || (v.Kind != KindBounceHard && v.Kind != KindBounceSoft) {
		return nil
	}
	b := &Bounce{
		ProfileID:         profile,
		OriginalMessageID: v.DSN.OriginalMessageID,
		Recipient:         strings.ToLower(strings.TrimSpace(firstNonEmpty(v.DSN.Recipient, v.Recipient))),
		Code:              v.DSN.Code,
		Diagnostic:        v.DSN.Diagnostic,
		Severity:          v.DSN.Severity,
	}
	if link != nil {
		b.SentMessageID = link.SentMessageID
	}
	if b.Recipient == "" {
		return nil // sans adresse en échec, rien à appliquer
	}
	return b
}

// applyBounce remet le rebond à la chaîne. Best-effort : un échec est journalisé, le mail reste
// rangé, et le réconciliateur (invariant 1, « rebond non appliqué ») le signale et le rejoue.
func (g *Ingestor) applyBounce(ctx context.Context, ws string, b *Bounce) {
	if g.bounces == nil || b == nil {
		return
	}
	if err := g.bounces.BounceReceived(ctx, ws, *b); err != nil {
		g.log.WithFields(map[string]interface{}{
			"workspace_id": ws, "dsn": b.Code, "error": err.Error(),
		}).Warn("Mailbox: bounce not applied, the reconciler will report and replay it")
	}
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
