package mailbox

import (
	"encoding/base64"
	"strings"
)

func crlf(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }

// replyHuman : réponse humaine d'un prospect à un envoi Notifuse (id nu abc... dans In-Reply-To).
func replyHuman(inReplyTo, refs string) []byte {
	return crlf(`From: Marie Dupont <marie@exemple-animaux.fr>
To: hugo.bourdat@animalservicedistribution.fr
Subject: Re: Présentation ASD
Date: Thu, 29 Oct 2026 10:15:00 +0100
Message-ID: <rep1@exemple-animaux.fr>
In-Reply-To: ` + inReplyTo + `
References: ` + refs + `
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="b1"

--b1
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: quoted-printable

Bonjour, oui cela m'int=C3=A9resse. Pouvez-vous m'appeler ?

> Bonjour, nous distribuons des produits pour animaux.
--b1
Content-Type: text/html; charset=utf-8

<html><body><p>Bonjour, <b>oui</b> cela m'int&eacute;resse.</p><script>alert(1)</script><img src="https://t.example/p.gif" onerror="x()"></body></html>
--b1--
`)
}

// bounce550 : NDR Postfix, adresse inexistante chez Gmail.
func bounce550(origID string) []byte {
	return crlf(`From: MAILER-DAEMON@relais.animalservicedistribution.fr (Mail Delivery System)
To: hugo.bourdat@animalservicedistribution.fr
Subject: Undelivered Mail Returned to Sender
Date: Thu, 29 Oct 2026 10:20:00 +0100
Message-ID: <bnc1@relais.animalservicedistribution.fr>
MIME-Version: 1.0
Content-Type: multipart/report; report-type=delivery-status; boundary="bb"

--bb
Content-Type: text/plain; charset=utf-8

This is the mail system at host relais.
I'm sorry to have to inform you that your message could not be delivered.

--bb
Content-Type: message/delivery-status

Reporting-MTA: dns; relais.animalservicedistribution.fr

Final-Recipient: rfc822; mort@gmail.com
Original-Recipient: rfc822;mort@gmail.com
Action: failed
Status: 5.1.1
Remote-MTA: dns; gmail-smtp-in.l.google.com
Diagnostic-Code: smtp; 550-5.1.1 The email account that you tried to reach does not exist.

--bb
Content-Type: message/rfc822

Message-ID: <` + origID + `>
From: hugo.bourdat@animalservicedistribution.fr
To: mort@gmail.com
Subject: Présentation ASD

contenu d'origine
--bb--
`)
}

// bounceSoft : boîte pleine, 4.2.2.
func bounceSoft(origID string) []byte {
	return crlf(`From: MAILER-DAEMON@relais.animalservicedistribution.fr
To: hugo.bourdat@animalservicedistribution.fr
Subject: Delivery Status Notification (Delay)
Date: Thu, 29 Oct 2026 10:21:00 +0100
Message-ID: <bnc2@relais.animalservicedistribution.fr>
MIME-Version: 1.0
Content-Type: multipart/report; report-type=delivery-status; boundary="bb"

--bb
Content-Type: text/plain

delayed

--bb
Content-Type: message/delivery-status

Reporting-MTA: dns; relais.animalservicedistribution.fr

Final-Recipient: rfc822; plein@orange.fr
Action: failed
Status: 4.2.2
Remote-MTA: dns; smtp-in.orange.fr
Diagnostic-Code: smtp; 452 4.2.2 Over quota

--bb
Content-Type: message/rfc822

Message-ID: <` + origID + `>
Subject: Présentation ASD

x
--bb--
`)
}

func autoReply(inReplyTo string) []byte {
	return crlf(`From: contact@societe.fr
To: hugo.bourdat@animalservicedistribution.fr
Subject: Réponse automatique : Présentation ASD
Date: Thu, 29 Oct 2026 11:00:00 +0100
Message-ID: <auto1@societe.fr>
In-Reply-To: ` + inReplyTo + `
Auto-Submitted: auto-replied
Content-Type: text/plain; charset=utf-8

Je suis absent jusqu'au 5 novembre.
`)
}

func withAttachment(inReplyTo string) []byte {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 fake pdf content"))
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfakepng"))
	return crlf(`From: Paul <paul@cabinet.fr>
To: hugo.bourdat@animalservicedistribution.fr
Subject: Re: Présentation ASD
Date: Thu, 29 Oct 2026 12:00:00 +0100
Message-ID: <att1@cabinet.fr>
In-Reply-To: ` + inReplyTo + `
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="m"

--m
Content-Type: multipart/related; boundary="r"

--r
Content-Type: text/html; charset=utf-8

<p>Voir <img src="cid:logo@cabinet"> et le catalogue joint.</p>
--r
Content-Type: image/png
Content-ID: <logo@cabinet>
Content-Disposition: inline; filename="logo.png"
Content-Transfer-Encoding: base64

` + png + `
--r--
--m
Content-Type: application/pdf; name="catalogue.pdf"
Content-Disposition: attachment; filename="catalogue.pdf"
Content-Transfer-Encoding: base64

` + pdf + `
--m--
`)
}

func latin1Mail() []byte {
	// « Café » en ISO-8859-1 : octet 0xE9
	b := []byte("From: x@y.fr\r\nTo: a@b.fr\r\nSubject: Caf=?ISO-8859-1?Q?=E9?=\r\nMessage-ID: <l1@y.fr>\r\nContent-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: 8bit\r\n\r\nCaf")
	b = append(b, 0xE9)
	b = append(b, []byte(" cr")...)
	b = append(b, 0xE8)
	b = append(b, []byte("me")...)
	return b
}
