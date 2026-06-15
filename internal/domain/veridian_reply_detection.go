package domain

// Veridian fork — Détection de réponse prospect (Lot 3 sprint cold outbound,
// 2026-06-15) : "stop-on-reply".
//
// Réflexe cold #1 de décence et de réputation : si un prospect RÉPOND à un de nos
// envois, on arrête IMMÉDIATEMENT de le relancer. Ce fichier porte la LOGIQUE PURE
// de détection (aucune I/O) : à partir d'un message entrant capté par le poller IMAP
// (Lot 1, VeridianIMAPMessage), décider si c'est une réponse à un de NOS envois, et
// par quel signal.
//
// Deux signaux, par ordre de fiabilité décroissante :
//
//  1. MATCH PAR MESSAGE-ID (signal FORT, fiable, privilégié). On pose à l'envoi un
//     header RFC822 `Message-ID: <{message_history.id}@{domaine_envoi}>` (cf. diff
//     INLINE smtp_service.go). Quand le prospect répond, son client mail recopie ce
//     Message-ID dans `In-Reply-To` et/ou `References` (RFC 5322 §3.6.4). On extrait
//     la local-part (= notre message_history.id) de chacun de ces IDs, et si l'un
//     d'eux existe en base ET correspond à un envoi vers CE contact, c'est une
//     réponse certaine. Aucune ambiguïté : c'est exactement notre mail qui est cité.
//
//  2. FALLBACK PAR EXPÉDITEUR (signal FAIBLE). Si aucun In-Reply-To/References ne
//     matche (client mail exotique, header strippé par un proxy, réponse "nouveau
//     message" sans threading), on se rabat sur : `From` = un contact connu du
//     workspace ET le message N'EST PAS un NDR. C'est moins sûr (un prospect peut
//     nous écrire spontanément sans répondre à une séquence) mais le résultat métier
//     est le même et souhaitable : un humain connu nous a écrit → on ne le relance
//     plus en cold. Le fallback est volontairement borné au filtre NDR ci-dessous
//     pour ne JAMAIS confondre un rapport de non-remise (MAILER-DAEMON) avec une
//     réponse humaine — un NDR est traité par le Lot 2 (bounce-loop), pas ici.
//
// Un NDR n'est JAMAIS une réponse (sinon stop-on-reply marquerait "replied" une
// adresse en fait morte, et empêcherait le bounce-loop de la suppr). Le filtre NDR
// vit ici (autonome) pour que le Lot 3 ne dépende pas du code du Lot 2 (non encore
// mergé au moment de l'écriture) ; les deux lots partagent la MÊME heuristique de
// reconnaissance NDR, à garder synchronisée.

import (
	"strings"
)

// VeridianReplyMatchType qualifie COMMENT une réponse a été détectée (audit / log /
// timeline). MessageID est le signal fort ; SenderFallback le signal faible.
type VeridianReplyMatchType string

const (
	// VeridianReplyMatchNone : ce message n'est pas (détecté comme) une réponse.
	VeridianReplyMatchNone VeridianReplyMatchType = ""
	// VeridianReplyMatchMessageID : matché via In-Reply-To/References → notre message_history.id.
	VeridianReplyMatchMessageID VeridianReplyMatchType = "message_id"
	// VeridianReplyMatchSenderFallback : matché via From = contact connu (pas de threading exploitable).
	VeridianReplyMatchSenderFallback VeridianReplyMatchType = "sender_fallback"
)

// VeridianReplyDetection est le verdict de la détection pour un message entrant.
type VeridianReplyDetection struct {
	// IsReply : true si le message est une réponse à un de nos envois.
	IsReply bool
	// MatchType : par quel signal (vide si IsReply == false).
	MatchType VeridianReplyMatchType
	// ContactEmail : l'adresse du prospect ayant répondu (normalisée lowercase),
	// = la clé sur laquelle on pose le signal 'replied' et l'exit de séquence.
	ContactEmail string
	// MatchedMessageID : l'id message_history (local-part) qui a matché, si match
	// fort. Vide en fallback. Sert d'audit (quel envoi a déclenché la réponse).
	MatchedMessageID string
}

