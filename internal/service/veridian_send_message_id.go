package service

import (
	"strings"
)

// Veridian fork — Message-ID RFC822 matchable à l'envoi (Lot 3 stop-on-reply, 2026-06-15).
//
// Pour matcher FORTEMENT les réponses prospect (stop-on-reply), on doit pouvoir relier
// l'In-Reply-To/References d'une réponse à l'envoi correspondant. Par défaut, go-mail
// génère un Message-ID RFC822 ALÉATOIRE (au moment du WriteTo) que Notifuse ne connaît
// pas et ne persiste pas → impossible à retrouver. On le rend déterministe en posant
// nous-mêmes le header `Message-ID: <{message_history.id}@{domaine_envoi}>`.
//
// La local-part = notre message_history.id (request.MessageID, un UUID). Quand le
// prospect répond, son client recopie ce Message-ID dans In-Reply-To/References ; le
// reply-detection (Lot 3) en ré-extrait la local-part et la retrouve dans message_history
// (FindContactEmailByMessageID). Le domaine n'est qu'un host RFC-valide (peut être
// réécrit par un relai sans casser le match, qui ne porte que sur la local-part).
//
// On ne touche PAS le X-Message-ID (header de tracking interne déjà posé) : c'est un
// usage distinct (clic/open/webhooks). Ici on standardise le Message-ID RFC822, ce que
// tout ESP cold fait pour le threading.
//
// Idempotent / sûr : si messageID ou fromAddress sont vides/illisibles, on retourne ""
// et le caller laisse go-mail générer son Message-ID aléatoire (comportement upstream,
// non-régression — juste pas de match fort pour cet envoi).

// veridianMessageIDForSend construit la valeur du header Message-ID RFC822 (SANS les
// chevrons, que go-mail ajoute) à partir de l'id de message et de l'adresse d'envoi.
// Retourne "" si non constructible (→ fallback go-mail aléatoire).
func veridianMessageIDForSend(messageID, fromAddress string) string {
	id := strings.TrimSpace(messageID)
	if id == "" {
		return ""
	}
	domain := veridianHostFromEmail(fromAddress)
	if domain == "" {
		return ""
	}
	return id + "@" + domain
}

// veridianHostFromEmail extrait le host (partie après le dernier '@') d'une adresse,
// en tolérant un format "Display Name <addr@host>". Retourne "" si pas de host.
func veridianHostFromEmail(raw string) string {
	s := strings.TrimSpace(raw)
	if lt := strings.LastIndexByte(s, '<'); lt >= 0 {
		if gt := strings.IndexByte(s[lt:], '>'); gt >= 0 {
			s = s[lt+1 : lt+gt]
		}
	}
	at := strings.LastIndexByte(s, '@')
	if at < 0 || at == len(s)-1 {
		return ""
	}
	return strings.TrimSpace(s[at+1:])
}
