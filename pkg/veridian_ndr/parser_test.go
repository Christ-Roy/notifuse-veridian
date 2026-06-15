package veridian_ndr

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixtures NDR réalistes inspirées des formats réels des MTA majeurs. Les CRLF
// sont importants pour le parsing MIME : on les pose explicitement.

// postfixHardNDR : NDR Postfix classique (multipart/report; delivery-status)
// avec 550 5.1.1 (utilisateur inconnu). Format le plus courant pour notre
// relai self-hosted.
const postfixHardNDR = "From: MAILER-DAEMON@mail.agences-veridian.fr (Mail Delivery System)\r\n" +
	"To: bounce@agences-veridian.fr\r\n" +
	"Subject: Undelivered Mail Returned to Sender\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/report; report-type=delivery-status;\r\n" +
	"\tboundary=\"ABCD1234\"\r\n" +
	"\r\n" +
	"--ABCD1234\r\n" +
	"Content-Type: text/plain; charset=us-ascii\r\n" +
	"\r\n" +
	"This is the mail system at host mail.agences-veridian.fr.\r\n" +
	"I'm sorry to have to inform you that your message could not be delivered.\r\n" +
	"\r\n" +
	"--ABCD1234\r\n" +
	"Content-Type: message/delivery-status\r\n" +
	"\r\n" +
	"Reporting-MTA: dns; mail.agences-veridian.fr\r\n" +
	"\r\n" +
	"Final-Recipient: rfc822; jean.mort@exemple-client.fr\r\n" +
	"Original-Recipient: rfc822;jean.mort@exemple-client.fr\r\n" +
	"Action: failed\r\n" +
	"Status: 5.1.1\r\n" +
	"Diagnostic-Code: smtp; 550 5.1.1 <jean.mort@exemple-client.fr>: Recipient address rejected: User unknown\r\n" +
	"\r\n" +
	"--ABCD1234\r\n" +
	"Content-Type: message/rfc822\r\n" +
	"\r\n" +
	"Message-ID: <orig-msg-42@agences-veridian.fr>\r\n" +
	"Subject: Votre site web\r\n" +
	"\r\n" +
	"--ABCD1234--\r\n"

// gmailHardNDR : NDR Gmail (host google.com), sujet "Delivery Status
// Notification (Failure)", 550 5.1.1.
const gmailHardNDR = "From: Mail Delivery Subsystem <mailer-daemon@googlemail.com>\r\n" +
	"To: bounce@agences-veridian.fr\r\n" +
	"Subject: Delivery Status Notification (Failure)\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/report; report-type=delivery-status;\r\n" +
	" boundary=\"000GMAIL000\"\r\n" +
	"\r\n" +
	"--000GMAIL000\r\n" +
	"Content-Type: text/plain; charset=UTF-8\r\n" +
	"\r\n" +
	"Address not found. Your message wasn't delivered to morte@gmail.com.\r\n" +
	"\r\n" +
	"--000GMAIL000\r\n" +
	"Content-Type: message/delivery-status\r\n" +
	"\r\n" +
	"Reporting-MTA: dns; googlemail.com\r\n" +
	"\r\n" +
	"Final-Recipient: rfc822; morte@gmail.com\r\n" +
	"Action: failed\r\n" +
	"Status: 5.1.1\r\n" +
	"Diagnostic-Code: smtp; 550-5.1.1 The email account that you tried to reach does not exist.\r\n" +
	"\r\n" +
	"--000GMAIL000--\r\n"

// outlookSoftNDR : NDR Exchange/Outlook avec 4.2.2 (mailbox full) — soft.
const outlookSoftNDR = "From: postmaster@outlook.com\r\n" +
	"To: bounce@agences-veridian.fr\r\n" +
	"Subject: Undeliverable: Votre devis\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/report; report-type=delivery-status;\r\n" +
	" boundary=\"OUTLOOK_BOUND\"\r\n" +
	"\r\n" +
	"--OUTLOOK_BOUND\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"Delivery has failed to these recipients or groups:\r\n" +
	"plein@contoso.com\r\n" +
	"The recipient's mailbox is full.\r\n" +
	"\r\n" +
	"--OUTLOOK_BOUND\r\n" +
	"Content-Type: message/delivery-status\r\n" +
	"\r\n" +
	"Final-Recipient: rfc822;plein@contoso.com\r\n" +
	"Action: failed\r\n" +
	"Status: 4.2.2\r\n" +
	"Diagnostic-Code: smtp;452 4.2.2 The email account that the user is trying to reach is over quota\r\n" +
	"\r\n" +
	"--OUTLOOK_BOUND--\r\n"

