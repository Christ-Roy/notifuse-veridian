package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyProviderClass(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  string
	}{
		// Google
		{"gmail", "jean.dupont@gmail.com", ProviderClassGoogle},
		{"googlemail", "x@googlemail.com", ProviderClassGoogle},
		{"gmail uppercase domain", "JEAN@GMAIL.COM", ProviderClassGoogle},
		{"gmail plus alias", "jean+tag@gmail.com", ProviderClassGoogle},

		// Microsoft
		{"outlook com", "a@outlook.com", ProviderClassMicrosoft},
		{"outlook fr", "a@outlook.fr", ProviderClassMicrosoft},
		{"hotmail fr", "a@hotmail.fr", ProviderClassMicrosoft},
		{"live com", "a@live.com", ProviderClassMicrosoft},
		{"msn", "a@msn.com", ProviderClassMicrosoft},

		// Yahoo / AOL
		{"yahoo com", "a@yahoo.com", ProviderClassYahooAol},
		{"yahoo fr", "a@yahoo.fr", ProviderClassYahooAol},
		{"aol", "a@aol.com", ProviderClassYahooAol},
		{"ymail", "a@ymail.com", ProviderClassYahooAol},

		// Freemail FR
		{"orange", "a@orange.fr", ProviderClassFreemailFR},
		{"wanadoo", "a@wanadoo.fr", ProviderClassFreemailFR},
		{"free", "a@free.fr", ProviderClassFreemailFR},
		{"sfr", "a@sfr.fr", ProviderClassFreemailFR},
		{"laposte", "a@laposte.net", ProviderClassFreemailFR},

		// Corporate fallback
		{"corporate fr", "contact@boitepro.fr", ProviderClassCorporate},
		{"corporate com", "info@acme-corp.com", ProviderClassCorporate},
		{"subdomain of gmail is NOT gmail", "a@mail.gmail.com", ProviderClassCorporate},

		// V1 : table de SUFFIXE EXACT (MX résolu en amont par Prospection). Les
		// sous-domaines de providers tombent donc en corporate — comportement
		// voulu et documenté, testé ici pour le verrouiller contre une dérive.
		{"subdomain corp.google.com is corporate", "a@corp.google.com", ProviderClassCorporate},
		{"subdomain mail.yahoo.com is corporate", "a@mail.yahoo.com", ProviderClassCorporate},
		{"gmail.co.uk not in table is corporate", "a@gmail.co.uk", ProviderClassCorporate},

		// FQDN absolu : point terminal normalisé → classé comme le domaine nu.
		{"gmail FQDN trailing dot", "a@gmail.com.", ProviderClassGoogle},
		{"outlook FQDN trailing dot", "a@outlook.fr.", ProviderClassMicrosoft},
		{"corporate FQDN trailing dot stays corporate", "a@acme.fr.", ProviderClassCorporate},

		// Casse mixte sur le domaine.
		{"gmail mixed case", "Jean@Gmail.Com", ProviderClassGoogle},

		// IDN / unicode : non transformé en punycode ici → corporate (jamais de
		// panic). La résolution fine d'un domaine IDN provider relève de l'amont.
		{"IDN unicode domain", "a@münchen.de", ProviderClassCorporate},
		{"punycode domain", "a@xn--mnchen-3ya.de", ProviderClassCorporate},

		// Robustesse : jamais de panic, toujours corporate
		{"empty", "", ProviderClassCorporate},
		{"no at sign", "not-an-email", ProviderClassCorporate},
		{"trailing at", "jean@", ProviderClassCorporate},
		{"only at", "@", ProviderClassCorporate},
		{"only dot domain", "a@.", ProviderClassCorporate},
		{"whitespace only domain", "a@   ", ProviderClassCorporate},
		{"tab in email handled", "a@gmail.com\t", ProviderClassGoogle},
		{"double at takes last", "a@b@gmail.com", ProviderClassGoogle},
		{"domain with spaces", "a@ gmail.com ", ProviderClassGoogle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ClassifyProviderClass(tt.email))
		})
	}
}

func TestIsValidProviderClass(t *testing.T) {
	for _, valid := range []string{
		ProviderClassGoogle, ProviderClassMicrosoft, ProviderClassYahooAol,
		ProviderClassFreemailFR, ProviderClassCorporate,
	} {
		assert.True(t, IsValidProviderClass(valid), valid)
	}
	for _, invalid := range []string{"", "gmail", "Google", "GOOGLE", "outlook", "unknown"} {
		assert.False(t, IsValidProviderClass(invalid), invalid)
	}
}

