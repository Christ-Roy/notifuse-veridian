package domain

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Les plafonds et débits de la fiche 69 (19/10 et 22/10) : le passage en données ne change
// aucune valeur. Ce tableau est LE témoin : si le JSON bouge, ce test le dit.
var fiche69Golden = map[string]struct {
	cap  int
	rate float64
}{
	ProviderClassGoogle: {30, 0.3}, ProviderClassMicrosoft: {30, 0.3}, ProviderClassYahooAol: {10, 0.1},
	ProviderClassFreemailFR: {20, 0.2}, ProviderClassCorporate: {30, 0.3}, ProviderClassOVH: {40, 0.4},
	ProviderClassIonos: {20, 0.2}, ProviderClassAppleICloud: {10, 0.1}, ProviderClassSecurityGateway: {10, 0.1},
	ProviderClassOtherHoster: {30, 0.3}, ProviderClassCorporateSelfhost: {30, 0.3}, ProviderClassInfomaniak: {25, 0.25},
	ProviderClassGandi: {25, 0.25}, ProviderClassHostinger: {25, 0.25}, ProviderClassO2switch: {25, 0.25},
	ProviderClassLWS: {25, 0.25}, ProviderClassScaleway: {25, 0.25}, ProviderClassWebsiteBuilder: {20, 0.2},
	ProviderClassZoho: {20, 0.2}, ProviderClassProton: {10, 0.1}, ProviderClassOrange: {10, 0.1},
	ProviderClassSFRAltice: {20, 0.2}, ProviderClassFree: {20, 0.2}, ProviderClassLaPoste: {20, 0.2},
	ProviderClassBouygues: {20, 0.2},
}

func TestClassPresets_ReproduceLesDefautsDeLaFiche69(t *testing.T) {
	require.Len(t, fiche69Golden, len(VeridianAllProviderClasses()), "le témoin couvre toutes les classes")
	for class, want := range fiche69Golden {
		assert.Equal(t, want.cap, VeridianDefaultCapForClass(class), "plafond %s", class)
		assert.InDelta(t, want.rate, VeridianDefaultRateForClass(class), 1e-9, "débit %s", class)
		p, ok := VeridianClassPresetFor(class)
		require.True(t, ok, class)
		assert.Equal(t, want.cap, p.DailyCap, class)
	}
}

func TestClassPresets_CatalogueEmbarqueEstValideEtComplet(t *testing.T) {
	cat := VeridianClassPresets()
	assert.Len(t, cat.Classes, len(VeridianAllProviderClasses()))
	assert.NotEmpty(t, cat.AsOf)
	assert.NotEmpty(t, cat.Source)
	assert.Greater(t, cat.ReplyPrior.Sent, 0)
	for _, p := range cat.Classes {
		assert.NotEmpty(t, p.Label, p.Class)
		assert.NotEmpty(t, p.Why, p.Class)
		assert.LessOrEqual(t, p.Observed.RepliedHuman, p.Observed.Sent, p.Class)
		assert.LessOrEqual(t, p.Observed.Bounced, p.Observed.Sent, p.Class)
		// Les français ne doivent pas contenir de tiret cadratin entouré d'espaces.
		assert.NotContains(t, p.Why, " — ", p.Class)
		assert.NotContains(t, p.Label, " — ", p.Class)
	}
}

func presetsJSONWith(t *testing.T, mutate func(c *VeridianClassPresetCatalog)) []byte {
	t.Helper()
	var c VeridianClassPresetCatalog
	require.NoError(t, json.Unmarshal(veridianClassPresetsJSON, &c))
	mutate(&c)
	b, err := json.Marshal(c)
	require.NoError(t, err)
	return b
}

func TestParseClassPresets_RefuseLesCataloguesInvalides(t *testing.T) {
	cases := map[string]func(c *VeridianClassPresetCatalog){
		"classe manquante":        func(c *VeridianClassPresetCatalog) { c.Classes = c.Classes[1:] },
		"classe inconnue":         func(c *VeridianClassPresetCatalog) { c.Classes[0].Class = "inconnue" },
		"classe en double":        func(c *VeridianClassPresetCatalog) { c.Classes[1].Class = c.Classes[0].Class },
		"plafond nul":             func(c *VeridianClassPresetCatalog) { c.Classes[0].DailyCap = 0 },
		"plafond hors borne":      func(c *VeridianClassPresetCatalog) { c.Classes[0].DailyCap = 5000 },
		"débit nul":               func(c *VeridianClassPresetCatalog) { c.Classes[0].RatePerMin = 0 },
		"débit hors borne":        func(c *VeridianClassPresetCatalog) { c.Classes[0].RatePerMin = 50 },
		"part de chauffe nulle":   func(c *VeridianClassPresetCatalog) { c.Classes[0].WarmupShare = 0 },
		"part de chauffe > 1":     func(c *VeridianClassPresetCatalog) { c.Classes[0].WarmupShare = 1.5 },
		"seuil sous la borne":     func(c *VeridianClassPresetCatalog) { c.Classes[0].SlowdownThreshold = 0.001 },
		"seuil au-dessus":         func(c *VeridianClassPresetCatalog) { c.Classes[0].SlowdownThreshold = 0.5 },
		"explication vide":        func(c *VeridianClassPresetCatalog) { c.Classes[0].Why = " " },
		"poids de lissage absent": func(c *VeridianClassPresetCatalog) { c.ReplySmoothingWeight = 0 },
	}
	for name, mutate := range cases {
		_, err := VeridianParseClassPresets(presetsJSONWith(t, mutate))
		assert.Error(t, err, name)
	}
	_, err := VeridianParseClassPresets([]byte("{"))
	assert.Error(t, err)
	_, err = VeridianParseClassPresets(presetsJSONWith(t, func(*VeridianClassPresetCatalog) {}))
	assert.NoError(t, err, "le catalogue intact passe")
}

