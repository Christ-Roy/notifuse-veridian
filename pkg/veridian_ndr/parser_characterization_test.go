package veridian_ndr

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	gomail "github.com/emersion/go-message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests de caractérisation du parseur de NDR (lot L4, 29/10/2026).
//
// Un NDR classé « hard » supprime un contact : chaque branche du verdict compte.
// Écrits à partir des mutants qui survivaient aux tests d'origine (comparaisons
// inversées, repli de recipient, ordre des parts MIME, texte encodé non lu).
// Chaque test ci-dessous tue au moins un mutant listé dans docs/claude/75.

func crlf(lines ...string) string { return strings.Join(lines, "\r\n") + "\r\n" }

// report assemble un multipart/report avec les parts données (déjà complètes, entêtes + corps).
func report(from, subject string, parts ...string) []byte {
	var sb strings.Builder
	sb.WriteString(crlf(
		"From: "+from,
		"To: bounce@agences-veridian.fr",
		"Subject: "+subject,
		"MIME-Version: 1.0",
		`Content-Type: multipart/report; report-type=delivery-status; boundary="BND"`,
		""))
	for _, p := range parts {
		sb.WriteString("--BND\r\n" + p + "\r\n")
	}
	sb.WriteString("--BND--\r\n")
	return []byte(sb.String())
}

func textPart(body string) string {
	return crlf("Content-Type: text/plain; charset=us-ascii", "") + body + "\r\n"
}

func dsPart(fields ...string) string {
	return crlf("Content-Type: message/delivery-status", "", "Reporting-MTA: dns; mx.example", "") + crlf(fields...)
}

func rfc822Part(kind, messageID string) string {
	return crlf("Content-Type: "+kind, "", "Message-ID: <"+messageID+">", "Subject: x")
}

// --- from / subject : le caller prime, sinon on relit les en-têtes ---

func TestParse_CallerFromPrimeSurLEntete(t *testing.T) {
	// En-tête From = un prospect ; le poller IMAP, lui, a identifié MAILER-DAEMON.
	raw := crlf("From: prospect@client.fr", "Subject: Re: offre", "", "Erreur 550 5.1.1 pour <mort@dead.example>")
	res := Parse([]byte(raw), "MAILER-DAEMON@relais.example", "")
	assert.True(t, res.IsNDR, "From fourni par le caller doit servir, pas l'en-tête")
	assert.Equal(t, "mort@dead.example", res.Recipient)
}

func TestParse_EnteteFromRelueQuandCallerVide(t *testing.T) {
	raw := crlf("From: MAILER-DAEMON@relais.example", "Subject: Re: offre", "", "Erreur 550 5.1.1 pour <mort@dead.example>")
	res := Parse([]byte(raw), "", "")
	assert.True(t, res.IsNDR, "From vide côté caller : l'en-tête du message fait foi")
	assert.Equal(t, SeverityHard, res.Severity)
}

func TestParse_EnteteSubjectRelueQuandCallerVide(t *testing.T) {
	raw := crlf("From: quelquun@relais.example", "Subject: Undelivered Mail Returned to Sender", "", "Erreur 550 5.1.1 pour <mort@dead.example>")
	res := Parse([]byte(raw), "", "")
	assert.True(t, res.IsNDR)
	res2 := Parse([]byte(raw), "", "Re: offre")
	assert.False(t, res2.IsNDR, "le sujet fourni par le caller prime sur l'en-tête")
}

// --- multipart/report : la voie structurée ---

