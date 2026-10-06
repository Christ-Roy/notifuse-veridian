package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func rawMsg(headers, body string) []byte {
	return []byte(headers + "\r\n\r\n" + body)
}

func TestVeridianClassifyReplyType(t *testing.T) {
	cases := []struct {
		name string
		msg  *VeridianIMAPMessage
		want VeridianReplyType
	}{
		{
			name: "human reply without any automatic marker",
			msg: &VeridianIMAPMessage{
				From: "Anne <anne@client.fr>", Subject: "Re: question sur client.fr",
				RawBody: rawMsg("From: anne@client.fr\r\nSubject: Re: question", "Bonjour, oui, appelez-moi."),
			},
			want: VeridianReplyTypeHuman,
		},
		{
			name: "Auto-Submitted auto-replied",
			msg: &VeridianIMAPMessage{
				From: "x@client.fr", Subject: "Re: question",
				RawBody: rawMsg("Auto-Submitted: auto-replied", "absent"),
			},
			want: VeridianReplyTypeAuto,
		},
		{
			name: "Auto-Submitted no is a human",
			msg: &VeridianIMAPMessage{
				From: "x@client.fr", Subject: "Re: question",
				RawBody: rawMsg("Auto-Submitted: no", "oui"),
			},
			want: VeridianReplyTypeHuman,
		},
		{
			name: "Precedence bulk",
			msg: &VeridianIMAPMessage{
				From: "x@client.fr", Subject: "Conges",
				RawBody: rawMsg("Precedence: bulk", "absent"),
			},
			want: VeridianReplyTypeAuto,
		},
		{
			name: "X-Autoreply header",
			msg: &VeridianIMAPMessage{
				From: "x@client.fr", Subject: "Re: x",
				RawBody: rawMsg("X-Autoreply: yes", "absent"),
			},
			want: VeridianReplyTypeAuto,
		},
		{
			name: "auto-reply subject without headers (no RawBody)",
			msg:  &VeridianIMAPMessage{From: "x@client.fr", Subject: "Réponse automatique : question"},
			want: VeridianReplyTypeAuto,
		},
		{
			name: "out of office subject",
			msg:  &VeridianIMAPMessage{From: "x@client.fr", Subject: "Out of office: Re: question"},
			want: VeridianReplyTypeAuto,
		},
		{
			name: "anti-spam gateway sender (xefi)",
			msg:  &VeridianIMAPMessage{From: "tfs_sait_france@antispam6.xefi.fr", Subject: "Re: La refonte"},
			want: VeridianReplyTypeChallenge,
		},
		{
			name: "mailinblack invitation sender",
			msg:  &VeridianIMAPMessage{From: "procop@invitations.mailinblack.com", Subject: "Re: La refonte"},
			want: VeridianReplyTypeChallenge,
		},
		{
			name: "challenge detected in the body, no automatic header",
			msg: &VeridianIMAPMessage{
				From: "nfs@newfreshservice.fr", Subject: "Re: la vente en ligne",
				RawBody: rawMsg("From: nfs@newfreshservice.fr", "Votre message est protege par un anti-spam, cliquez pour valider"),
			},
			want: VeridianReplyTypeChallenge,
		},
		{
			name: "nil message defaults to human",
			msg:  nil,
			want: VeridianReplyTypeHuman,
		},
		{
			name: "truncated body (headers only) stays human",
			msg: &VeridianIMAPMessage{
				From: "x@client.fr", Subject: "Re: question", RawBody: []byte("From: x@client.fr\r\nSubject: Re: question"),
			},
			want: VeridianReplyTypeHuman,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, VeridianClassifyReplyType(c.msg))
		})
	}
}

func TestVeridianReferencesFromRaw(t *testing.T) {
	raw := rawMsg("From: x@y.fr\r\nReferences: <a@d.fr>\r\n <b@d.fr>\r\nSubject: s", "corps")
	refs := VeridianReferencesFromRaw(raw)
	assert.Equal(t, []string{"<a@d.fr> <b@d.fr>"}, refs)
	// Les local-parts se retrouvent par le helper existant.
	assert.Equal(t, []string{"a", "b"}, VeridianExtractMessageIDLocalParts("", refs))

	assert.Empty(t, VeridianReferencesFromRaw(nil))
	assert.Empty(t, VeridianReferencesFromRaw(rawMsg("From: x@y.fr", "corps")))
}

func TestVeridianParseRawHeaders(t *testing.T) {
	hdr := VeridianParseRawHeaders(rawMsg("Auto-Submitted: auto-replied\r\nSubject: s", "corps"))
	assert.Equal(t, "auto-replied", hdr.Get("Auto-Submitted"))
	assert.Equal(t, "s", hdr.Get("Subject"))
	// Entree vide ou illisible : jamais nil, jamais de panique.
	assert.NotNil(t, VeridianParseRawHeaders(nil))
	assert.Empty(t, VeridianParseRawHeaders([]byte("pas un en-tete")).Get("Subject"))
}