func TestVeridianProviderClassRatesFromMetadata(t *testing.T) {
	t.Run("nil metadata", func(t *testing.T) {
		assert.Nil(t, VeridianProviderClassRatesFromMetadata(nil))
	})

	t.Run("missing key", func(t *testing.T) {
		assert.Nil(t, VeridianProviderClassRatesFromMetadata(MapOfAny{"other": 1}))
	})

	t.Run("json round-trip values (map[string]any, float64)", func(t *testing.T) {
		metadata := MapOfAny{
			VeridianProviderClassRatesMetadataKey: map[string]any{
				"google":    0.5,
				"microsoft": float64(2),
				"corporate": 30,
			},
		}
		rates := VeridianProviderClassRatesFromMetadata(metadata)
		require.NotNil(t, rates)
		assert.Equal(t, 0.5, rates["google"])
		assert.Equal(t, 2.0, rates["microsoft"])
		assert.Equal(t, 30.0, rates["corporate"])
	})

	t.Run("invalid classes and values ignored silently", func(t *testing.T) {
		metadata := MapOfAny{
			VeridianProviderClassRatesMetadataKey: map[string]any{
				"google":      1.0,
				"gmail":       5.0,       // classe non canonique
				"microsoft":   "fast",    // non numérique
				"yahoo_aol":   0.0,       // débit nul = ignoré
				"freemail_fr": -1.0,      // négatif = ignoré
				"corporate":   []any{42}, // type absurde
			},
		}
		rates := VeridianProviderClassRatesFromMetadata(metadata)
		require.NotNil(t, rates)
		assert.Equal(t, map[string]float64{"google": 1.0}, rates)
	})

	t.Run("all invalid yields nil (no class throttle)", func(t *testing.T) {
		metadata := MapOfAny{
			VeridianProviderClassRatesMetadataKey: map[string]any{"gmail": 5.0},
		}
		assert.Nil(t, VeridianProviderClassRatesFromMetadata(metadata))
	})

	t.Run("wrong container type yields nil", func(t *testing.T) {
		metadata := MapOfAny{VeridianProviderClassRatesMetadataKey: "google=5"}
		assert.Nil(t, VeridianProviderClassRatesFromMetadata(metadata))
	})

	t.Run("native map[string]float64 (constructions Go directes)", func(t *testing.T) {
		metadata := MapOfAny{
			VeridianProviderClassRatesMetadataKey: map[string]float64{
				"google": 1.5,
				"bogus":  3.0,
			},
		}
		rates := VeridianProviderClassRatesFromMetadata(metadata)
		assert.Equal(t, map[string]float64{"google": 1.5}, rates)
	})
}

func TestVeridianContactProviderClass(t *testing.T) {
	t.Run("nil contact", func(t *testing.T) {
		assert.Equal(t, "", VeridianContactProviderClass(nil))
	})

	t.Run("no custom_string_5", func(t *testing.T) {
		assert.Equal(t, "", VeridianContactProviderClass(&Contact{Email: "a@b.fr"}))
	})

	t.Run("null custom_string_5", func(t *testing.T) {
		c := &Contact{Email: "a@b.fr", CustomString5: &NullableString{IsNull: true}}
		assert.Equal(t, "", VeridianContactProviderClass(c))
	})

	t.Run("valid canonical tag", func(t *testing.T) {
		c := &Contact{Email: "a@b.fr", CustomString5: &NullableString{String: "google"}}
		assert.Equal(t, ProviderClassGoogle, VeridianContactProviderClass(c))
	})

	t.Run("tag normalized (case, spaces)", func(t *testing.T) {
		c := &Contact{Email: "a@b.fr", CustomString5: &NullableString{String: "  Microsoft "}}
		assert.Equal(t, ProviderClassMicrosoft, VeridianContactProviderClass(c))
	})

	t.Run("non-canonical tag rejected", func(t *testing.T) {
		c := &Contact{Email: "a@b.fr", CustomString5: &NullableString{String: "gmail"}}
		assert.Equal(t, "", VeridianContactProviderClass(c))
	})
}

func TestVeridianApplyProviderThrottle(t *testing.T) {
	rates := map[string]any{"google": 1.0, "corporate": 30.0}
	broadcastWithRates := &Broadcast{
		ID:       "b1",
		Metadata: MapOfAny{VeridianProviderClassRatesMetadataKey: rates},
	}

	t.Run("nil entry does not panic", func(t *testing.T) {
		VeridianApplyProviderThrottle(nil, broadcastWithRates, nil)
	})

	t.Run("no config leaves payload strictly untouched", func(t *testing.T) {
		entry := &EmailQueueEntry{Payload: EmailQueuePayload{Subject: "s"}}
		VeridianApplyProviderThrottle(entry, &Broadcast{ID: "b2"}, &Contact{Email: "a@b.fr"})
		assert.Empty(t, entry.Payload.VeridianProviderClass)
		assert.Nil(t, entry.Payload.VeridianProviderClassRates)
	})

	t.Run("nil broadcast with contact tag still sets class", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		contact := &Contact{Email: "a@b.fr", CustomString5: &NullableString{String: "google"}}
		VeridianApplyProviderThrottle(entry, nil, contact)
		assert.Equal(t, ProviderClassGoogle, entry.Payload.VeridianProviderClass)
		assert.Nil(t, entry.Payload.VeridianProviderClassRates)
	})

	t.Run("broadcast rates copied into payload", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		VeridianApplyProviderThrottle(entry, broadcastWithRates, nil)
		assert.Empty(t, entry.Payload.VeridianProviderClass)
		assert.Equal(t, map[string]float64{"google": 1.0, "corporate": 30.0},
			entry.Payload.VeridianProviderClassRates)
	})

	t.Run("contact tag and rates together", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		contact := &Contact{Email: "a@boitepro.fr", CustomString5: &NullableString{String: "microsoft"}}
		VeridianApplyProviderThrottle(entry, broadcastWithRates, contact)
		assert.Equal(t, ProviderClassMicrosoft, entry.Payload.VeridianProviderClass)
		assert.Equal(t, map[string]float64{"google": 1.0, "corporate": 30.0},
			entry.Payload.VeridianProviderClassRates)
	})

	t.Run("invalid contact tag not propagated", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		contact := &Contact{Email: "a@b.fr", CustomString5: &NullableString{String: "gmail"}}
		VeridianApplyProviderThrottle(entry, broadcastWithRates, contact)
		assert.Empty(t, entry.Payload.VeridianProviderClass)
	})
}
