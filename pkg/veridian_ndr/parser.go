// Package veridian_ndr — parseur de NDR / DSN (Non-Delivery Report /
// Delivery Status Notification) pour la boucle de bounce cold outbound
// (Lot 2 sprint cold, 2026-06-15).
//
// Contexte : le cold part via un relai SMTP Postfix self-hosted. Les bounces
// ASYNCHRONES (non-remise constatée après coup) reviennent par mail dans la
// boîte du Return-Path du domaine d'envoi — Notifuse ne les voit pas via un
// webhook provider (il n'y en a pas). Le poller IMAP (Lot 1) rapatrie ces
// messages ; ce package détecte lesquels sont des NDR et en extrait :
//   - l'adresse morte (Final-Recipient / Original-Recipient),
//   - le code DSN (Status: 5.x.x = permanent/hard, 4.x.x = temporaire/soft),
//   - le diagnostic SMTP brut,
//   - le Message-ID original (si présent) pour mapper bounce -> message.
//
// Le résultat alimente la chaîne de suppression EXISTANTE de Notifuse
// (processSMTPWebhook -> ClassifyBounce -> MarkEmailsAsBounced). Ce package
// ne supprime rien lui-même : il PARSE, point.
//
// Robustesse : les NDR sont notoirement hétérogènes selon le MTA émetteur
// (Postfix, Google, Outlook/Exchange, Yahoo, qmail...). On combine donc deux
// stratégies, dans cet ordre :
//
//  1. MIME structuré RFC 3464 : multipart/report; report-type=delivery-status.
//     On lit la part message/delivery-status (champs Status, Action,
//     Final-Recipient, Diagnostic-Code) — la source la plus fiable.
//  2. Heuristiques de repli : From contenant MAILER-DAEMON / postmaster, sujet
//     "Undelivered Mail Returned to Sender" / "Delivery Status Notification" /
//     "Mail delivery failed", et scan du corps texte pour un code DSN
//     (5.x.x / 4.x.x) + une adresse e-mail.
//
// Un message qui n'est PAS un NDR est rendu proprement via Result{IsNDR:false}
// (jamais d'erreur) : le consumer l'ignore (ce peut être une vraie réponse de
// prospect, traitée par le Lot 3 stop-on-reply).
package veridian_ndr

import (
	"bytes"
	"io"
	"mime"
	"net/mail"
	"regexp"
	"strings"

	gomail "github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // enregistre les charsets non-UTF8
)

// Severity classe la gravité d'un bounce détecté.
type Severity string

const (
	// SeverityHard : échec permanent (code DSN 5.x.x). L'adresse est morte —
	// suppression immédiate du contact.
	SeverityHard Severity = "hard"
	// SeveritySoft : échec temporaire (code DSN 4.x.x). Mailbox pleine, greylist,
	// indisponibilité transitoire — compte vers le seuil soft, n'escalade pas seul.
	SeveritySoft Severity = "soft"
	// SeverityUnknown : NDR détecté mais code DSN absent/illisible. Traité comme
	// soft par prudence (on ne supprime jamais sur un signal ambigu unique).
	SeverityUnknown Severity = "unknown"
)

// Result est le verdict du parseur sur un message.
type Result struct {
	// IsNDR indique si le message est un rapport de non-remise.
	IsNDR bool
	// Recipient : l'adresse morte extraite (Final-Recipient/Original-Recipient
	// ou heuristique). Normalisée en minuscules, sans chevrons ni préfixe rfc822;.
	Recipient string
	// Severity : hard / soft / unknown selon le code DSN.
	Severity Severity
	// DSNCode : code statut enrichi RFC 3463 normalisé (ex "5.1.1"), si trouvé.
	DSNCode string
	// DiagnosticCode : ligne Diagnostic-Code brute (ex "smtp; 550 5.1.1 User unknown")
	// ou la ligne de réponse SMTP trouvée par heuristique. Utile pour l'audit.
	DiagnosticCode string
	// OriginalMessageID : Message-ID du mail original ayant bounce (si le NDR le
	// renvoie via la part message/rfc822 ou un header Original-Message-ID).
	OriginalMessageID string
}

