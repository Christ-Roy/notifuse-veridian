package reservoir

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestNormalizeSirenEtDomaine(t *testing.T) {
	for in, want := range map[string]string{"352522304": "352522304", "352 522 304": "352522304", "35252230400012": "352522304", "12345": "", "000000000": "", "": ""} {
		assert.Equal(t, want, NormalizeSiren(in), in)
	}
	for in, want := range map[string]string{"https://www.Exemple.fr/contact?x=1": "exemple.fr", "boutique.exemple.co.uk": "exemple.co.uk", "exemple.fr.": "exemple.fr",
		"a@b.fr": "", "localhost": "", "": "", "pas un site": ""} {
		assert.Equal(t, want, NormalizeDomain(in), in)
	}
	assert.True(t, SharedDomain("gmail.com"))
	assert.True(t, SharedDomain("orange.fr"))
	assert.True(t, SharedDomain("icloud.com"))
	assert.False(t, SharedDomain("exemple.fr"))
}

func TestParseRow(t *testing.T) {
	r, reason := ParseRow(3, raw(`{"email":" Contact@Exemple.FR ","first_name":"Léa","siret":"35252230400012","website":"https://www.autre.fr","ville":"Lyon","custom_json_1":{"code_naf":"70.10Z","ville":"Paris"},"created_at":"2001-01-01T00:00:00Z"}`))
	require.Empty(t, reason)
	assert.Equal(t, "contact@exemple.fr", r.Email)
	assert.Equal(t, "352522304", r.Siren)
	assert.Equal(t, []string{"exemple.fr", "autre.fr"}, r.Domains)
	assert.Contains(t, r.Fields, "first_name")
	assert.NotContains(t, r.Fields, "created_at", "une date de fiche ne s'importe pas")
	c, err := r.Contact(raw(`{"deja":"la","code_naf":"vieux"}`))
	require.NoError(t, err)
	j := c.CustomJSON1.Data.(map[string]any)
	assert.Equal(t, "la", j["deja"], "le json existant est complété, pas écrasé")
	assert.Equal(t, "70.10Z", j["code_naf"])
	assert.Equal(t, "Lyon", j["ville"], "le champ de premier niveau prime")
	assert.Equal(t, "352522304", j["siren"])
	assert.Equal(t, "exemple.fr", j["domaine"])

	r, reason = ParseRow(0, raw(`{"email":"x@gmail.com"}`))
	require.Empty(t, reason)
	assert.Empty(t, r.Domains, "une messagerie partagée n'est pas une clé")
	for body, want := range map[string]string{`{"email":"pas une adresse"}`: ReasonBadEmail, `{}`: ReasonBadEmail, `"x"`: ReasonBadContact, `null`: ReasonBadContact,
		`{"email":"a@b.fr","custom_json_1":"texte"}`: ReasonBadContact} {
		_, reason = ParseRow(0, raw(body))
		assert.Equal(t, want, reason, body)
	}
}

func rows(t *testing.T, bodies ...string) []Row {
	var out []Row
	for i, b := range bodies {
		r, reason := ParseRow(i, raw(b))
		require.Empty(t, reason, b)
		out = append(out, r)
	}
	return out
}

func TestBuild_DoublonsEtExclusions(t *testing.T) {
	in := rows(t,
		`{"email":"a@un.fr","siren":"111111111"}`,
		`{"email":"A@un.fr"}`,                          // même adresse
		`{"email":"b@deux.fr","siren":"111111111"}`,    // même SIREN dans le lot
		`{"email":"c@un.fr"}`,                          // même domaine dans le lot
		`{"email":"d@gmail.com","siren":"222222222"}`,  // gmail : le domaine ne compte pas
		`{"email":"e@gmail.com","siren":"333333333"}`,  // idem
		`{"email":"f@trois.fr","siren":"444444444"}`,   // SIREN déjà en base
		`{"email":"g@quatre.fr"}`,                      // domaine déjà en base
		`{"email":"stop@cinq.fr"}`,                     // désinscrit
		`{"email":"connu@six.fr","siren":"444444444"}`, // adresse déjà en base : existant, pas doublon
	)
	k := Known{Emails: map[string]bool{"connu@six.fr": true, "stop@cinq.fr": true}, Sirens: map[string]string{"444444444": "connu@six.fr"},
		Domains: map[string]string{"quatre.fr": "z@quatre.fr"}, Blocked: map[string]string{"stop@cinq.fr": ReasonUnsubscribed}}
	p := Build(in, []Detail{{Index: 99, Reason: ReasonBadEmail}}, k, Dedupe{Siren: true, Domain: true})
	var got []string
	for _, r := range p.New {
		got = append(got, r.Email)
	}
	assert.Equal(t, []string{"a@un.fr", "d@gmail.com", "e@gmail.com"}, got)
	require.Len(t, p.Existing, 1)
	assert.Equal(t, map[string]int{ReasonInBatch: 3, ReasonSiren: 1, ReasonDomain: 1, ReasonUnsubscribed: 1, ReasonBadEmail: 1}, p.Skipped)

	p = Build(in, nil, k, Dedupe{})
	assert.Len(t, p.New, 7, "sans SIREN ni domaine : seules l'adresse et les exclusions comptent")
	assert.Equal(t, 1, p.Skipped[ReasonUnsubscribed], "une désinscription s'applique toujours")
}

func TestParseDedupe(t *testing.T) {
	d, err := ParseDedupe(nil)
	require.NoError(t, err)
	assert.Equal(t, Dedupe{true, true}, d)
	d, err = ParseDedupe([]string{"email"})
	require.NoError(t, err)
	assert.Equal(t, Dedupe{}, d)
	_, err = ParseDedupe([]string{"nom"})
	assert.ErrorIs(t, err, ErrInvalid)
}
