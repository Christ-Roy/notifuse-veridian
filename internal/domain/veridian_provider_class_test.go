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
	// Les 5 historiques restent valides (non-régression stricte).
	for _, valid := range []string{
		ProviderClassGoogle, ProviderClassMicrosoft, ProviderClassYahooAol,
		ProviderClassFreemailFR, ProviderClassCorporate,
	} {
		assert.True(t, IsValidProviderClass(valid), valid)
	}
	// Les 6 nouvelles classes MX (Lot 4) sont désormais valides PARTOUT où
	// IsValidProviderClass garde l'entrée (rates, caps, pixel, tag contact).
	for _, valid := range []string{
		ProviderClassOVH, ProviderClassIonos, ProviderClassAppleICloud,
		ProviderClassSecurityGateway, ProviderClassOtherHoster, ProviderClassCorporateSelfhost,
	} {
		assert.True(t, IsValidProviderClass(valid), valid)
	}
	for _, invalid := range []string{"", "gmail", "Google", "GOOGLE", "outlook", "unknown", "Ovh", "selfhost"} {
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

	t.Run("daily caps copied into payload from metadata", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		b := &Broadcast{
			ID: "b3",
			Metadata: MapOfAny{
				VeridianProviderClassDailyCapMetadataKey: map[string]any{"google": float64(1), "microsoft": float64(5)},
				VeridianPerRecipientDailyCapMetadataKey:  float64(1),
			},
		}
		VeridianApplyProviderThrottle(entry, b, nil)
		assert.Equal(t, map[string]int{"google": 1, "microsoft": 5}, entry.Payload.VeridianProviderClassDailyCap)
		assert.Equal(t, 1, entry.Payload.VeridianPerRecipientDailyCap)
	})

	t.Run("no daily cap metadata leaves payload caps empty", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		VeridianApplyProviderThrottle(entry, broadcastWithRates, nil)
		assert.Nil(t, entry.Payload.VeridianProviderClassDailyCap)
		assert.Zero(t, entry.Payload.VeridianPerRecipientDailyCap)
	})

	t.Run("sending window copied into payload from metadata", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		b := &Broadcast{
			ID: "b4",
			Metadata: MapOfAny{
				VeridianSendingWindowMetadataKey: map[string]any{
					"days": []any{1.0, 2.0, 3.0, 4.0, 5.0}, "start_hour": 9.0, "end_hour": 18.0, "timezone": "Europe/Paris",
				},
			},
		}
		VeridianApplyProviderThrottle(entry, b, nil)
		require.NotNil(t, entry.Payload.VeridianSendingWindow)
		assert.Equal(t, 9, entry.Payload.VeridianSendingWindow.StartHour)
		assert.Equal(t, 18, entry.Payload.VeridianSendingWindow.EndHour)
		assert.Equal(t, "Europe/Paris", entry.Payload.VeridianSendingWindow.Timezone)
	})

	t.Run("invalid sending window metadata leaves payload window nil", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		b := &Broadcast{
			ID:       "b5",
			Metadata: MapOfAny{VeridianSendingWindowMetadataKey: map[string]any{"start_hour": 18.0, "end_hour": 9.0}}, // inversée
		}
		VeridianApplyProviderThrottle(entry, b, nil)
		assert.Nil(t, entry.Payload.VeridianSendingWindow)
	})

	t.Run("jitter pct copied into payload from metadata", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		b := &Broadcast{
			ID:       "b6",
			Metadata: MapOfAny{VeridianJitterPctMetadataKey: 0.5},
		}
		VeridianApplyProviderThrottle(entry, b, nil)
		require.NotNil(t, entry.Payload.VeridianJitterPct)
		assert.Equal(t, 0.5, *entry.Payload.VeridianJitterPct)
	})

	t.Run("jitter pct 0 explicit copied as *0 (disable, not nil)", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		b := &Broadcast{
			ID:       "b7",
			Metadata: MapOfAny{VeridianJitterPctMetadataKey: 0.0},
		}
		VeridianApplyProviderThrottle(entry, b, nil)
		require.NotNil(t, entry.Payload.VeridianJitterPct, "0 explicite doit être propagé comme *0, pas nil")
		assert.Equal(t, 0.0, *entry.Payload.VeridianJitterPct)
	})

	t.Run("no jitter metadata leaves payload jitter nil", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		VeridianApplyProviderThrottle(entry, broadcastWithRates, nil)
		assert.Nil(t, entry.Payload.VeridianJitterPct)
	})
}

