package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// strPtr est défini dans veridian_open_pixel_test.go (même package domain).

func TestVeridianClassifyContactProviderClass(t *testing.T) {
	tests := []struct {
		name string
		row  VeridianContactProviderRow
		want string
	}{
		{
			name: "gmail suffix -> google",
			row:  VeridianContactProviderRow{Email: "jane@gmail.com"},
			want: ProviderClassGoogle,
		},
		{
			name: "outlook suffix -> microsoft",
			row:  VeridianContactProviderRow{Email: "bob@outlook.fr"},
			want: ProviderClassMicrosoft,
		},
		{
			name: "yahoo suffix -> yahoo_aol",
			row:  VeridianContactProviderRow{Email: "x@yahoo.com"},
			want: ProviderClassYahooAol,
		},
		{
			name: "orange.fr suffix -> freemail_fr",
			row:  VeridianContactProviderRow{Email: "y@orange.fr"},
			want: ProviderClassFreemailFR,
		},
		{
			name: "unknown domain -> corporate",
			row:  VeridianContactProviderRow{Email: "ceo@acme-corp.io"},
			want: ProviderClassCorporate,
		},
		{
			name: "invalid email -> corporate",
			row:  VeridianContactProviderRow{Email: "not-an-email"},
			want: ProviderClassCorporate,
		},
		{
			name: "custom_string_5 override prime sur suffixe gmail",
			row:  VeridianContactProviderRow{Email: "jane@gmail.com", CustomString5: strPtr("corporate")},
			want: ProviderClassCorporate,
		},
		{
			name: "custom_string_5 override microsoft sur corporate domain",
			row:  VeridianContactProviderRow{Email: "ceo@acme-corp.io", CustomString5: strPtr("microsoft")},
			want: ProviderClassMicrosoft,
		},
		{
			name: "custom_string_5 override normalisé (trim + upper)",
			row:  VeridianContactProviderRow{Email: "ceo@acme-corp.io", CustomString5: strPtr("  GOOGLE  ")},
			want: ProviderClassGoogle,
		},
		{
			name: "custom_string_5 non-canonique ignoré -> fallback suffixe",
			row:  VeridianContactProviderRow{Email: "jane@gmail.com", CustomString5: strPtr("not_a_class")},
			want: ProviderClassGoogle,
		},
		{
			name: "custom_string_5 null ignoré -> fallback suffixe",
			row:  VeridianContactProviderRow{Email: "jane@gmail.com", CustomString5: &NullableString{IsNull: true}},
			want: ProviderClassGoogle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VeridianClassifyContactProviderClass(tt.row)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVeridianClassifyDomainProviderClass(t *testing.T) {
	// Équivalence stricte avec la version email : pour chaque cas, classer par
	// DOMAINE (pré-agrégé par SQL) doit donner la MÊME classe que classer par
	// EMAIL — c'est le contrat qui garantit la non-régression du breakdown.
	cases := []struct {
		domain string
		tag    *NullableString
		want   string
	}{
		{"gmail.com", nil, ProviderClassGoogle},
		{"outlook.fr", nil, ProviderClassMicrosoft},
		{"yahoo.com", nil, ProviderClassYahooAol},
		{"orange.fr", nil, ProviderClassFreemailFR},
		{"acme-corp.io", nil, ProviderClassCorporate},
		{"", nil, ProviderClassCorporate}, // domaine vide (email invalide en SQL)
		{"gmail.com", strPtr("corporate"), ProviderClassCorporate},
		{"acme-corp.io", strPtr("microsoft"), ProviderClassMicrosoft},
		{"acme-corp.io", strPtr("  GOOGLE  "), ProviderClassGoogle},
		{"gmail.com", strPtr("not_a_class"), ProviderClassGoogle},
		{"gmail.com", &NullableString{IsNull: true}, ProviderClassGoogle},
		{"GMAIL.COM", nil, ProviderClassGoogle}, // domaine non normalisé -> normalisé
	}
	for _, c := range cases {
		got := VeridianClassifyDomainProviderClass(c.domain, c.tag)
		assert.Equal(t, c.want, got, "domain=%q tag=%v", c.domain, c.tag)
	}
}

func TestVeridianAggregateProviderBreakdownCounts(t *testing.T) {
	t.Run("workspace vide -> toutes classes à 0, total 0", func(t *testing.T) {
		got := VeridianAggregateProviderBreakdownCounts(nil)
		assert.Equal(t, 0, got.Total)
		// Sortie STABLE : TOUTES les classes canoniques (historiques + MX)
		// sont présentes à 0, pour que l'UI rende une carte par classe.
		want := map[string]int{}
		for _, c := range VeridianAllProviderClasses() {
			want[c] = 0
		}
		assert.Equal(t, want, got.Breakdown)
		assert.Len(t, got.Breakdown, 11, "11 classes attendues dans le breakdown")
	})

	t.Run("comptage mixte par classe (counts pré-agrégés)", func(t *testing.T) {
		// Équivalent SQL-agrégé du jeu de 7 contacts ligne-par-ligne : gmail x2
		// est UNE ligne count=2, etc. Le breakdown final doit être identique.
		counts := []VeridianContactProviderCount{
			{Domain: "gmail.com", Count: 2},
			{Domain: "outlook.com", Count: 1},
			{Domain: "yahoo.fr", Count: 1},
			{Domain: "free.fr", Count: 1},
			{Domain: "acme.io", Count: 1},
			{Domain: "unknown-domain.xyz", Count: 1},
		}
		got := VeridianAggregateProviderBreakdownCounts(counts)
		assert.Equal(t, 7, got.Total)
		assert.Equal(t, 2, got.Breakdown[ProviderClassGoogle])
		assert.Equal(t, 1, got.Breakdown[ProviderClassMicrosoft])
		assert.Equal(t, 1, got.Breakdown[ProviderClassYahooAol])
		assert.Equal(t, 1, got.Breakdown[ProviderClassFreemailFR])
		assert.Equal(t, 2, got.Breakdown[ProviderClassCorporate]) // acme.io + unknown
	})

	t.Run("override custom_string_5 reclasse le compte agrégé", func(t *testing.T) {
		counts := []VeridianContactProviderCount{
			{Domain: "gmail.com", Count: 1},                              // google
			{Domain: "gmail.com", CustomString5: strPtr("corporate"), Count: 1}, // forcé corporate (groupe SQL distinct)
			{Domain: "acme.io", CustomString5: strPtr("google"), Count: 1},      // forcé google
		}
		got := VeridianAggregateProviderBreakdownCounts(counts)
		assert.Equal(t, 3, got.Total)
		assert.Equal(t, 2, got.Breakdown[ProviderClassGoogle])    // gmail + acme(forcé)
		assert.Equal(t, 1, got.Breakdown[ProviderClassCorporate]) // gmail(forcé)
	})

	t.Run("count <= 0 ignoré (robustesse)", func(t *testing.T) {
		counts := []VeridianContactProviderCount{
			{Domain: "gmail.com", Count: 3},
			{Domain: "outlook.com", Count: 0},  // ignoré
			{Domain: "yahoo.com", Count: -5},   // ignoré (best-effort)
		}
		got := VeridianAggregateProviderBreakdownCounts(counts)
		assert.Equal(t, 3, got.Total)
		assert.Equal(t, 3, got.Breakdown[ProviderClassGoogle])
		assert.Equal(t, 0, got.Breakdown[ProviderClassMicrosoft])
	})

	// Non-régression PROUVÉE : agréger par counts (SQL GROUP BY) == agréger les
	// mêmes contacts ligne-par-ligne (ancienne voie). C'est le coeur du fix #3.
	t.Run("équivalence stricte counts vs lignes", func(t *testing.T) {
		rows := []VeridianContactProviderRow{
			{Email: "a@gmail.com"},
			{Email: "b@gmail.com"},
			{Email: "c@outlook.com"},
			{Email: "d@acme.io", CustomString5: strPtr("google")},
			{Email: "e@acme.io"},
		}
		// Référence : ancienne agrégation ligne-par-ligne.
		ref := map[string]int{}
		for _, c := range VeridianAllProviderClasses() {
			ref[c] = 0
		}
		for _, r := range rows {
			ref[VeridianClassifyContactProviderClass(r)]++
		}
		// Counts équivalents (gmail x2 groupé, acme.io+google et acme.io séparés).
		counts := []VeridianContactProviderCount{
			{Domain: "gmail.com", Count: 2},
			{Domain: "outlook.com", Count: 1},
			{Domain: "acme.io", CustomString5: strPtr("google"), Count: 1},
			{Domain: "acme.io", Count: 1},
		}
		got := VeridianAggregateProviderBreakdownCounts(counts)
		assert.Equal(t, len(rows), got.Total)
		assert.Equal(t, ref, got.Breakdown, "le breakdown agrégé doit être identique au ligne-par-ligne")
	})
}

func TestVeridianProviderClassFromTag(t *testing.T) {
	assert.Equal(t, "", veridianProviderClassFromTag(nil))
	assert.Equal(t, "", veridianProviderClassFromTag(&NullableString{IsNull: true}))
	assert.Equal(t, "", veridianProviderClassFromTag(strPtr("nope")))
	assert.Equal(t, ProviderClassMicrosoft, veridianProviderClassFromTag(strPtr("microsoft")))
}