// finalRecipientRe / originalRecipientRe / actionRe / statusRe / diagnosticRe
// matchent les champs du corps message/delivery-status (RFC 3464). On les
// applique aussi au corps brut en repli, car certains MTA ne posent pas un
// Content-Type structuré correct.
var (
	finalRecipientRe    = regexp.MustCompile(`(?im)^final-recipient:\s*(?:rfc822;)?\s*(.+?)\s*$`)
	originalRecipientRe = regexp.MustCompile(`(?im)^original-recipient:\s*(?:rfc822;)?\s*(.+?)\s*$`)
	statusRe            = regexp.MustCompile(`(?im)^status:\s*([245]\.\d{1,3}\.\d{1,3})\s*$`)
	diagnosticRe        = regexp.MustCompile(`(?im)^diagnostic-code:\s*(.+?)\s*$`)
	origMsgIDRe         = regexp.MustCompile(`(?im)^(?:original-message-id|message-id):\s*(.+?)\s*$`)

	// dsnInTextRe capture un code DSN enrichi 5.x.x / 4.x.x n'importe où dans le
	// texte (repli quand aucun champ Status: structuré n'existe).
	dsnInTextRe = regexp.MustCompile(`\b([45]\.\d{1,3}\.\d{1,3})\b`)
	// smtpReplyRe capture un code de réponse SMTP basique 5xx / 4xx (repli ultime
	// quand même le code enrichi manque, ex vieux qmail "550 ...").
	smtpReplyRe = regexp.MustCompile(`\b([45]\d{2})\b`)
	// emailRe extrait une adresse e-mail plausible (repli pour Final-Recipient).
	emailRe = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
)

// mailerDaemonHints : expéditeurs typiques d'un NDR.
var mailerDaemonHints = []string{
	"mailer-daemon", "postmaster", "mail delivery system",
	"mail delivery subsystem", "mdaemon", "internet mail delivery",
}

// subjectHints : sujets typiques d'un NDR (multi-langue light).
var subjectHints = []string{
	"undelivered mail returned to sender",
	"delivery status notification",
	"mail delivery failed",
	"returned mail",
	"delivery failure",
	"failure notice",
	"undeliverable",
	"échec de la remise", // FR
	"message non distribué",
}

// Parse inspecte un message brut (RFC822) et rend un Result. From et Subject
// peuvent être passés par le caller (déjà parsés par le poller IMAP) pour
// affiner la détection heuristique ; ils sont optionnels (si vides, on les
// relit depuis raw). Ne renvoie jamais d'erreur : un message illisible ou
// non-NDR donne Result{IsNDR:false}.
func Parse(raw []byte, from, subject string) Result {
	res := Result{Severity: SeverityUnknown}
	if len(raw) == 0 {
		// Sans corps on ne peut rien faire de fiable. On peut tout de même
		// flaguer NDR si le From/Subject crient MAILER-DAEMON, mais sans
		// destinataire extractible c'est inexploitable -> non-NDR.
		return res
	}

	entity, err := gomail.Read(bytes.NewReader(raw))
	if err != nil {
		// Parsing MIME impossible : on bascule full-heuristique sur le brut.
		return heuristicParse(raw, from, subject, res)
	}

	// Compléter From/Subject depuis les headers parsés si le caller ne les a pas
	// fournis.
	if strings.TrimSpace(from) == "" {
		from = entity.Header.Get("From")
	}
	if strings.TrimSpace(subject) == "" {
		subject, _ = decodeHeader(entity.Header.Get("Subject"))
	}

	mediaType, params := parseContentType(entity.Header.Get("Content-Type"))

	// Voie privilégiée : multipart/report; report-type=delivery-status.
	if strings.EqualFold(mediaType, "multipart/report") {
		if r, ok := parseDeliveryStatusReport(entity, params); ok {
			r.IsNDR = true
			finalizeSeverity(&r)
			if r.Recipient != "" {
				return r
			}
			// Report structuré mais sans recipient exploitable : on tente le repli
			// heuristique pour récupérer l'adresse, en gardant le code DSN trouvé.
			res = r
		}
	}

	// Repli : un NDR peut être un simple text/plain de MAILER-DAEMON sans
	// multipart/report propre, OU un corps que go-message a interprété comme des
	// headers (delivery-status inline sans ligne vide). On scanne donc le RAW
	// complet (headers + corps), augmenté du texte des parts décodées. Le scan
	// regex multiline tolère les headers mêlés au texte.
	body := extractAllText(entity)
	combined := string(raw)
	if body != "" {
		combined += "\n" + body
	}
	return heuristicParse([]byte(combined), from, subject, res)
}

