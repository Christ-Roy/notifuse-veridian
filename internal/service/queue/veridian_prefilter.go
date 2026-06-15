package queue

import (
	"net/mail"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/disposable_emails"
)

// Veridian — gate de PRÉ-FILTRAGE d'envoi cold outbound (Lot 7, ticket
// todo/2026-06-14-bounce-loop-postfix-suppression-cold.md lot B « pré-filtrage »).
//
// BUT : ne PAS taper le serveur SMTP pour une adresse qu'on SAIT déjà morte —
// chaque envoi vers une adresse invalide est un bounce probable qui grille la
// réputation IP et gaspille du quota. On filtre TROIS conditions DURABLES (qui
// ne deviendront jamais valides en re-tentant) AVANT MarkAsProcessing :
//   1. syntaxe d'adresse invalide (mail.ParseAddress, RFC 5322) ;
//   2. domaine jetable (pkg/disposable_emails, liste embarquée) ;
//   3. domaine DNS-mort de façon DÉCISIVE (NXDOMAIN, ou aucun MX ET aucun A/AAAA
//      — cf. domain.VeridianMXClassifier.ResolveDeliverability, RFC 5321).
//
// DIFFÉRENCE de contrat avec les gates throttle/cap (qui RESCHEDULENT) : une
// adresse invalide ne redeviendra jamais valide. Le pré-filtre ne reporte donc
// PAS l'entrée — le worker la route vers l'échec PERMANENT (chemin upstream
// `550 user unknown` : Delete de l'entrée queue + message_history FailedAt),
// pour qu'elle ne soit JAMAIS re-tentée en boucle.
//
// BEST-EFFORT STRICT (non-régression critique) : tout doute laisse PASSER.
//   - la syntaxe et le domaine jetable sont des checks PURS, zéro I/O, toujours
//     actifs (gratuit, déterministe) ;
//   - le check DNS est best-effort : timeout / erreur transitoire / resolver sans
//     capacité host → verdict INDÉTERMINÉ → on laisse partir l'envoi. JAMAIS un
//     glitch DNS ne bloque un destinataire légitime.
//   - on NE supprime PAS le contact ni ne touche à contact_lists ici : la
//     suppression durable du contact est la prérogative du bounce réel (Lot 2,
//     webhook NDR Postfix → MarkEmailsAsBounced) ; la dupliquer ici serait
//     justement le contournement interdit. Le pré-filtre est la DERNIÈRE ligne
//     de défense par ENVOI, pas le registre de suppression de contacts.

// veridianPrefilterReason décrit pourquoi une adresse a été pré-filtrée (sert au
// message d'échec permanent loggé / persisté en message_history).
type veridianPrefilterReason string

const (
	veridianPrefilterSyntax        veridianPrefilterReason = "invalid_syntax"
	veridianPrefilterDisposable    veridianPrefilterReason = "disposable_domain"
	veridianPrefilterUndeliverable veridianPrefilterReason = "undeliverable_domain"
)

// veridianPrefilterRecipient applique les trois checks de pré-filtrage à une
// adresse. Retourne (reason, true) si l'adresse doit être SKIPPÉE en échec
// permanent (jamais re-tentée), ("", false) si elle peut partir.
//
// Fonction du worker (méthode) pour réutiliser le classifier MX déjà injecté
// (même DI que les autres gates) — aucune nouvelle dépendance.
func (w *EmailQueueWorker) veridianPrefilterRecipient(entry *domain.EmailQueueEntry) (veridianPrefilterReason, bool) {
	email := strings.TrimSpace(entry.ContactEmail)

	// 1. Syntaxe (RFC 5322 via stdlib) — check PUR, zéro I/O. mail.ParseAddress
	//    refuse les adresses vides, sans @, sans domaine, mal formées. Une adresse
	//    syntaxiquement invalide ne sera jamais acceptée par aucun MTA.
	if !veridianValidEmailSyntax(email) {
		return veridianPrefilterSyntax, true
	}

	domainPart := veridianEmailDomain(email)

	// 2. Domaine jetable — check PUR sur la liste embarquée (slices.Contains sur
	//    le DOMAINE, pas l'email : la liste pkg/disposable_emails est une liste de
	//    DOMAINES). Le cold n'a aucun intérêt à toucher une boîte jetable.
	if domainPart != "" && disposable_emails.IsDisposableEmail(domainPart) {
		return veridianPrefilterDisposable, true
	}

	// 3. Délivrabilité DNS — best-effort STRICT : seul un verdict DÉCISIF
	//    « undeliverable » (NXDOMAIN, ou ni MX ni A/AAAA) bloque. Indéterminé
	//    (timeout, transitoire, resolver sans host lookup) → laisse passer.
	if w.providerMXClassifier != nil && domainPart != "" {
		if w.providerMXClassifier.ResolveDeliverability(w.ctx, domainPart) == domain.VeridianMXUndeliverable {
			return veridianPrefilterUndeliverable, true
		}
	}

	return "", false
}

// veridianValidEmailSyntax valide la syntaxe d'une adresse via la stdlib.
// mail.ParseAddress accepte la forme « Name <a@b> » ; pour une adresse de queue
// on veut une adresse NUE, donc on vérifie aussi qu'elle ne contient ni espace
// ni chevron et qu'elle a exactement un domaine non vide.
func veridianValidEmailSyntax(email string) bool {
	if email == "" {
		return false
	}
	if strings.ContainsAny(email, " <>\t\r\n,") {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return false
	}
	// ParseAddress peut normaliser ; on exige que l'adresse parsée soit identique
	// (pas de display name caché) et qu'elle ait un domaine.
	if addr.Address != email {
		return false
	}
	at := strings.LastIndex(email, "@")
	return at > 0 && at < len(email)-1
}

// veridianEmailDomain extrait le domaine lowercase d'une adresse déjà jugée
// syntaxiquement valide. Retourne "" si pas de domaine exploitable.
func veridianEmailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	d := strings.ToLower(strings.TrimSpace(email[at+1:]))
	return strings.TrimSuffix(d, ".")
}
