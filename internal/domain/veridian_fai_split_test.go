package domain

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scission de freemail_fr en FAI (22/10/2026, fiche 69).

var veridianFAIClasses = []string{
	ProviderClassOrange, ProviderClassSFRAltice, ProviderClassFree,
	ProviderClassLaPoste, ProviderClassBouygues,
}

// Chaque FAI a ses défauts, jamais au-dessus de ceux de freemail_fr ; Orange est le plus bas.
func TestFAIClasses_DefaultsNeverAboveLegacyFreemail(t *testing.T) {
	legacyCap := VeridianDefaultCapForClass(ProviderClassFreemailFR)
	legacyRate := VeridianDefaultRateForClass(ProviderClassFreemailFR)
	for _, c := range veridianFAIClasses {
		assert.True(t, IsValidProviderClass(c), c)
		assert.LessOrEqual(t, VeridianDefaultCapForClass(c), legacyCap, c)
		assert.LessOrEqual(t, VeridianDefaultRateForClass(c), legacyRate+1e-9, c)
	}
	assert.Less(t, VeridianDefaultCapForClass(ProviderClassOrange), legacyCap)
	assert.Less(t, VeridianDefaultRateForClass(ProviderClassOrange), legacyRate)
}

// Domaine -> FAI, MX -> FAI : la famille complète de la demande de Robert.
func TestFAIClasses_SuffixAndMXMapping(t *testing.T) {
	for domainName, want := range map[string]string{
		"orange.fr": ProviderClassOrange, "wanadoo.fr": ProviderClassOrange, "orange-business.com": ProviderClassOrange,
		"sfr.fr": ProviderClassSFRAltice, "neuf.fr": ProviderClassSFRAltice, "numericable.fr": ProviderClassSFRAltice,
		"sfr.net": ProviderClassSFRAltice, "altice.net": ProviderClassSFRAltice,
		"free.fr": ProviderClassFree, "laposte.net": ProviderClassLaPoste, "laposte.fr": ProviderClassLaPoste,
		"bbox.fr": ProviderClassBouygues,
	} {
		assert.Equal(t, want, ClassifyProviderClass("x@"+domainName), domainName)
	}
	for host, want := range map[string]string{
		"smtp-in.orange.fr": ProviderClassOrange, "mx.mailbox.orange-business.com": ProviderClassOrange,
		"smtp-in.sfr.fr": ProviderClassSFRAltice, "mx.zrh.net.altice.net": ProviderClassSFRAltice,
		"mx2.free.fr": ProviderClassFree, "smtpz4.laposte.net": ProviderClassLaPoste,
		"mx-b.bbox.fr": ProviderClassBouygues,
		// une passerelle devant un MX FAI reste une passerelle (ordre de la table)
		"mxa-0071d001.gslb.pphosted.com": ProviderClassSecurityGateway,
	} {
		got, ok := classifyMXHost(host)
		assert.True(t, ok, host)
		assert.Equal(t, want, got, host)
	}
}

// Tag historique freemail_fr : éclaté par le domaine, sans I/O ; hors FAI il reste freemail_fr.
func TestFAIClasses_LegacyTagIsSplit(t *testing.T) {
	mk := func(email string) *Contact {
		return &Contact{Email: email, CustomString5: &NullableString{String: "freemail_fr"}}
	}
	assert.Equal(t, ProviderClassOrange, VeridianContactProviderClass(mk("a@orange.fr")))
	assert.Equal(t, ProviderClassSFRAltice, VeridianContactProviderClass(mk("a@sfr.fr")))
	assert.Equal(t, ProviderClassFree, VeridianContactProviderClass(mk("A@FREE.FR")))
	assert.Equal(t, ProviderClassLaPoste, VeridianContactProviderClass(mk("a@laposte.net")))
	assert.Equal(t, ProviderClassBouygues, VeridianContactProviderClass(mk("a@bbox.fr")))
	assert.Equal(t, ProviderClassFreemailFR, VeridianContactProviderClass(mk("a@cabinet-pro.fr")))
	assert.Equal(t, ProviderClassFreemailFR, VeridianContactProviderClass(mk("a@gmx.fr")))
	// jamais vers google/microsoft sur un tag périmé
	assert.Equal(t, ProviderClassFreemailFR, VeridianContactProviderClass(mk("a@gmail.com")))
	// un tag déjà fin est respecté tel quel
	c := &Contact{Email: "a@orange.fr", CustomString5: &NullableString{String: "free"}}
	assert.Equal(t, ProviderClassFree, VeridianContactProviderClass(c))
}

