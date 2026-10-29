package mailbox

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/veridian_ndr"
)

// Verdict est le résultat de la classification d'un mail entrant.
type Verdict struct {
	Kind Kind
	DSN  *DSN
	// Recipient : pour un rebond, l'adresse en échec (le contact concerné), sinon "".
	Recipient string
}

// Classify range un mail entrant : rebond (dur ou temporaire), rapport de livraison,
// réponse humaine, réponse automatique, ou autre. linked dit que le mail se rattache à un
// de nos envois (référence trouvée, ou expéditeur connu) : sans rattachement, un mail
// n'est jamais une « réponse » (pas de stop-on-reply sur un courrier sans rapport), mais
// un rebond reste un rebond.
func Classify(p *Parsed, raw []byte, linked bool) Verdict {
	ndr := veridian_ndr.Parse(raw, p.From, p.Subject)
	if ndr.IsNDR {
		d := DecodeDSN(ndr, p.DeliveryStatus, p.Text)
		v := Verdict{DSN: d, Recipient: domain.VeridianNormalizeEmail(ndr.Recipient)}
		switch {
		case d.Action == "delivered" || d.Action == "relayed" || d.Action == "expanded" || d.Action == "delayed":
			v.Kind = KindDSN
		case d.Severity == string(veridian_ndr.SeverityHard):
			v.Kind = KindBounceHard
		default:
			v.Kind = KindBounceSoft
		}
		return v
	}
	if domain.VeridianFromLooksLikeDaemon(p.From) {
		return Verdict{Kind: KindOther}
	}
	if !linked {
		return Verdict{Kind: KindOther}
	}
	rt := domain.VeridianClassifyReplyType(&domain.VeridianIMAPMessage{From: p.From, Subject: p.Subject, RawBody: raw})
	if rt == domain.VeridianReplyTypeHuman || rt == "" {
		return Verdict{Kind: KindReplyHuman}
	}
	return Verdict{Kind: KindReplyAuto}
}

// ---- fils de conversation ----

var subjectPrefixRe = regexp.MustCompile(`(?i)^\s*((re|fw|fwd|tr|réf|ref|aw|wg|sv|vs)\s*(\[\d+\])?\s*:\s*)+`)

// NormalizeSubject retire les préfixes de réponse et de transfert (Re:, TR:, Fwd:...) et la casse.
func NormalizeSubject(s string) string {
	s = subjectPrefixRe.ReplaceAllString(s, "")
	return strings.ToLower(cleanLine(s))
}

func hash16(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])[:16]
}

// ThreadIDFromRoot : identifiant de fil dérivé d'un Message-ID racine (déterministe, sans base).
func ThreadIDFromRoot(rootMessageID string) string {
	return "t_" + hash16("root", strings.ToLower(LocalPart(rootMessageID)))
}

// ThreadIDFromSubject : identifiant de fil de repli, contact + objet normalisé.
func ThreadIDFromSubject(contactEmail, subject string) string {
	return "t_" + hash16("subject", strings.ToLower(strings.TrimSpace(contactEmail)), NormalizeSubject(subject))
}

// DeterministicThreadID calcule le fil d'un message sans consulter la base : la racine est
// la première référence, sinon In-Reply-To, sinon le message lui-même (s'il a un Message-ID),
// sinon contact + objet. Les relances de séquence portent References = chaîne depuis le
// premier mail : tous les mails d'une séquence aboutissent au même fil.
// (Le rattachement par la base, plus fiable quand un client coupe References, est fait par
// l'ingestion avant ce repli.)
func DeterministicThreadID(messageID, inReplyTo string, references []string, contactEmail, subject string) string {
	for _, r := range references {
		if LocalPart(r) != "" {
			return ThreadIDFromRoot(r)
		}
	}
	if LocalPart(inReplyTo) != "" {
		return ThreadIDFromRoot(inReplyTo)
	}
	if LocalPart(messageID) != "" {
		return ThreadIDFromRoot(messageID)
	}
	return ThreadIDFromSubject(contactEmail, subject)
}

// LocalPart donne la partie locale d'un Message-ID (sans chevrons, avant le premier @).
func LocalPart(id string) string {
	s := strings.Trim(strings.TrimSpace(id), "<>")
	if at := strings.IndexByte(s, '@'); at >= 0 {
		s = s[:at]
	}
	return strings.TrimSpace(s)
}

// CandidateIDs liste les identifiants que cite un message (In-Reply-To puis References,
// du plus récent au plus ancien), sans doublon, sous la forme avec chevrons.
func CandidateIDs(inReplyTo string, references []string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(id string) {
		id = normalizeMsgID(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, f := range strings.Fields(inReplyTo) {
		add(f)
	}
	var flat []string
	for _, r := range references {
		flat = append(flat, strings.Fields(r)...)
	}
	for i := len(flat) - 1; i >= 0; i-- {
		add(flat[i])
	}
	return out
}

// StripReplyPrefix retire les préfixes Re:/TR:/Fwd: d'un objet en gardant sa casse.
func StripReplyPrefix(s string) string {
	return strings.TrimSpace(subjectPrefixRe.ReplaceAllString(s, ""))
}