// plainTextHardNDR : NDR sans multipart/report propre (text/plain de
// MAILER-DAEMON). Doit passer par l'heuristique.
const plainTextHardNDR = "From: Mail Delivery System <MAILER-DAEMON@mail.agences-veridian.fr>\r\n" +
	"To: bounce@agences-veridian.fr\r\n" +
	"Subject: Mail delivery failed: returning message to sender\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"This message was created automatically by mail delivery software.\r\n" +
	"\r\n" +
	"A message that you sent could not be delivered to one or more of its\r\n" +
	"recipients. This is a permanent error. The following address(es) failed:\r\n" +
	"\r\n" +
	"  fantome@vieux-domaine.com\r\n" +
	"    host mx.vieux-domaine.com\r\n" +
	"    550 5.1.1 No such user here\r\n"

// realReply : une VRAIE réponse de prospect — surtout PAS un NDR. Contient même
// un nombre qui pourrait ressembler à un code (550 €) pour piéger le parseur.
const realReply = "From: Jean Client <jean@vraie-entreprise.fr>\r\n" +
	"To: robert@agences-veridian.fr\r\n" +
	"Subject: Re: Votre proposition\r\n" +
	"In-Reply-To: <camp-1@agences-veridian.fr>\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Bonjour Robert,\r\n" +
	"Merci pour votre message. Votre devis a 550 euros me parait correct,\r\n" +
	"recontactez-moi la semaine prochaine.\r\n" +
	"Cordialement, Jean\r\n"

// autoReplyOOO : un auto-reply "absence du bureau" — pas un NDR non plus.
const autoReplyOOO = "From: Marie Absente <marie@entreprise.fr>\r\n" +
	"To: robert@agences-veridian.fr\r\n" +
	"Subject: Réponse automatique : absence du bureau\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Je suis absente jusqu'au 30 du mois. Pour toute urgence contactez l'accueil.\r\n"

func TestParse_PostfixHardBounce(t *testing.T) {
	res := Parse([]byte(postfixHardNDR), "", "")
	require.True(t, res.IsNDR, "Postfix NDR doit être détecté")
	assert.Equal(t, "jean.mort@exemple-client.fr", res.Recipient)
	assert.Equal(t, SeverityHard, res.Severity)
	assert.Equal(t, "5.1.1", res.DSNCode)
	assert.Contains(t, res.DiagnosticCode, "User unknown")
	assert.Equal(t, "orig-msg-42@agences-veridian.fr", res.OriginalMessageID)
}