// parseContentType découpe un header Content-Type en (mediaType, params).
func parseContentType(ct string) (string, map[string]string) {
	if ct == "" {
		return "", nil
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		// On récupère au moins la première portion avant ';'.
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			return strings.TrimSpace(strings.ToLower(ct[:i])), nil
		}
		return strings.TrimSpace(strings.ToLower(ct)), nil
	}
	return strings.ToLower(mt), params
}

// parseDeliveryStatusReport walke un multipart/report et lit la part
// message/delivery-status (champs RFC 3464) + la part message/rfc822 (pour
// l'Original-Message-ID). Retourne (Result, found).
func parseDeliveryStatusReport(entity *gomail.Entity, _ map[string]string) (Result, bool) {
	res := Result{Severity: SeverityUnknown}
	mr := entity.MultipartReader()
	if mr == nil {
		return res, false
	}

	found := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		pmt, _ := parseContentType(part.Header.Get("Content-Type"))
		switch {
		case strings.EqualFold(pmt, "message/delivery-status"):
			// Lecture bornée : les champs DSN (Status/Final-Recipient/Diagnostic)
			// tiennent très largement dans veridianNDRMaxPartBytes (anti-OOM sur
			// une part piégée surdimensionnée).
			b, _ := io.ReadAll(io.LimitReader(part.Body, veridianNDRMaxPartBytes))
			applyDeliveryStatusFields(&res, string(b))
			found = true
		case strings.EqualFold(pmt, "message/rfc822"),
			strings.EqualFold(pmt, "text/rfc822-headers"):
			b, _ := io.ReadAll(io.LimitReader(part.Body, veridianNDRMaxPartBytes))
			if id := extractOriginalMessageID(string(b)); id != "" && res.OriginalMessageID == "" {
				res.OriginalMessageID = id
			}
		default:
			// On draine la part pour avancer le reader proprement.
			_, _ = io.Copy(io.Discard, part.Body)
		}
	}
	return res, found
}

// applyDeliveryStatusFields lit les champs RFC 3464 d'un corps
// message/delivery-status et remplit res (recipient, code DSN, diagnostic).
func applyDeliveryStatusFields(res *Result, body string) {
	if m := statusRe.FindStringSubmatch(body); m != nil {
		res.DSNCode = m[1]
	}
	if m := diagnosticRe.FindStringSubmatch(body); m != nil {
		res.DiagnosticCode = strings.TrimSpace(m[1])
		// Si aucun Status: explicite, retomber sur le code DSN trouvé DANS le
		// diagnostic (ex "smtp; 550 5.1.1 ...").
		if res.DSNCode == "" {
			if dm := dsnInTextRe.FindStringSubmatch(res.DiagnosticCode); dm != nil {
				res.DSNCode = dm[1]
			}
		}
	}
	// Final-Recipient prioritaire, sinon Original-Recipient.
	if m := finalRecipientRe.FindStringSubmatch(body); m != nil {
		res.Recipient = normalizeRecipient(m[1])
	}
	if res.Recipient == "" {
		if m := originalRecipientRe.FindStringSubmatch(body); m != nil {
			res.Recipient = normalizeRecipient(m[1])
		}
	}
}

