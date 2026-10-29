package mailbox

import "fmt"

// NewThread assemble un fil à partir de ses messages (ordre chronologique) : bornes, nature du
// dernier message et jalons lisibles (premier mail, relances, réponse, rebond).
func NewThread(id string, msgs []Message) *Thread {
	t := &Thread{ThreadID: id, Messages: msgs, Count: len(msgs)}
	if len(msgs) == 0 {
		return t
	}
	t.FirstAt, t.LastAt = msgs[0].OccurredAt, msgs[len(msgs)-1].OccurredAt
	t.LastKind = msgs[len(msgs)-1].Kind
	t.ContactEmail = msgs[0].ContactEmail
	t.Subject = msgs[0].Subject
	for _, m := range msgs {
		if m.Direction == DirectionOut {
			t.Subject = m.Subject
			break
		}
	}
	out := 0
	for _, m := range msgs {
		ms := Milestone{MessageID: m.ID, Kind: m.Kind, At: m.OccurredAt}
		switch m.Kind {
		case KindSent:
			if out == 0 {
				ms.Label = "Premier mail"
			} else {
				ms.Label = fmt.Sprintf("Relance %d", out)
			}
			out++
		case KindReplyHuman:
			ms.Label = "Réponse"
		case KindReplyAuto:
			ms.Label = "Réponse automatique"
		case KindBounceHard:
			ms.Label = "Rebond définitif"
		case KindBounceSoft:
			ms.Label = "Rebond temporaire"
		case KindDSN:
			ms.Label = "Rapport de livraison"
		default:
			ms.Label = "Autre message"
		}
		t.Milestones = append(t.Milestones, ms)
	}
	return t
}