func TestParse_GmailHardBounce(t *testing.T) {
	res := Parse([]byte(gmailHardNDR), "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "morte@gmail.com", res.Recipient)
	assert.Equal(t, SeverityHard, res.Severity)
	assert.Equal(t, "5.1.1", res.DSNCode)
}

func TestParse_OutlookSoftBounce(t *testing.T) {
	res := Parse([]byte(outlookSoftNDR), "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "plein@contoso.com", res.Recipient)
	assert.Equal(t, SeveritySoft, res.Severity, "4.2.2 mailbox full = soft")
	assert.Equal(t, "4.2.2", res.DSNCode)
}

func TestParse_PlainTextHardBounce_Heuristic(t *testing.T) {
	res := Parse([]byte(plainTextHardNDR), "", "")
	require.True(t, res.IsNDR, "NDR text/plain de MAILER-DAEMON doit passer par l'heuristique")
	assert.Equal(t, "fantome@vieux-domaine.com", res.Recipient)
	assert.Equal(t, SeverityHard, res.Severity)
	assert.Equal(t, "5.1.1", res.DSNCode)
}

func TestParse_RealReply_NotNDR(t *testing.T) {
	res := Parse([]byte(realReply), "", "")
	assert.False(t, res.IsNDR, "une vraie réponse ne doit JAMAIS être prise pour un NDR")
	assert.Empty(t, res.Recipient)
}

func TestParse_AutoReplyOOO_NotNDR(t *testing.T) {
	res := Parse([]byte(autoReplyOOO), "", "")
	assert.False(t, res.IsNDR, "un auto-reply OOO n'est pas un bounce")
}

func TestParse_EmptyBody_NotNDR(t *testing.T) {
	res := Parse(nil, "MAILER-DAEMON@x", "Undelivered Mail")
	assert.False(t, res.IsNDR, "sans corps, pas de destinataire exploitable -> non-NDR")
}

func TestParse_GarbageMIME_FallsBackHeuristic(t *testing.T) {
	// Headers cassés mais From daemon + code + adresse dans le corps.
	garbage := "From: MAILER-DAEMON\r\nSubject: failure notice\r\n\r\n" +
		"Sorry, no mailbox here by that name. 550 5.5.0\r\n" +
		"<perdu@nowhere.example>: failed\r\n"
	res := Parse([]byte(garbage), "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "perdu@nowhere.example", res.Recipient)
	assert.Equal(t, SeverityHard, res.Severity)
}

func TestParse_FromSubjectHintsProvided(t *testing.T) {
	// Le caller (poller IMAP) fournit From/Subject déjà parsés : doivent être pris
	// en compte même si le brut ne les reparse pas.
	body := "Final-Recipient: rfc822; cible@dead.example\r\nStatus: 5.0.0\r\n"
	res := Parse([]byte(body), "mailer-daemon@host", "Delivery Status Notification")
	require.True(t, res.IsNDR)
	assert.Equal(t, "cible@dead.example", res.Recipient)
	assert.Equal(t, SeverityHard, res.Severity)
}

func TestParse_SoftWithoutEnhancedCode(t *testing.T) {
	// 421 service unavailable sans code enrichi -> soft via repli SMTP brut.
	body := "From: postmaster@host\r\nSubject: failure notice\r\n\r\n" +
		"<temp@busy.example>\r\n421 Service not available, try later\r\n"
	res := Parse([]byte(body), "", "")
	require.True(t, res.IsNDR)
	assert.Equal(t, "temp@busy.example", res.Recipient)
	assert.Equal(t, SeveritySoft, res.Severity)
}

func TestNormalizeRecipient(t *testing.T) {
	cases := []struct{ in, want string }{
		{"rfc822; Jean.Mort@Exemple.FR", "jean.mort@exemple.fr"},
		{"<user@host.com>", "user@host.com"},
		{"rfc822;user@host.com", "user@host.com"},
		{"  USER@HOST.COM  ", "user@host.com"},
		{"Some Name <real@addr.io> noise", "real@addr.io"},
		{"", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, normalizeRecipient(c.in), "input=%q", c.in)
	}
}

func TestParseAddressList(t *testing.T) {
	assert.Equal(t, []string{"a@b.com"}, ParseAddressList("A <a@b.com>"))
	assert.Equal(t, []string{"a@b.com", "c@d.com"}, ParseAddressList("a@b.com, c@d.com"))
	assert.Equal(t, []string{"raw@x.io"}, ParseAddressList("garbage raw@x.io garbage"))
	assert.Nil(t, ParseAddressList("no address here"))
}

func TestParse_DoesNotPanicOnWeirdInput(t *testing.T) {
	inputs := [][]byte{
		[]byte("\r\n\r\n"),
		[]byte("Content-Type: multipart/report; boundary=x\r\n\r\n--x\r\n--x--\r\n"),
		[]byte(strings.Repeat("A", 10000)),
		[]byte("Content-Type: multipart/report;\r\n\r\nincomplete"),
	}
	for i, in := range inputs {
		assert.NotPanics(t, func() { Parse(in, "", "") }, "input #%d", i)
	}
}