// heuristicParse est le repli quand le MIME structuré ne donne pas tout. Il
// décide IsNDR via From/Subject + présence d'un code DSN, et extrait recipient
// + sévérité du texte brut. Préserve les champs déjà remplis dans `seed`.
func heuristicParse(raw []byte, from, subject string, seed Result) Result {
	res := seed
	text := string(raw)
	lowerFrom := strings.ToLower(from)
	lowerSubject := strings.ToLower(subject)

	// Indices "c'est un NDR".
	fromIsDaemon := containsAny(lowerFrom, mailerDaemonHints)
	subjectIsBounce := containsAny(lowerSubject, subjectHints)

	// Compléter recipient depuis les champs DSN éventuellement présents dans le
	// texte brut (cas MTA qui inline le delivery-status sans Content-Type propre).
	if res.Recipient == "" {
		if m := finalRecipientRe.FindStringSubmatch(text); m != nil {
			res.Recipient = normalizeRecipient(m[1])
		} else if m := originalRecipientRe.FindStringSubmatch(text); m != nil {
			res.Recipient = normalizeRecipient(m[1])
		}
	}
	// Compléter le code DSN depuis Status: puis depuis le texte.
	if res.DSNCode == "" {
		if m := statusRe.FindStringSubmatch(text); m != nil {
			res.DSNCode = m[1]
		} else if m := dsnInTextRe.FindStringSubmatch(text); m != nil {
			res.DSNCode = m[1]
		}
	}
	if res.DiagnosticCode == "" {
		if m := diagnosticRe.FindStringSubmatch(text); m != nil {
			res.DiagnosticCode = strings.TrimSpace(m[1])
		} else if m := smtpReplyRe.FindStringSubmatch(text); m != nil {
			// Pas de ligne Diagnostic-Code: structurée, mais un code SMTP brut
			// (ex "421 ...", "550 ...") traîne dans le texte. On le conserve pour
			// que finalizeSeverity puisse en dériver hard/soft à défaut de code
			// enrichi.
			res.DiagnosticCode = m[1]
		}
	}
	if res.OriginalMessageID == "" {
		if id := extractOriginalMessageID(text); id != "" {
			res.OriginalMessageID = id
		}
	}

	hasDSN := res.DSNCode != ""
	// Repli ultime : code SMTP 5xx/4xx brut (sans code enrichi) compte comme
	// signal de bounce SI le contexte (From daemon ou sujet bounce) le confirme.
	hasRawSMTP := smtpReplyRe.MatchString(text)

	// Verdict NDR : il faut un signal "rapport de non-remise" ET une adresse
	// morte exploitable. Sans destinataire, le bounce est inexploitable pour la
	// suppression -> on ne flague pas NDR (évite les faux positifs sur de vraies
	// réponses qui citeraient un code au hasard).
	isNDR := res.IsNDR ||
		(fromIsDaemon && (hasDSN || hasRawSMTP)) ||
		(subjectIsBounce && (hasDSN || hasRawSMTP)) ||
		(fromIsDaemon && subjectIsBounce)

	if !isNDR && !res.IsNDR {
		// Pas un NDR (et le seed structuré ne l'avait pas déjà flagué) : Result
		// neutre. On ne downgrade jamais un seed déjà marqué NDR via la voie
		// multipart/report.
		return Result{Severity: SeverityUnknown}
	}

	// Si on a un recipient via DSN, on le garde. Sinon, dernier recours :
	// première adresse e-mail plausible du corps (hors adresses du daemon).
	if res.Recipient == "" {
		res.Recipient = firstPlausibleEmail(text, from)
	}

	// Sans destinataire exploitable, le NDR est inutile pour la suppression.
	if res.Recipient == "" {
		return Result{IsNDR: false, Severity: SeverityUnknown}
	}

	res.IsNDR = true
	finalizeSeverity(&res)
	return res
}