func TestValidateClassPolicyValues_Bornes(t *testing.T) {
	assert.NoError(t, VeridianValidateClassPolicyValues(0, 0, 0, 0), "rien de posé est accepté")
	assert.NoError(t, VeridianValidateClassPolicyValues(30, 0.3, 0.5, 0.03))
	assert.Error(t, VeridianValidateClassPolicyValues(-1, 0, 0, 0))
	assert.Error(t, VeridianValidateClassPolicyValues(1001, 0, 0, 0))
	assert.Error(t, VeridianValidateClassPolicyValues(0, 0.001, 0, 0))
	assert.Error(t, VeridianValidateClassPolicyValues(0, 11, 0, 0))
	assert.Error(t, VeridianValidateClassPolicyValues(0, 0, 1.01, 0))
	assert.Error(t, VeridianValidateClassPolicyValues(0, 0, 0.001, 0))
	assert.Error(t, VeridianValidateClassPolicyValues(0, 0, 0, 0.16))
	assert.Error(t, VeridianValidateClassPolicyValues(0, 0, 0, 0.005))
}

type fakePolicyProvider struct{ snap *VeridianPolicySnapshot }

func (f fakePolicyProvider) PolicyFor(string) *VeridianPolicySnapshot { return f.snap }

func TestPolicyFor_SansFournisseurLesPresetsSontLaPolitique(t *testing.T) {
	t.Cleanup(func() { SetVeridianPolicyProvider(nil) })
	SetVeridianPolicyProvider(nil)
	s := VeridianPolicyFor("ws-neuf")
	require.NotNil(t, s)
	assert.Equal(t, 30, s.Caps[ProviderClassGoogle])
	assert.Equal(t, 0.02, s.Classes[ProviderClassGoogle].SlowdownThreshold)
	assert.Equal(t, 0.5, s.Classes[ProviderClassGoogle].WarmupShare)
	assert.Empty(t, s.Overridden, "aucune surcharge sur un workspace neuf")
}

func TestPolicyFor_LeFournisseurPrimeEtNilRetombeSurLesPresets(t *testing.T) {
	t.Cleanup(func() { SetVeridianPolicyProvider(nil) })
	custom := &VeridianPolicySnapshot{Caps: map[string]int{ProviderClassGoogle: 7}}
	SetVeridianPolicyProvider(fakePolicyProvider{snap: custom})
	assert.Equal(t, 7, VeridianPolicyFor("ws").Caps[ProviderClassGoogle])
	SetVeridianPolicyProvider(fakePolicyProvider{snap: nil})
	assert.Equal(t, 30, VeridianPolicyFor("ws").Caps[ProviderClassGoogle], "un fournisseur qui rend nil ne casse rien")
}

func TestPolicyFor_AccesConcurrent(t *testing.T) {
	t.Cleanup(func() { SetVeridianPolicyProvider(nil) })
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				SetVeridianPolicyProvider(fakePolicyProvider{snap: VeridianPresetSnapshot()})
			}
			_ = VeridianPolicyFor("ws")
		}(i)
	}
	wg.Wait()
}

func TestSendPriority_Validation(t *testing.T) {
	for _, ok := range []int{2, 5, 9} {
		assert.NoError(t, VeridianValidateMarketingPriority(ok), ok)
	}
	for _, ko := range []int{-1, 0, 1, 10, 100} {
		err := VeridianValidateMarketingPriority(ko)
		require.Error(t, err, ko)
		assert.True(t, strings.Contains(err.Error(), "2 et 9"), err.Error())
	}
}

type fakePrioProvider struct{ p int }

func (f fakePrioProvider) SendPriorityFor(string, EmailQueueSourceType, string) int { return f.p }

func TestSendPriority_DefautCinqEtGardeFouDePlage(t *testing.T) {
	t.Cleanup(func() { SetVeridianSendPriorityProvider(nil) })
	SetVeridianSendPriorityProvider(nil)
	assert.Equal(t, 5, VeridianSendPriority("ws", EmailQueueSourceBroadcast, "b1"), "sans fournisseur : 5")
	SetVeridianSendPriorityProvider(fakePrioProvider{p: 0})
	assert.Equal(t, 5, VeridianSendPriority("ws", EmailQueueSourceBroadcast, "b1"), "0 = non posé : 5")
	SetVeridianSendPriorityProvider(fakePrioProvider{p: 2})
	assert.Equal(t, 2, VeridianSendPriority("ws", EmailQueueSourceAutomation, "a1"))
	SetVeridianSendPriorityProvider(fakePrioProvider{p: 1})
	assert.Equal(t, 5, VeridianSendPriority("ws", EmailQueueSourceBroadcast, "b1"), "1 est réservé au transactionnel : jamais atteint par du marketing")
	SetVeridianSendPriorityProvider(fakePrioProvider{p: 42})
	assert.Equal(t, 5, VeridianSendPriority("ws", EmailQueueSourceBroadcast, "b1"), "donnée corrompue : 5")
}
