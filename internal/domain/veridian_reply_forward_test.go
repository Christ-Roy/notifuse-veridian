package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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

func TestVeridianReplyForward_ValidateIsWiredInWorkspaceHook(t *testing.T) {
	// Le crochet Veridian deja appele par createIntegration, updateIntegration et Workspace.Validate.
	w := &Workspace{ID: "ws"}
	bad := &EmailProvider{Kind: EmailProviderKindSMTP, VeridianReplyForwardTo: []string{"pas-une-adresse"}}
	err := w.ValidateVeridianReturnIMAPLink(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "veridian_reply_forward_to")
	var verr ValidationError
	assert.ErrorAs(t, err, &verr, "doit etre une erreur de validation (400), pas une erreur interne")

	ok := &EmailProvider{Kind: EmailProviderKindSMTP, VeridianReplyForwardTo: []string{" Client@Client.FR "}}
	require.NoError(t, w.ValidateVeridianReturnIMAPLink(ok))
	assert.Equal(t, []string{"client@client.fr"}, ok.VeridianReplyForwardTo, "normalise en place")

	none := &EmailProvider{Kind: EmailProviderKindSMTP}
	assert.NoError(t, w.ValidateVeridianReturnIMAPLink(none))
	assert.NoError(t, w.ValidateVeridianReturnIMAPLink(nil))
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

func TestVeridianReplyForward_DailyCapValidationAndDefault(t *testing.T) {
	assert.Equal(t, 500, (&EmailProvider{}).VeridianEffectiveReplyForwardDailyCap(), "defaut 500")
	assert.Equal(t, 120, (&EmailProvider{VeridianReplyForwardDailyCap: 120}).VeridianEffectiveReplyForwardDailyCap())
	assert.Equal(t, 5000, (&EmailProvider{VeridianReplyForwardDailyCap: 5000}).VeridianEffectiveReplyForwardDailyCap())
	assert.Equal(t, 500, (&EmailProvider{VeridianReplyForwardDailyCap: 9999}).VeridianEffectiveReplyForwardDailyCap(), "hors borne a l'execution : defaut")
	var nilProvider *EmailProvider
	assert.Equal(t, 500, nilProvider.VeridianEffectiveReplyForwardDailyCap())

	for _, ok := range []int{0, 1, 500, 5000} {
		assert.NoError(t, (&EmailProvider{VeridianReplyForwardDailyCap: ok}).ValidateVeridianReplyForwardTo(), "%d", ok)
	}
	for _, bad := range []int{-1, 5001, 100000} {
		err := (&EmailProvider{VeridianReplyForwardDailyCap: bad}).ValidateVeridianReplyForwardTo()
		assert.Error(t, err, "%d doit etre refuse a l'ecriture", bad)
	}
	// valide meme sans redirection configuree
	assert.Error(t, (&EmailProvider{VeridianReplyForwardDailyCap: 6000, VeridianReplyForwardTo: nil}).ValidateVeridianReplyForwardTo())

	b, err := json.Marshal(EmailProvider{Kind: EmailProviderKindSMTP})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "veridian_reply_forward_daily_cap", "omitempty : le blob des profils existants ne change pas")
}

func TestVeridianBuildReplyForwardOverview(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	oldest := now.Add(-3 * time.Hour)
	on := &EmailProvider{VeridianReplyForwardTo: []string{"client@client.fr"}, VeridianReplyForwardDailyCap: 50}
	off := &EmailProvider{}

	assert.Nil(t, VeridianBuildReplyForwardOverview(off, nil, true, now), "ni redirection ni historique : rien a afficher")
	assert.Nil(t, VeridianBuildReplyForwardOverview(off, nil, false, now))

	o := VeridianBuildReplyForwardOverview(on, &VeridianReplyForwardProfileStats{Sent24h: 20, Pending: 1, Retry: 2, Deferred: 3, Failed7d: 4, DominantErrorClass: "smtp_4xx", OldestUnsentAt: &oldest}, true, now)
	require.NotNil(t, o)
	assert.True(t, o.Enabled)
	assert.Equal(t, 50, o.DailyCap)
	assert.True(t, o.StatsKnown)
	assert.Equal(t, 20, o.Sent24h)
	assert.Equal(t, 6, o.Waiting)
	assert.Equal(t, 4, o.Failed7d)
	assert.Equal(t, "smtp_4xx", o.DominantErrorClass)
	assert.Equal(t, int64(3*3600), o.OldestUnsentAgeSeconds)

	known := VeridianBuildReplyForwardOverview(on, nil, true, now)
	require.NotNil(t, known)
	assert.True(t, known.StatsKnown, "journal lu, aucune ligne : de vrais zeros")
	unknown := VeridianBuildReplyForwardOverview(on, nil, false, now)
	require.NotNil(t, unknown)
	assert.False(t, unknown.StatsKnown, "lecture impossible : jamais presente comme des zeros mesures")

	// redirection retiree mais des copies attendent encore : on les montre
	left := VeridianBuildReplyForwardOverview(off, &VeridianReplyForwardProfileStats{Deferred: 2, OldestUnsentAt: &oldest}, true, now)
	require.NotNil(t, left)
	assert.False(t, left.Enabled)
	assert.Equal(t, 2, left.Waiting)
}

func TestVeridianReplyForward_RetryScheduleIsTheDocumentedOne(t *testing.T) {
	assert.Equal(t, []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 12 * time.Hour}, VeridianReplyForwardRetryDelays)
}