// VeridianExtractMessageIDLocalParts extrait les "local-parts" candidates des headers
// In-Reply-To et References d'un message entrant. La local-part d'un Message-ID au
// format `<local@domaine>` est ce que nous avons posé à l'envoi = notre
// message_history.id (un UUID). On retourne la liste dédupliquée et ordonnée (priorité
// à In-Reply-To, le plus direct, puis References du plus récent au plus ancien).
//
// On ne présume RIEN du domaine (un relai peut réécrire l'host) : seule la local-part
// est signifiante de notre côté. On tolère les IDs avec ou sans chevrons.
func VeridianExtractMessageIDLocalParts(inReplyTo string, references []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 1+len(references))

	add := func(raw string) {
		lp := veridianMessageIDLocalPart(raw)
		if lp == "" {
			return
		}
		if _, ok := seen[lp]; ok {
			return
		}
		seen[lp] = struct{}{}
		out = append(out, lp)
	}

	// In-Reply-To d'abord (le plus direct : c'est LE message auquel on répond).
	// Le header peut techniquement contenir plusieurs IDs ; on les sépare.
	for _, id := range strings.Fields(inReplyTo) {
		add(id)
	}
	// References ensuite (chaîne de threading, du plus ancien au plus récent en
	// principe ; on parcourt tel quel, la dédup gère le recoupement avec In-Reply-To).
	for _, ref := range references {
		for _, id := range strings.Fields(ref) {
			add(id)
		}
	}
	return out
}

// veridianMessageIDLocalPart normalise un Message-ID brut (`<local@host>`, `local@host`,
// ou `<local>`) en sa local-part nue (`local`). Retourne "" si rien d'exploitable.
func veridianMessageIDLocalPart(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "<>")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Tronquer à l'@ : la local-part est tout ce qui précède le premier '@'.
	if at := strings.IndexByte(s, '@'); at >= 0 {
		s = s[:at]
	}
	return strings.TrimSpace(s)
}

// Note : la décision "ce message est-il un NDR ?" n'est volontairement PAS dans ce
// fichier. Elle est déléguée au parseur canonique pkg/veridian_ndr (partagé avec le
// Lot 2 bounce-loop), appelé par le service stop-on-reply qui a accès au RawBody. Le
// parseur inspecte From + Subject ET le corps DSN (Final-Recipient, Diagnostic-Code) —
// plus fiable qu'une heuristique From/Subject, et ZÉRO divergence d'heuristique NDR
// entre les deux consumers du même poller (la règle d'or interdit de dupliquer cette
// logique).

// VeridianFromLooksLikeDaemon : garde-fou MINIMAL (pas une détection NDR) — l'expéditeur
// ressemble-t-il à un agent de remise (MAILER-DAEMON, postmaster, mail delivery system) ?
//
// Le verdict NDR complet est délégué à pkg/veridian_ndr (qui exige un corps DSN). Mais
// ce parseur retourne IsNDR=false si le RawBody est ABSENT (fetch IMAP tronqué). Sans ce
// garde-fou, un NDR sans corps dont l'In-Reply-To cite un de nos envois passerait le
// match FORT comme "réponse" — faux positif qui marquerait 'replied' une adresse en fait
// morte. On bloque donc tout expéditeur daemon AVANT le match fort. Ce n'est PAS une
// duplication de la logique DSN (juste un test de préfixe d'adresse), et ça reste
// cohérent avec les mailerDaemonHints de pkg/veridian_ndr.
func VeridianFromLooksLikeDaemon(from string) bool {
	f := strings.ToLower(strings.TrimSpace(from))
	if f == "" {
		// Pas d'expéditeur adressable = pas de réponse humaine possible.
		return true
	}
	for _, hint := range []string{
		"mailer-daemon", "postmaster", "mail delivery system",
		"mail delivery subsystem", "mdaemon", "internet mail delivery",
	} {
		if strings.Contains(f, hint) {
			return true
		}
	}
	return false
}

// VeridianNormalizeEmail normalise une adresse pour comparaison/clé : trim + lowercase,
// et extraction de l'adresse nue depuis un éventuel "Display Name <addr@host>". Ne
// valide pas le format (best-effort) ; retourne "" si rien d'exploitable.
func VeridianNormalizeEmail(raw string) string {
	s := strings.TrimSpace(raw)
	// "Display Name <addr@host>" -> "addr@host"
	if lt := strings.LastIndexByte(s, '<'); lt >= 0 {
		if gt := strings.IndexByte(s[lt:], '>'); gt >= 0 {
			s = s[lt+1 : lt+gt]
		}
	}
	return strings.ToLower(strings.TrimSpace(s))
}