func TestVeridianJitterPctFromMetadata(t *testing.T) {
	t.Run("nil metadata returns not-set", func(t *testing.T) {
		pct, ok := VeridianJitterPctFromMetadata(nil)
		assert.False(t, ok)
		assert.Zero(t, pct)
	})

	t.Run("missing key returns not-set", func(t *testing.T) {
		pct, ok := VeridianJitterPctFromMetadata(MapOfAny{"x": 1})
		assert.False(t, ok)
		assert.Zero(t, pct)
	})

	t.Run("float64 round-trip", func(t *testing.T) {
		pct, ok := VeridianJitterPctFromMetadata(MapOfAny{VeridianJitterPctMetadataKey: 0.3})
		assert.True(t, ok)
		assert.Equal(t, 0.3, pct)
	})

	t.Run("native int", func(t *testing.T) {
		pct, ok := VeridianJitterPctFromMetadata(MapOfAny{VeridianJitterPctMetadataKey: 1})
		assert.True(t, ok)
		assert.Equal(t, 1.0, pct)
	})

	t.Run("zero is a valid value (jitter disabled), present=true", func(t *testing.T) {
		pct, ok := VeridianJitterPctFromMetadata(MapOfAny{VeridianJitterPctMetadataKey: 0.0})
		assert.True(t, ok, "0 doit être considéré présent (disable), pas absent")
		assert.Zero(t, pct)
	})

	t.Run("negative dropped as not-set", func(t *testing.T) {
		pct, ok := VeridianJitterPctFromMetadata(MapOfAny{VeridianJitterPctMetadataKey: -0.5})
		assert.False(t, ok)
		assert.Zero(t, pct)
	})

	t.Run("non-numeric returns not-set", func(t *testing.T) {
		pct, ok := VeridianJitterPctFromMetadata(MapOfAny{VeridianJitterPctMetadataKey: "nope"})
		assert.False(t, ok)
		assert.Zero(t, pct)
	})
}

func TestVeridianProviderClassDailyCapFromMetadata(t *testing.T) {
	t.Run("nil metadata returns nil", func(t *testing.T) {
		assert.Nil(t, VeridianProviderClassDailyCapFromMetadata(nil))
	})

	t.Run("missing key returns nil", func(t *testing.T) {
		assert.Nil(t, VeridianProviderClassDailyCapFromMetadata(MapOfAny{"other": 1}))
	})

	t.Run("json round-trip float64 values", func(t *testing.T) {
		md := MapOfAny{VeridianProviderClassDailyCapMetadataKey: map[string]any{
			"google":    float64(50),
			"microsoft": float64(20),
		}}
		got := VeridianProviderClassDailyCapFromMetadata(md)
		assert.Equal(t, map[string]int{"google": 50, "microsoft": 20}, got)
	})

	t.Run("native map[string]int", func(t *testing.T) {
		md := MapOfAny{VeridianProviderClassDailyCapMetadataKey: map[string]int{"yahoo_aol": 10}}
		assert.Equal(t, map[string]int{"yahoo_aol": 10}, VeridianProviderClassDailyCapFromMetadata(md))
	})

	t.Run("non-canonical class and non-positive caps dropped", func(t *testing.T) {
		md := MapOfAny{VeridianProviderClassDailyCapMetadataKey: map[string]any{
			"google":      float64(1),
			"gmail":       float64(99), // non canonique → ignoré
			"corporate":   float64(0),  // <= 0 → ignoré
			"freemail_fr": float64(-3), // négatif → ignoré
		}}
		assert.Equal(t, map[string]int{"google": 1}, VeridianProviderClassDailyCapFromMetadata(md))
	})

	t.Run("all dropped returns nil", func(t *testing.T) {
		md := MapOfAny{VeridianProviderClassDailyCapMetadataKey: map[string]any{"gmail": float64(5)}}
		assert.Nil(t, VeridianProviderClassDailyCapFromMetadata(md))
	})
}

func TestVeridianPerRecipientDailyCapFromMetadata(t *testing.T) {
	t.Run("nil metadata returns 0", func(t *testing.T) {
		assert.Zero(t, VeridianPerRecipientDailyCapFromMetadata(nil))
	})

	t.Run("missing key returns 0", func(t *testing.T) {
		assert.Zero(t, VeridianPerRecipientDailyCapFromMetadata(MapOfAny{"x": 1}))
	})

	t.Run("float64 round-trip", func(t *testing.T) {
		md := MapOfAny{VeridianPerRecipientDailyCapMetadataKey: float64(1)}
		assert.Equal(t, 1, VeridianPerRecipientDailyCapFromMetadata(md))
	})

	t.Run("native int", func(t *testing.T) {
		md := MapOfAny{VeridianPerRecipientDailyCapMetadataKey: 3}
		assert.Equal(t, 3, VeridianPerRecipientDailyCapFromMetadata(md))
	})

	t.Run("zero or negative returns 0 (unlimited)", func(t *testing.T) {
		assert.Zero(t, VeridianPerRecipientDailyCapFromMetadata(MapOfAny{VeridianPerRecipientDailyCapMetadataKey: float64(0)}))
		assert.Zero(t, VeridianPerRecipientDailyCapFromMetadata(MapOfAny{VeridianPerRecipientDailyCapMetadataKey: float64(-1)}))
	})

	t.Run("non-numeric returns 0", func(t *testing.T) {
		assert.Zero(t, VeridianPerRecipientDailyCapFromMetadata(MapOfAny{VeridianPerRecipientDailyCapMetadataKey: "nope"}))
	})
}

