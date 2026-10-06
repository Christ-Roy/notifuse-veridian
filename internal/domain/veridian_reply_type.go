package domain

// Veridian fork, type de reponse (2026-10-06).
//
// Constat : sur la campagne du 28/09, le dossier de reponses melangeait 5 reponses
// humaines, 5 messages automatiques (absence, accuse, filtre ou defi anti-spam), des
// rejets et des tests. Le stop-on-reply traitait tout message cite comme une reponse
// et sortait de sequence des prospects qui n'avaient rien repondu (un auto-repondeur,
// un filtre anti-spam, un defi Mailinblack). Une reponse n'arrete la cadence que si
// c'est un humain qui a repondu.
//
// Le type est pose dans veridian_contact_reply.reply_type (migration V60) :
//
//	human     un humain a repondu : seul type qui sort le contact de sequence
//	auto      auto-repondeur (Auto-Submitted, Precedence, sujet "reponse automatique")
//	challenge filtre ou defi anti-spam (passerelle xefi, Mailinblack, captcha)
//
// La classification est pure (aucune I/O) et lit les en-tetes du RawBody recu par le
// poller IMAP (le FETCH ramene le message RFC822 borne, en-tetes compris).

import (
	"bufio"
	"bytes"
	"net/textproto"
	"regexp"
	"strings"
)

// VeridianReplyType qualifie QUI a repondu.
type VeridianReplyType string

const (
	VeridianReplyTypeHuman     VeridianReplyType = "human"
	VeridianReplyTypeAuto      VeridianReplyType = "auto"
	VeridianReplyTypeChallenge VeridianReplyType = "challenge"
)

// veridianReplyBodyScanBytes borne la partie du corps scrutee pour les defis anti-spam.
const veridianReplyBodyScanBytes = 4000

var (
	// veridianAutoSubjectRe : sujets d'auto-repondeurs (FR/EN).
	veridianAutoSubjectRe = regexp.MustCompile(`(?i)^\s*(r[eé]ponse automatique|automatic reply|auto-?reply|absence|out of office|accus[eé] de r[eé]ception|cong[eé]s)\b`)
	// veridianChallengeSenderRe : passerelles anti-spam dont l'expediteur est le filtre.
	veridianChallengeSenderRe = regexp.MustCompile(`(?i)(^|\.)(antispam\d*\.xefi\.fr|invitations\.mailinblack\.com)$`)
	// veridianChallengeBodyRe : defi ou filtre anti-spam repere dans le debut du corps.
	veridianChallengeBodyRe = regexp.MustCompile(`(?i)mailinblack|anti-?spam|captcha`)
)

// VeridianParseRawHeaders lit les en-tetes d'un message RFC822 brut (eventuellement
// tronque). Best-effort : en cas d'erreur de lecture on retourne les en-tetes deja
// lus (jamais nil), un message sans en-tete exploitable donne une map vide.
func VeridianParseRawHeaders(raw []byte) textproto.MIMEHeader {
	if len(raw) == 0 {
		return textproto.MIMEHeader{}
	}
	hdr, _ := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw))).ReadMIMEHeader()
	if hdr == nil {
		return textproto.MIMEHeader{}
	}
	return hdr
}

// VeridianReferencesFromRaw retourne les valeurs du header References du message brut
// (une entree par header), prete pour VeridianExtractMessageIDLocalParts.
func VeridianReferencesFromRaw(raw []byte) []string {
	var out []string
	for _, v := range VeridianParseRawHeaders(raw).Values("References") {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// VeridianClassifyReplyType decide si un message entrant est une reponse humaine, un
// auto-repondeur ou un defi/filtre anti-spam. Sans signal contraire : humain.
func VeridianClassifyReplyType(msg *VeridianIMAPMessage) VeridianReplyType {
	if msg == nil {
		return VeridianReplyTypeHuman
	}
	from := VeridianNormalizeEmail(msg.From)
	domainPart := ""
	if at := strings.LastIndexByte(from, '@'); at >= 0 {
		domainPart = from[at+1:]
	}
	if domainPart != "" && veridianChallengeSenderRe.MatchString(domainPart) {
		return VeridianReplyTypeChallenge
	}

	hdr := VeridianParseRawHeaders(msg.RawBody)
	if v := strings.ToLower(strings.TrimSpace(hdr.Get("Auto-Submitted"))); v != "" && v != "no" {
		return VeridianReplyTypeAuto
	}
	switch strings.ToLower(strings.TrimSpace(hdr.Get("Precedence"))) {
	case "bulk", "auto_reply", "auto-reply", "junk", "list":
		return VeridianReplyTypeAuto
	}
	if hdr.Get("X-Autoreply") != "" || hdr.Get("X-Autorespond") != "" || hdr.Get("X-Auto-Response-Suppress") != "" {
		return VeridianReplyTypeAuto
	}
	if veridianAutoSubjectRe.MatchString(msg.Subject) {
		return VeridianReplyTypeAuto
	}

	// Defi / filtre anti-spam sans en-tete automatique : on regarde le debut du corps
	// (apres les en-tetes). Les reponses humaines n'en contiennent jamais.
	if body := veridianBodyAfterHeaders(msg.RawBody); body != "" && veridianChallengeBodyRe.MatchString(body) {
		return VeridianReplyTypeChallenge
	}
	return VeridianReplyTypeHuman
}

// veridianBodyAfterHeaders retourne le debut (borne) du corps apres la ligne vide qui
// termine les en-tetes, en minuscules inutiles a normaliser (la regexp est insensible
// a la casse). Corps absent ou message sans separateur : "".
func veridianBodyAfterHeaders(raw []byte) string {
	for _, sep := range []string{"\r\n\r\n", "\n\n"} {
		if i := bytes.Index(raw, []byte(sep)); i >= 0 {
			body := raw[i+len(sep):]
			if len(body) > veridianReplyBodyScanBytes {
				body = body[:veridianReplyBodyScanBytes]
			}
			return string(body)
		}
	}
	return ""
}
