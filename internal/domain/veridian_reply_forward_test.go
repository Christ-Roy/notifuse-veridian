package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianReplyForward_EmptyIsUnchanged(t *testing.T) {
	e := &EmailProvider{}
	assert.NoError(t, e.ValidateVeridianReplyForwardTo())
	assert.Nil(t, e.VeridianEffectiveReplyForwardTo())
	var nilProvider *EmailProvider
	assert.Nil(t, nilProvider.VeridianEffectiveReplyForwardTo())

	// Le champ est omitempty : un profil sans redirection ne change pas le blob.
	b, err := json.Marshal(EmailProvider{Kind: EmailProviderKindSMTP})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "veridian_reply_forward_to")
}

func TestVeridianReplyForward_ValidationAcceptsAndNormalizes(t *testing.T) {
	e := &EmailProvider{VeridianReplyForwardTo: []string{" Client@Client.FR ", "autre@client.fr", "client@client.fr", ""}}
	require.NoError(t, e.ValidateVeridianReplyForwardTo())
	assert.Equal(t, []string{"client@client.fr", "autre@client.fr"}, e.VeridianReplyForwardTo)
}

func TestVeridianReplyForward_ValidationRejectsInvalid(t *testing.T) {
	for _, bad := range []string{
		"pas-une-adresse", "a@b", "@client.fr", "client@", "Nom <client@client.fr>", "a@b.fr,c@d.fr",
		"a b@client.fr", "client@client.fr\nBcc: x@y.fr", "\"x\"@client.fr", strings.Repeat("a", 250) + "@client.fr",
	} {
		e := &EmailProvider{VeridianReplyForwardTo: []string{bad}}
		assert.Error(t, e.ValidateVeridianReplyForwardTo(), "doit refuser %q", bad)
	}
}

func TestVeridianReplyForward_ValidationMaxFive(t *testing.T) {
	five := []string{"a@c.fr", "b@c.fr", "c@c.fr", "d@c.fr", "e@c.fr"}
	e := &EmailProvider{VeridianReplyForwardTo: five}
	require.NoError(t, e.ValidateVeridianReplyForwardTo())
	e = &EmailProvider{VeridianReplyForwardTo: append(five, "f@c.fr")}
	assert.Error(t, e.ValidateVeridianReplyForwardTo())
}

func TestVeridianReplyForward_RejectsOwnSenderAddress(t *testing.T) {
	e := &EmailProvider{
		Senders:                []EmailSender{{Email: "Pro@Send.example"}},
		SMTP:                   &SMTPSettings{Username: "boite@send.example"},
		VeridianReplyForwardTo: []string{"pro@send.example"},
	}
	assert.Error(t, e.ValidateVeridianReplyForwardTo())
	e.VeridianReplyForwardTo = []string{"boite@send.example"}
	assert.Error(t, e.ValidateVeridianReplyForwardTo())
}

func TestVeridianReplyForward_ValidateIsWiredInProviderValidate(t *testing.T) {
	e := &EmailProvider{Kind: EmailProviderKindSMTP, VeridianReplyForwardTo: []string{"pas-une-adresse"}}
	err := e.Validate("pass")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "veridian_reply_forward_to")
}

func TestVeridianReplyForward_EffectiveDropsBadAndOwnAndCaps(t *testing.T) {
	// Un blob ecrit a la main (hors API) ne doit jamais produire d'envoi vers une adresse
	// invalide ou vers l'adresse d'envoi.
	e := &EmailProvider{
		Senders: []EmailSender{{Email: "pro@send.example"}},
		VeridianReplyForwardTo: []string{
			"bad", "pro@send.example", "ok1@c.fr", "OK1@c.fr", "ok2@c.fr", "ok3@c.fr", "ok4@c.fr", "ok5@c.fr", "ok6@c.fr",
		},
	}
	assert.Equal(t, []string{"ok1@c.fr", "ok2@c.fr", "ok3@c.fr", "ok4@c.fr", "ok5@c.fr"}, e.VeridianEffectiveReplyForwardTo())
}

func TestVeridianReplyForward_JSONRoundTrip(t *testing.T) {
	in := EmailProvider{Kind: EmailProviderKindSMTP, VeridianReplyForwardTo: []string{"client@client.fr"}}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"veridian_reply_forward_to":["client@client.fr"]`)
	var out EmailProvider
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in.VeridianReplyForwardTo, out.VeridianReplyForwardTo)
}