// finalizeSeverity dérive Severity de DSNCode (5.x -> hard, 4.x -> soft). À
// défaut de code enrichi, tente le code SMTP brut dans le diagnostic.
func finalizeSeverity(res *Result) {
	code := res.DSNCode
	if code == "" {
		// Repli sur le diagnostic (peut contenir "550 ..." sans code enrichi).
		if m := smtpReplyRe.FindStringSubmatch(res.DiagnosticCode); m != nil {
			switch m[1][0] {
			case '5':
				res.Severity = SeverityHard
				return
			case '4':
				res.Severity = SeveritySoft
				return
			}
		}
		res.Severity = SeverityUnknown
		return
	}
	switch code[0] {
	case '5':
		res.Severity = SeverityHard
	case '4':
		res.Severity = SeveritySoft
	default:
		res.Severity = SeverityUnknown
	}
}

// normalizeRecipient nettoie une valeur Final-Recipient : retire le préfixe
// "rfc822;", les chevrons, espaces, et met en minuscules. Si la valeur contient
// du bruit autour, on extrait l'adresse e-mail.
func normalizeRecipient(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.ToLower(v), "rfc822;")
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "<>")
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// Si la valeur n'est pas une adresse propre, tenter d'en extraire une.
	if !strings.Contains(v, "@") || strings.ContainsAny(v, " \t") {
		if m := emailRe.FindString(v); m != "" {
			return strings.ToLower(m)
		}
	}
	return strings.ToLower(v)
}

// envelopeHeaderRe matche les lignes d'en-tête qui portent une de NOS adresses
// d'enveloppe (la boîte de retour, l'expéditeur daemon, etc.), pas l'adresse
// morte. On exclut ces adresses du repli firstPlausibleEmail pour ne pas
// supprimer par erreur notre propre adresse de bounce.
var envelopeHeaderRe = regexp.MustCompile(`(?im)^(?:to|from|return-path|delivered-to|reply-to|sender|x-original-to|received|reporting-mta|cc):.*$`)

// firstPlausibleEmail retourne la première adresse e-mail du texte qui n'est pas
// une adresse d'enveloppe (To/From/Return-Path...), pas celle du daemon, et pas
// une adresse de service évidente. C'est le dernier recours quand aucun champ
// Final-Recipient structuré n'a été trouvé.
func firstPlausibleEmail(text, from string) string {
	lowerFrom := strings.ToLower(from)

	// Collecter les adresses présentes sur des lignes d'en-tête d'enveloppe : ce
	// sont les nôtres (boîte de retour, expéditeur), à exclure.
	envelope := map[string]struct{}{}
	for _, line := range envelopeHeaderRe.FindAllString(text, -1) {
		for _, e := range emailRe.FindAllString(line, -1) {
			envelope[strings.ToLower(e)] = struct{}{}
		}
	}

	for _, m := range emailRe.FindAllString(text, -1) {
		e := strings.ToLower(m)
		if _, isEnvelope := envelope[e]; isEnvelope {
			continue
		}
		if strings.Contains(lowerFrom, e) {
			continue
		}
		if strings.HasPrefix(e, "mailer-daemon@") || strings.HasPrefix(e, "postmaster@") {
			continue
		}
		return e
	}
	return ""
}

// extractOriginalMessageID lit un Message-ID depuis des headers rfc822 inline.
func extractOriginalMessageID(s string) string {
	if m := origMsgIDRe.FindStringSubmatch(s); m != nil {
		id := strings.TrimSpace(m[1])
		return strings.Trim(id, "<>")
	}
	return ""
}