// Les surcharges posées sur freemail_fr avant la scission continuent de s'appliquer aux FAI.
func TestFAIClasses_InheritFreemailOverrides(t *testing.T) {
	rates := map[string]float64{ProviderClassFreemailFR: 0.05}
	caps := map[string]int{ProviderClassFreemailFR: 7}
	for _, c := range veridianFAIClasses {
		r, ok := VeridianRateForClass(rates, c)
		assert.True(t, ok, c)
		assert.InDelta(t, 0.05, r, 1e-9, c)
		k, ok := VeridianCapForClass(caps, c)
		assert.True(t, ok, c)
		assert.Equal(t, 7, k, c)
	}
	// une entrée propre prime
	k, _ := VeridianCapForClass(map[string]int{ProviderClassFreemailFR: 7, ProviderClassOrange: 3}, ProviderClassOrange)
	assert.Equal(t, 3, k)
	// et ne déborde pas sur les classes d'autres familles
	k, _ = VeridianCapForClass(caps, ProviderClassGoogle)
	assert.Equal(t, VeridianDefaultCapForClass(ProviderClassGoogle), k)
	// pixel : l'override freemail_fr vaut pour les FAI, pas pour other_hoster -> ses filles
	pix := map[string]bool{ProviderClassFreemailFR: false, ProviderClassOtherHoster: false}
	b, ok := veridianPixelLookup(pix, ProviderClassOrange)
	assert.True(t, ok)
	assert.False(t, b)
	_, ok = veridianPixelLookup(pix, ProviderClassGandi)
	assert.False(t, ok, "comportement des classes filles d'other_hoster inchangé")
}

// Une exclusion de freemail_fr (politique pilote) exclut les FAI : sans cela, un contact
// tagué freemail_fr et désormais classé orange passerait l'exclusion.
func TestFAIClasses_ExclusionOfFreemailCoversFAI(t *testing.T) {
	ex := VeridianResolveExcludedClasses(nil, &EmailProvider{VeridianExcludedProviderClasses: []string{ProviderClassFreemailFR}}, nil)
	assert.True(t, ex[ProviderClassFreemailFR])
	for _, c := range veridianFAIClasses {
		assert.True(t, ex[c], c)
	}
	ex = VeridianResolveExcludedClasses(nil, &EmailProvider{VeridianExcludedProviderClasses: []string{ProviderClassOrange}}, nil)
	assert.True(t, ex[ProviderClassOrange])
	assert.False(t, ex[ProviderClassFree])
	assert.False(t, ex[ProviderClassFreemailFR])
}

// Les domaines de chaque classe (compte journalier par domaine) sont disjoints ; le
// résidu freemail_fr ne garde que ce qui n'a pas de famille FAI.
func TestFAIClasses_DomainsPerClass(t *testing.T) {
	d, exclude := VeridianDomainsForClass(ProviderClassOrange)
	assert.False(t, exclude)
	assert.ElementsMatch(t, []string{"orange.fr", "wanadoo.fr", "orange-business.com"}, d)
	d, _ = VeridianDomainsForClass(ProviderClassFreemailFR)
	assert.ElementsMatch(t, []string{"gmx.fr", "dartybox.com"}, d)
	d, _ = VeridianDomainsForClass(ProviderClassFree)
	assert.Contains(t, d, "free.fr")
	assert.NotContains(t, d, "orange.fr")
}

// La table MX exportée porte les FAI (copie lue par ODH) et aucun suffixe FAI ne mène encore à freemail_fr.
func TestFAIClasses_ExportedTableCarriesThem(t *testing.T) {
	raw, err := os.ReadFile("testdata/veridian_mx_table.json")
	require.NoError(t, err)
	var tbl struct {
		Classes []string `json:"classes"`
		Rules   []struct {
			Suffix string `json:"suffix"`
			Class  string `json:"class"`
		} `json:"rules"`
	}
	require.NoError(t, json.Unmarshal(raw, &tbl))
	for _, c := range veridianFAIClasses {
		assert.Contains(t, tbl.Classes, c)
		found := false
		for _, r := range tbl.Rules {
			if r.Class == c {
				found = true
			}
		}
		assert.True(t, found, "aucune règle MX pour %s", c)
	}
	for _, r := range tbl.Rules {
		assert.NotEqual(t, ProviderClassFreemailFR, r.Class, "suffixe %s encore sur freemail_fr", r.Suffix)
		assert.True(t, strings.HasPrefix(r.Suffix, ".") || r.Suffix != "", r.Suffix)
	}
}