func TestParse_ReportSansRecipient_RecupereLAdresseDansLeTexte(t *testing.T) {
	raw := report("MAILER-DAEMON@mx.example", "Undelivered Mail Returned to Sender",
		textPart("Votre message pour <bob@morte.example> n'a pas pu etre remis."),
		dsPart("Action: failed", "Status: 5.1.1"))
	res := Parse(raw, "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "bob@morte.example", res.Recipient, "report sans Final-Recipient : repli sur le texte, pas un NDR sans adresse")
	assert.Equal(t, "5.1.1", res.DSNCode, "le code du report structuré est conservé")
	assert.Equal(t, SeverityHard, res.Severity)
}

func TestParse_ReportParsParDeLaDeuxiemePart_SansIndiceHeuristique(t *testing.T) {
	// Expéditeur et sujet neutres : seule la lecture de TOUTES les parts, dont la
	// seconde (delivery-status), permet de conclure NDR.
	raw := report("agent@relais.example", "Votre message",
		textPart("Bonjour."),
		dsPart("Final-Recipient: rfc822; cible@dead.example", "Action: failed", "Status: 5.1.1"))
	res := Parse(raw, "", "")
	require.True(t, res.IsNDR, "multipart/report delivery-status = NDR même sans indice From/Sujet")
	assert.Equal(t, "cible@dead.example", res.Recipient)
	assert.Equal(t, SeverityHard, res.Severity)
}

func TestParse_StatusExplicitPrimeSurLeCodeDuDiagnostic(t *testing.T) {
	raw := report("MAILER-DAEMON@mx.example", "Delivery Status Notification",
		dsPart("Final-Recipient: rfc822; plein@dead.example", "Action: delayed",
			"Status: 4.2.2", "Diagnostic-Code: smtp; 550 5.1.1 User unknown"))
	res := Parse(raw, "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "4.2.2", res.DSNCode, "Status: explicite l'emporte sur le code cité dans le diagnostic")
	assert.Equal(t, SeveritySoft, res.Severity)
}

func TestParse_SansStatus_CodeDuDiagnostic(t *testing.T) {
	raw := report("MAILER-DAEMON@mx.example", "Delivery Status Notification",
		dsPart("Final-Recipient: rfc822; x@dead.example", "Diagnostic-Code: smtp; 550 5.7.1 refused"))
	res := Parse(raw, "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "5.7.1", res.DSNCode)
	assert.Equal(t, SeverityHard, res.Severity)
}

func TestParse_FinalRecipientPrimeSurOriginalRecipient(t *testing.T) {
	raw := report("MAILER-DAEMON@mx.example", "Delivery Status Notification",
		dsPart("Original-Recipient: rfc822; alias@dead.example", "Final-Recipient: rfc822; reel@dead.example", "Status: 5.1.1"))
	res := Parse(raw, "", "")
	assert.Equal(t, "reel@dead.example", res.Recipient)
}

func TestParse_OriginalRecipientSeulUtilise(t *testing.T) {
	raw := report("MAILER-DAEMON@mx.example", "Delivery Status Notification",
		dsPart("Original-Recipient: rfc822; alias@dead.example", "Status: 5.1.1"))
	res := Parse(raw, "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "alias@dead.example", res.Recipient)
}

func TestParse_PremierMessageIDOriginalGagne(t *testing.T) {
	raw := report("MAILER-DAEMON@mx.example", "Delivery Status Notification",
		dsPart("Final-Recipient: rfc822; cible@dead.example", "Status: 5.1.1"),
		rfc822Part("message/rfc822", "premier@veridian"),
		rfc822Part("text/rfc822-headers", "second@veridian"))
	res := Parse(raw, "", "")
	assert.Equal(t, "premier@veridian", res.OriginalMessageID)
}

// --- repli heuristique : décisions de verdict ---

func TestParse_VerdictNDR_TableDesCombinaisons(t *testing.T) {
	const addr = "<mort@dead.example>"
	cases := []struct {
		name    string
		from    string
		subject string
		body    string
		isNDR   bool
		sev     Severity
	}{
		{"daemon + code enrichi", "MAILER-DAEMON@mx.example", "Re: x", addr + " 550 5.1.1 inconnu", true, SeverityHard},
		{"daemon + code SMTP brut", "MAILER-DAEMON@mx.example", "Re: x", addr + " 550 inconnu", true, SeverityHard},
		{"sujet bounce + code enrichi", "quelquun@mx.example", "Undelivered Mail Returned to Sender", addr + " 452 4.2.2 plein", true, SeveritySoft},
		{"sujet bounce + code SMTP brut", "quelquun@mx.example", "Mail delivery failed", addr + " 421 occupe", true, SeveritySoft},
		{"daemon + sujet bounce, sans code", "MAILER-DAEMON@mx.example", "Delivery Status Notification", addr + " n'existe pas", true, SeverityUnknown},
		{"daemon seul", "MAILER-DAEMON@mx.example", "Re: x", "Merci, bien reçu " + addr, false, SeverityUnknown},
		{"sujet bounce seul", "quelquun@mx.example", "Undeliverable", "Merci, bien reçu " + addr, false, SeverityUnknown},
		{"code seul, expéditeur et sujet neutres (vraie réponse citant un code)", "client@mx.example", "Re: devis", "Voir le code 550 5.1.1 de votre serveur " + addr, false, SeverityUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Parse([]byte(crlf("From: "+c.from, "Subject: "+c.subject, "", c.body)), "", "")
			assert.Equal(t, c.isNDR, res.IsNDR)
			if c.isNDR {
				assert.Equal(t, "mort@dead.example", res.Recipient)
				assert.Equal(t, c.sev, res.Severity)
			} else {
				assert.Empty(t, res.Recipient, "un non-NDR ne rend jamais d'adresse à supprimer")
			}
		})
	}
}

func TestParse_Heuristique_FinalRecipientPrimeSurUneAdresseDecor(t *testing.T) {
	// Une adresse de décor précède le champ structuré : le champ gagne.
	raw := crlf("From: MAILER-DAEMON@mx.example", "Subject: Delivery Status Notification", "",
		"Aide : support@helpdesk.example", "Final-Recipient: rfc822; vrai@dead.example", "Status: 5.0.0")
	res := Parse([]byte(raw), "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "vrai@dead.example", res.Recipient)
}

func TestParse_Heuristique_OriginalRecipientSiPasDeFinal(t *testing.T) {
	raw := crlf("From: MAILER-DAEMON@mx.example", "Subject: Delivery Status Notification", "",
		"Aide : support@helpdesk.example", "Original-Recipient: rfc822; alias@dead.example", "Status: 5.0.0")
	res := Parse([]byte(raw), "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "alias@dead.example", res.Recipient)
}

func TestParse_Heuristique_MessageIDOriginalDuCorps(t *testing.T) {
	raw := crlf("From: MAILER-DAEMON@mx.example", "Subject: Delivery Status Notification", "",
		"Final-Recipient: rfc822; cible@dead.example", "Status: 5.1.1", "Original-Message-ID: <origine@veridian>")
	res := Parse([]byte(raw), "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "origine@veridian", res.OriginalMessageID)
}

func TestParse_Heuristique_AdresseEnveloppeJamaisChoisieCommeMorte(t *testing.T) {
	// Notre propre adresse de retour et celle du daemon ne doivent jamais être
	// prises pour l'adresse à supprimer.
	raw := crlf("From: MAILER-DAEMON@mx.example", "To: bounce@agences-veridian.fr", "Return-Path: <>",
		"Subject: failure notice", "", "Erreur 550 pour <vraie@dead.example>")
	res := Parse([]byte(raw), "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "vraie@dead.example", res.Recipient)
}

// --- parseContentType ---

func TestParseContentType_Cas(t *testing.T) {
	mt, p := parseContentType("")
	assert.Equal(t, "", mt)
	assert.Nil(t, p)

	mt, p = parseContentType("TEXT/Plain; charset=UTF-8")
	assert.Equal(t, "text/plain", mt)
	assert.Equal(t, "UTF-8", p["charset"])

	// Illisible pour mime.ParseMediaType : on garde la partie avant « ; ».
	mt, p = parseContentType(`Multipart/Report; boundary="non fermé`)
	assert.Equal(t, "multipart/report", mt)
	assert.Nil(t, p)

	// Point-virgule en tête : rien avant, donc type vide (et non la chaîne brute).
	mt, _ = parseContentType(";charset=x")
	assert.Equal(t, "", mt)

	// Sans point-virgule et illisible : la valeur entière, nettoyée.
	mt, _ = parseContentType("  Bizarre Chose  ")
	assert.Equal(t, "bizarre chose", mt)
}

// --- texte décodé des parts (repli) ---

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestParse_TexteEncodeBase64DansUneSecondePart(t *testing.T) {
	// Le brut ne contient que du base64 : adresse et code ne sont lisibles qu'après
	// décodage de la seconde part.
	raw := crlf(
		"From: MAILER-DAEMON@mx.example",
		"Subject: Mail delivery failed",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="MIX"`,
		"",
		"--MIX",
		"Content-Type: text/plain",
		"",
		"Bonjour.",
		"--MIX",
		"Content-Type: text/plain",
		"Content-Transfer-Encoding: base64",
		"",
		b64("<cache@dead.example>: 550 5.1.1 user unknown"),
		"--MIX--")
	res := Parse([]byte(raw), "", "")
	require.True(t, res.IsNDR, "le texte décodé de la seconde part doit être lu")
	assert.Equal(t, "cache@dead.example", res.Recipient)
	assert.Equal(t, SeverityHard, res.Severity)
}

func TestExtractAllText_RendLeTexteDesParts(t *testing.T) {
	e, err := gomail.Read(strings.NewReader(crlf(
		"MIME-Version: 1.0", `Content-Type: multipart/mixed; boundary="B"`, "",
		"--B", "Content-Type: text/plain", "", "premier", "--B", "Content-Type: text/plain", "", "second", "--B--")))
	require.NoError(t, err)
	out := extractAllText(e)
	assert.Contains(t, out, "premier")
	assert.Contains(t, out, "second", "toutes les parts sont lues, pas seulement la première")
}

// nested construit n niveaux de multipart imbriqués ; le niveau k porte le marqueur "NIVEAU<k>".
func nested(n int) string {
	var open, closeS strings.Builder
	for k := 0; k <= n; k++ {
		fmt.Fprintf(&open, "Content-Type: multipart/mixed; boundary=\"B%d\"\r\n\r\n--B%d\r\nContent-Type: text/plain\r\n\r\nNIVEAU%d\r\n--B%d\r\n", k, k, k, k)
	}
	open.WriteString("Content-Type: text/plain\r\n\r\nFOND\r\n")
	for k := n; k >= 0; k-- {
		fmt.Fprintf(&closeS, "--B%d--\r\n", k)
	}
	return "MIME-Version: 1.0\r\n" + open.String() + closeS.String()
}

func TestExtractAllText_TexteDesPartsImbriqueesEstLu(t *testing.T) {
	// Constat (29/10) : la part multipart imbriquée est lue en entier (corps brut, frontières
	// comprises) AVANT la récursion ; la récursion qui suit lit alors un corps vidé. Le texte
	// imbriqué reste donc accessible aux heuristiques sans dépendre de la limite de profondeur.
	// Ce test fige ce comportement : si la récursion devient effective, il doit rester vrai.
	e, err := gomail.Read(strings.NewReader(nested(5)))
	require.NoError(t, err)
	out := extractAllText(e)
	for k := 0; k <= 5; k++ {
		assert.Contains(t, out, fmt.Sprintf("NIVEAU%d", k))
	}
}

func TestExtractAllText_EntiteNilEtPlafondDeTaille(t *testing.T) {
	assert.Equal(t, "", extractAllText(nil))
	assert.Equal(t, int64(0), veridianNDRRemaining(fullBuilder()))
	assert.Equal(t, int64(veridianNDRMaxTextBytes), veridianNDRRemaining(&strings.Builder{}))
}

func fullBuilder() *strings.Builder {
	var sb strings.Builder
	sb.Grow(veridianNDRMaxTextBytes + 1)
	sb.WriteString(strings.Repeat("x", veridianNDRMaxTextBytes+1))
	return &sb
}
