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

func TestVeridianAggregateProviderBreakdown(t *testing.T) {
	t.Run("workspace vide -> toutes classes à 0, total 0", func(t *testing.T) {
		got := VeridianAggregateProviderBreakdown(nil)
		assert.Equal(t, 0, got.Total)
		assert.Equal(t, map[string]int{
			ProviderClassGoogle:     0,
			ProviderClassMicrosoft:  0,
			ProviderClassYahooAol:   0,
			ProviderClassFreemailFR: 0,
			ProviderClassCorporate:  0,
		}, got.Breakdown)
	})

	t.Run("comptage mixte par classe", func(t *testing.T) {
		rows := []VeridianContactProviderRow{
			{Email: "a@gmail.com"},
			{Email: "b@gmail.com"},
			{Email: "c@outlook.com"},
			{Email: "d@yahoo.fr"},
			{Email: "e@free.fr"},
			{Email: "f@acme.io"},
			{Email: "g@unknown-domain.xyz"},
		}
		got := VeridianAggregateProviderBreakdown(rows)
		assert.Equal(t, 7, got.Total)
		assert.Equal(t, 2, got.Breakdown[ProviderClassGoogle])
		assert.Equal(t, 1, got.Breakdown[ProviderClassMicrosoft])
		assert.Equal(t, 1, got.Breakdown[ProviderClassYahooAol])
		assert.Equal(t, 1, got.Breakdown[ProviderClassFreemailFR])
		assert.Equal(t, 2, got.Breakdown[ProviderClassCorporate]) // acme.io + unknown
	})

	t.Run("override custom_string_5 reclasse le compte", func(t *testing.T) {
		rows := []VeridianContactProviderRow{
			{Email: "a@gmail.com"}, // google
			{Email: "b@gmail.com", CustomString5: strPtr("corporate")}, // forcé corporate
			{Email: "c@acme.io", CustomString5: strPtr("google")},      // forcé google
		}
		got := VeridianAggregateProviderBreakdown(rows)
		assert.Equal(t, 3, got.Total)
		assert.Equal(t, 2, got.Breakdown[ProviderClassGoogle])    // a + c(forcé)
		assert.Equal(t, 1, got.Breakdown[ProviderClassCorporate]) // b(forcé)
	})
}

func TestVeridianProviderClassFromTag(t *testing.T) {
	assert.Equal(t, "", veridianProviderClassFromTag(nil))
	assert.Equal(t, "", veridianProviderClassFromTag(&NullableString{IsNull: true}))
	assert.Equal(t, "", veridianProviderClassFromTag(strPtr("nope")))
	assert.Equal(t, ProviderClassMicrosoft, veridianProviderClassFromTag(strPtr("microsoft")))
}