// Garde-fous anti MIME-bomb (un NDR/mail piégé peut imbriquer du multipart à
// l'infini et/ou contenir des parts énormes) :
//   - veridianNDRMaxDepth borne la profondeur de récursion multipart. Un NDR
//     légitime ne dépasse JAMAIS 2-3 niveaux (report > delivery-status / rfc822).
//   - veridianNDRMaxTextBytes borne le total de texte accumulé. Le fetch IMAP
//     est déjà cappé (veridianIMAPMaxBodyBytes), ceci est la défense en
//     profondeur côté parseur (robustesse pour un appel direct hors poller).
const (
	veridianNDRMaxDepth     = 10
	veridianNDRMaxTextBytes = 4 * 1024 * 1024
	// veridianNDRMaxPartBytes borne la lecture d'UNE part DSN/rfc822 isolée
	// (les champs utiles tiennent dans 256 KiB ; au-delà = part piégée).
	veridianNDRMaxPartBytes = 256 * 1024
)

// extractAllText concatène le texte de toutes les parts d'un message MIME
// (sert au repli heuristique quand la structure report n'est pas exploitable).
// Borné en profondeur de récursion ET en taille totale (anti MIME-bomb).
func extractAllText(entity *gomail.Entity) string {
	var sb strings.Builder
	extractAllTextInto(entity, &sb, 0)
	return sb.String()
}

// extractAllTextInto fait le travail récursif en accumulant dans sb, avec un
// compteur de profondeur (depth) et un plafond de taille (len(sb)). Best-effort :
// une fois un plafond atteint, on s'arrête proprement (le texte déjà collecté
// suffit aux heuristiques NDR — l'adresse morte et le code DSN sont en tête).
func extractAllTextInto(entity *gomail.Entity, sb *strings.Builder, depth int) {
	if entity == nil || depth > veridianNDRMaxDepth || sb.Len() >= veridianNDRMaxTextBytes {
		return
	}
	mr := entity.MultipartReader()
	if mr == nil {
		// Lecture bornée au reliquat de budget (jamais d'io.ReadAll nu).
		b, _ := io.ReadAll(io.LimitReader(entity.Body, veridianNDRRemaining(sb)))
		sb.Write(b)
		return
	}
	for {
		if sb.Len() >= veridianNDRMaxTextBytes {
			return
		}
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		b, _ := io.ReadAll(io.LimitReader(part.Body, veridianNDRRemaining(sb)))
		sb.Write(b)
		sb.WriteByte('\n')
		// Récursion bornée : une part peut être elle-même multipart (report
		// imbriqué). go-message expose ça via Entity sur la part.
		pmt, _ := parseContentType(part.Header.Get("Content-Type"))
		if strings.HasPrefix(pmt, "multipart/") {
			extractAllTextInto(part, sb, depth+1)
			sb.WriteByte('\n')
		}
	}
}

// veridianNDRRemaining = budget de lecture restant avant le plafond total.
// Toujours >= 0 (le caller vérifie déjà sb.Len() < max avant de lire).
func veridianNDRRemaining(sb *strings.Builder) int64 {
	rem := veridianNDRMaxTextBytes - sb.Len()
	if rem < 0 {
		return 0
	}
	return int64(rem)
}

// decodeHeader décode un header MIME encodé (=?utf-8?...?=). Best-effort.
func decodeHeader(v string) (string, error) {
	dec := new(mime.WordDecoder)
	out, err := dec.DecodeHeader(v)
	if err != nil {
		return v, err
	}
	return out, nil
}

// containsAny indique si s contient l'un des substrings.
func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ParseAddressList est un utilitaire exposé pour les callers qui veulent
// normaliser une liste d'adresses (ex From multiple). Best-effort.
func ParseAddressList(v string) []string {
	addrs, err := mail.ParseAddressList(v)
	if err != nil || len(addrs) == 0 {
		if e := emailRe.FindString(v); e != "" {
			return []string{strings.ToLower(e)}
		}
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, strings.ToLower(a.Address))
	}
	return out
}