func TestVeridianDomainsForClass(t *testing.T) {
	t.Run("google returns gmail domains, not excluded", func(t *testing.T) {
		domains, exclude := VeridianDomainsForClass(ProviderClassGoogle)
		assert.False(t, exclude)
		assert.Contains(t, domains, "gmail.com")
		assert.Contains(t, domains, "googlemail.com")
		// ne doit pas contenir un domaine d'une autre classe
		assert.NotContains(t, domains, "outlook.com")
	})

	t.Run("corporate returns ALL known domains, excluded", func(t *testing.T) {
		domains, exclude := VeridianDomainsForClass(ProviderClassCorporate)
		assert.True(t, exclude)
		// la liste corporate = tous les domaines connus à EXCLURE
		assert.Contains(t, domains, "gmail.com")
		assert.Contains(t, domains, "outlook.com")
		assert.Contains(t, domains, "orange.fr")
		assert.Greater(t, len(domains), 20)
	})

	t.Run("unknown class returns empty, not excluded", func(t *testing.T) {
		domains, exclude := VeridianDomainsForClass("not_a_class")
		assert.False(t, exclude)
		assert.Empty(t, domains)
	})

	t.Run("each known class returns only its own domains", func(t *testing.T) {
		ms, _ := VeridianDomainsForClass(ProviderClassMicrosoft)
		for _, d := range ms {
			assert.Equal(t, ProviderClassMicrosoft, ClassifyProviderClass("x@"+d), "domaine %s mal classé", d)
		}
	})

	t.Run("MX classes return empty (not suffix-backed) -> daily cap no-op", func(t *testing.T) {
		// Les classes MX (Lot 4) ne sont PAS adossées à la table de suffixes :
		// VeridianDomainsForClass renvoie une liste vide → le COUNT par domaine
		// du cap journalier est un no-op pour elles (dégradation gracieuse
		// documentée, le throttle minute protège la réputation sur le hot path).
		for _, c := range []string{
			ProviderClassOVH, ProviderClassIonos, ProviderClassAppleICloud,
			ProviderClassSecurityGateway, ProviderClassOtherHoster, ProviderClassCorporateSelfhost,
		} {
			domains, exclude := VeridianDomainsForClass(c)
			assert.Empty(t, domains, "classe MX %s ne doit avoir aucun domaine de suffixe", c)
			assert.False(t, exclude, "classe MX %s ne doit pas être en mode exclusion", c)
		}
	})
}

// TestVeridianAllProviderClasses : la liste énumère EXACTEMENT les 11 classes
// canoniques (5 historiques + 6 MX), toutes valides, sans doublon, et les 5
// historiques en tête (ordre déterministe pour un rendu reproductible).
func TestVeridianAllProviderClasses(t *testing.T) {
	all := VeridianAllProviderClasses()
	require.Len(t, all, 11)

	seen := map[string]bool{}
	for _, c := range all {
		assert.True(t, IsValidProviderClass(c), "classe %q non canonique", c)
		assert.False(t, seen[c], "doublon %q dans la liste", c)
		seen[c] = true
	}

	// Les 5 historiques d'abord, dans l'ordre attendu (non-régression d'ordre).
	assert.Equal(t, []string{
		ProviderClassGoogle, ProviderClassMicrosoft, ProviderClassYahooAol,
		ProviderClassFreemailFR, ProviderClassCorporate,
	}, all[:5])

	// Les 6 MX présentes.
	for _, mx := range []string{
		ProviderClassOVH, ProviderClassIonos, ProviderClassAppleICloud,
		ProviderClassSecurityGateway, ProviderClassOtherHoster, ProviderClassCorporateSelfhost,
	} {
		assert.Contains(t, all, mx)
	}
}

// TestClassifyProviderClassStaysPure verrouille la NON-RÉGRESSION de la fonction
// PURE après l'ajout de la couche MX : ClassifyProviderClass(email) ne fait
// JAMAIS de lookup et retombe sur `corporate` (PAS corporate_selfhost, qui est
// le fallback de la couche MX) pour tout domaine inconnu par suffixe. C'est ce
// que tous les call-sites historiques attendent.
func TestClassifyProviderClassStaysPure(t *testing.T) {
	// Domaines inconnus par suffixe → corporate (jamais corporate_selfhost ici).
	for _, email := range []string{
		"contact@cabinet-dupont.fr", // hébergé M365 en vrai, mais la fonction pure l'ignore
		"info@acme-corp.com",
		"x@une-pme-quelconque.io",
		"", "not-an-email", "jean@",
	} {
		got := ClassifyProviderClass(email)
		assert.Equal(t, ProviderClassCorporate, got, "%q doit rester corporate (pas corporate_selfhost)", email)
		assert.NotEqual(t, ProviderClassCorporateSelfhost, got)
	}
	// Suffixes connus inchangés (échantillon).
	assert.Equal(t, ProviderClassGoogle, ClassifyProviderClass("a@gmail.com"))
	assert.Equal(t, ProviderClassMicrosoft, ClassifyProviderClass("a@outlook.fr"))
}
