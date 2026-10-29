package queue

import (
	"fmt"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lot L3 (29/10/2026) : la politique de classe en données (surcharge du workspace, sinon
// preset) arrive aux portes du worker par les crochets de veridian_policy_hook.go.

type testPolicyProvider struct {
	snap *domain.VeridianPolicySnapshot
}

func (p testPolicyProvider) PolicyFor(string) *domain.VeridianPolicySnapshot { return p.snap }

// withPolicy branche un fournisseur de test pour la durée du test.
func withPolicy(t *testing.T, caps map[string]int, rates map[string]float64, classes map[string]domain.VeridianClassPolicy, over map[string]domain.VeridianPolicyOverridden) {
	t.Helper()
	base := domain.VeridianPresetSnapshot()
	snap := &domain.VeridianPolicySnapshot{Classes: map[string]domain.VeridianClassPolicy{}, Caps: map[string]int{}, Rates: map[string]float64{}, Overridden: over}
	for k, v := range base.Classes {
		snap.Classes[k] = v
	}
	for k, v := range base.Caps {
		snap.Caps[k] = v
	}
	for k, v := range base.Rates {
		snap.Rates[k] = v
	}
	for k, v := range caps {
		snap.Caps[k] = v
	}
	for k, v := range rates {
		snap.Rates[k] = v
	}
	for k, v := range classes {
		snap.Classes[k] = v
	}
	domain.SetVeridianPolicyProvider(testPolicyProvider{snap: snap})
	t.Cleanup(func() { domain.SetVeridianPolicyProvider(nil) })
}

func TestPolicyHook_SansConfigLeWorkspaceNeufRecoitLesPresets(t *testing.T) {
	enableVeridianClassDefaults(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	caps := veridianPolicyMergeCaps(ws, nil)
	rates := veridianPolicyMergeRates(ws, nil)
	for _, p := range domain.VeridianClassPresets().Classes {
		assert.Equal(t, p.DailyCap, caps[p.Class], "plafond %s", p.Class)
		assert.InDelta(t, p.RatePerMin, rates[p.Class], 1e-9, "débit %s", p.Class)
	}
}

func TestPolicyHook_SurchargeDuWorkspacePrimeSurLePreset(t *testing.T) {
	enableVeridianClassDefaults(t)
	withPolicy(t, map[string]int{"ovh": 7}, map[string]float64{"ovh": 0.05}, nil,
		map[string]domain.VeridianPolicyOverridden{"ovh": {DailyCap: true, RatePerMin: true}})
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	assert.Equal(t, 7, veridianPolicyMergeCaps(ws, nil)["ovh"])
	assert.InDelta(t, 0.05, veridianPolicyMergeRates(ws, nil)["ovh"], 1e-9)
	assert.Equal(t, 30, veridianPolicyMergeCaps(ws, nil)["google"], "les autres classes gardent leur preset")
}

func TestPolicyHook_LaResolutionDesDebitsDuWorkerPasseParLeCrochet(t *testing.T) {
	enableVeridianClassDefaults(t)
	withPolicy(t, nil, map[string]float64{"ovh": 0.05}, nil, map[string]domain.VeridianPolicyOverridden{"ovh": {RatePerMin: true}})
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	entry := veridianTestEntryFrom("e1", "a@x.example", "bot@send.fr", domain.EmailQueuePayload{})
	rates := veridianResolveProviderClassRates(ws, nil, entry)
	assert.InDelta(t, 0.05, rates["ovh"], 1e-9, "la porte de débit et EffectivePlan lisent la surcharge du workspace")
	assert.InDelta(t, 0.3, rates["google"], 1e-9)
	// Un profil qui pose ses débits garde la main sur les classes qu'il nomme.
	rates = veridianResolveProviderClassRates(ws, &domain.EmailProvider{VeridianProviderClassRates: map[string]float64{"ovh": 0.9}}, entry)
	assert.InDelta(t, 0.9, rates["ovh"], 1e-9)
	assert.InDelta(t, 0.3, rates["google"], 1e-9, "classe non nommée par le profil : preset")
}

func TestPolicyHook_LePlusSpecifiqueNestJamaisEcrase(t *testing.T) {
	enableVeridianClassDefaults(t)
	withPolicy(t, map[string]int{"ovh": 7}, nil, nil, map[string]domain.VeridianPolicyOverridden{"ovh": {DailyCap: true}})
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	// Un profil pose google: 3 et ovh: 99 à la main : ses valeurs priment sur la surcharge et le preset.
	merged := veridianPolicyMergeCaps(ws, map[string]int{"google": 3, "ovh": 99})
	assert.Equal(t, 3, merged["google"])
	assert.Equal(t, 99, merged["ovh"])
	assert.Equal(t, 20, merged["ionos"], "classe sans valeur propre : preset")
	// La table d'entrée n'est jamais modifiée.
	in := map[string]int{"google": 3}
	_ = veridianPolicyMergeCaps(ws, in)
	assert.Len(t, in, 1)
}

func TestPolicyHook_LHeritageDeLaClasseParenteResteAvantLePreset(t *testing.T) {
	enableVeridianClassDefaults(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	// Surcharge posée sur freemail_fr : les FAI en héritent (fiche 69), le preset de « free » ne la masque pas.
	merged := veridianPolicyMergeCaps(ws, map[string]int{domain.ProviderClassFreemailFR: 5})
	capV, ok := domain.VeridianCapForClass(merged, domain.ProviderClassFree)
	require.True(t, ok)
	assert.Equal(t, 5, capV)
	capV, _ = domain.VeridianCapForClass(merged, domain.ProviderClassOrange)
	assert.Equal(t, 5, capV)
	mergedRates := veridianPolicyMergeRates(ws, map[string]float64{domain.ProviderClassFreemailFR: 0.07})
	r, _ := domain.VeridianRateForClass(mergedRates, domain.ProviderClassLaPoste)
	assert.InDelta(t, 0.07, r, 1e-9)
}

func TestPolicyHook_CoupeAvecLeFlagHistorique(t *testing.T) {
	// veridianClassDefaultsEnabled est faux dans la suite historique : les crochets ne changent RIEN.
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	in := map[string]int{"google": 3}
	assert.Equal(t, in, veridianPolicyMergeCaps(ws, in))
	assert.Nil(t, veridianPolicyMergeRates(ws, nil))
	assert.Equal(t, 9, veridianPolicyWarmupClamp(ws, nil, "google", 20, 9))
}

func TestPolicyHook_ChauffeBorneLaClasseSeulementSiLePlafondNestPasPoseAMain(t *testing.T) {
	enableVeridianClassDefaults(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	// google : part 0,5. Chauffe du jour 20 -> 10 mails au plus, sous le plafond de classe 30.
	assert.Equal(t, 10, veridianPolicyWarmupClamp(ws, nil, "google", 20, 30))
	// Passerelle anti-spam : part 0,05 -> au moins 1.
	assert.Equal(t, 1, veridianPolicyWarmupClamp(ws, nil, "security_gateway", 10, 10))
	// Pas de chauffe : inchangé.
	assert.Equal(t, 30, veridianPolicyWarmupClamp(ws, nil, "google", 0, 30))
	// La borne ne relève jamais un plafond plus bas.
	assert.Equal(t, 4, veridianPolicyWarmupClamp(ws, nil, "google", 20, 4))
	// Plafond posé à la main sur le profil/workspace : l'opérateur a décidé.
	assert.Equal(t, 30, veridianPolicyWarmupClamp(ws, map[string]int{"google": 30}, "google", 20, 30))
	// Plafond surchargé par policy.set : idem.
	withPolicy(t, map[string]int{"google": 30}, nil, nil, map[string]domain.VeridianPolicyOverridden{"google": {DailyCap: true}})
	assert.Equal(t, 30, veridianPolicyWarmupClamp(ws, nil, "google", 20, 30))
}

func TestPolicyHook_ResolveCapLimitsAppliqueLePresetEtLaChauffe(t *testing.T) {
	enableVeridianClassDefaults(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	started := time.Now()
	provider := &domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, VeridianWarmupStartedAt: &started, VeridianWarmupSchedule: []int{20}}
	entry := veridianTestEntryFrom("e1", "a@gmail.com", "bot@send.fr", domain.EmailQueuePayload{})

	lim := veridianResolveCapLimits(ws, provider, entry, func() string { return "google" }, nil, time.Now())
	require.Equal(t, 20, lim.Warmup, "la chauffe est active")
	assert.Equal(t, 30, lim.ClassBase, "plafond configuré = preset google")
	assert.Equal(t, 10, lim.ClassCap, "pendant la chauffe : 50 % de 20")

	// Sans chauffe : plafond plein.
	lim = veridianResolveCapLimits(ws, &domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}, entry, func() string { return "google" }, nil, time.Now())
	assert.Equal(t, 30, lim.ClassCap)

	// Le ralentissement du fusible s'applique APRÈS la borne de chauffe.
	lim = veridianResolveCapLimits(ws, provider, entry, func() string { return "google" }, func(string) int { return 2 }, time.Now())
	assert.Equal(t, 5, lim.ClassCap)
}

func TestPolicyHook_PorteDePlafondUtiliseLaSurchargeDuWorkspace(t *testing.T) {
	enableVeridianClassDefaults(t)
	withPolicy(t, map[string]int{"google": 4}, nil, nil, map[string]domain.VeridianPolicyOverridden{"google": {DailyCap: true}})
	worker, fakeRepo := newVeridianDailyCapFakeEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0) // aucune configuration historique
	blocked := 0
	for i := 0; i < 10; i++ {
		entry := veridianTestEntryFrom(fmt.Sprintf("e%d", i), fmt.Sprintf("lead%d@x.example", i), "bot@send.fr",
			domain.EmailQueuePayload{VeridianProviderClass: "google"})
		if _, capped := worker.veridianDailyCapGate(ws, nil, entry); capped {
			blocked++
			continue
		}
		fakeRepo.record("google", "send.fr", entry.ContactEmail, time.Now().UTC())
	}
	assert.Equal(t, 6, blocked, "10 tentatives, plafond posé à 4 par policy.set : 6 refusées (le preset en aurait laissé passer 30)")
}

func TestPolicyHook_SeuilDeLaClasseSinonDuProfil(t *testing.T) {
	enableVeridianClassDefaults(t)
	p := &domain.EmailProvider{}
	assert.Equal(t, 0.02, veridianPolicyThreshold("ws-1", p, "google"), "preset google")
	assert.Equal(t, 0.03, veridianPolicyThreshold("ws-1", p, "ovh"), "preset ovh")
	assert.Equal(t, 0.03, veridianPolicyThreshold("ws-1", p, "classe_inconnue"), "défaut du fusible")
	assert.Equal(t, 0.03, veridianPolicyThreshold("ws-1", p, ""), "sans classe : défaut du fusible")
	assert.Equal(t, 0.03, veridianPolicyThreshold("", p, "google"), "sans workspace : défaut du fusible")
	assert.Equal(t, 0.03, veridianPolicyThreshold("ws-1", nil, "ovh"), "profil nil sans panique")
	// Le profil qui pose SON seuil l'emporte sur la classe.
	custom := &domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}
	assert.Equal(t, 0.08, veridianPolicyThreshold("ws-1", custom, "google"))
	// Surcharge du workspace.
	withPolicy(t, nil, nil, map[string]domain.VeridianClassPolicy{"ovh": {SlowdownThreshold: 0.05}}, nil)
	assert.Equal(t, 0.05, veridianPolicyThreshold("ws-1", p, "ovh"))
}

func TestPolicyHook_LeFusibleReagitSelonLeSeuilDeLaClasse(t *testing.T) {
	enableVeridianClassDefaults(t)
	env := newVeridianThrottleTestEnv(t)
	// 80 envois, 2 rebonds durs = 2,5 %. Google : seuil 2 % -> ralenti ÷2. OVH : seuil 3 % -> normal.
	veridianExpectRepCounts(env, "agence-veridian.fr", 0, 160, map[string]domain.VeridianReputationCounts{
		"google": {Sent: 80, HardBounces: 2},
		"ovh":    {Sent: 80, HardBounces: 2},
	})
	status, err := VeridianComputeReputationStatus(env.worker.ctx, env.mockMessageHistoryRepo, "ws-1", "agence-veridian.fr", nil, time.Now())
	require.NoError(t, err)
	byName := map[string]domain.VeridianReputationClassStatus{}
	for _, c := range status.Classes {
		byName[c.Class] = c
	}
	assert.Equal(t, 2, byName["google"].Factor)
	assert.Equal(t, 0.02, byName["google"].Threshold)
	assert.Equal(t, 1, byName["ovh"].Factor)
	assert.Equal(t, 0.03, byName["ovh"].Threshold)

	// Le gate rejoue les mêmes règles : cohérence écran / worker.
	gws := &domain.Workspace{ID: "ws-1"}
	_, _ = env.worker.veridianReputationGate(gws, nil, veridianReputationTestEntryClass("a@agence-veridian.fr", "google"))
	_, _ = env.worker.veridianReputationGate(gws, nil, veridianReputationTestEntryClass("a@agence-veridian.fr", "ovh"))
	assert.Equal(t, 2, repFactor(env, "agence-veridian.fr", "google"))
	assert.Equal(t, 1, repFactor(env, "agence-veridian.fr", "ovh"))

	// Un profil qui pose son seuil à 8 % : plus de ralentissement pour google.
	status, err = VeridianComputeReputationStatus(env.worker.ctx, env.mockMessageHistoryRepo, "ws-1", "agence-veridian.fr",
		&domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}, time.Now())
	require.NoError(t, err)
	for _, c := range status.Classes {
		if c.Class == "google" {
			assert.Equal(t, 1, c.Factor)
			assert.Equal(t, 0.08, c.Threshold)
		}
	}
}

func TestPolicyHook_LaSourceDesTablesDuPlanDitPolicyQuandLeWorkspaceSurcharge(t *testing.T) {
	enableVeridianClassDefaults(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	assert.Equal(t, "default", veridianPolicyTableSource(ws, false))
	assert.Equal(t, "default", veridianPolicyTableSource(ws, true))
	withPolicy(t, map[string]int{"ovh": 7}, nil, nil, map[string]domain.VeridianPolicyOverridden{"ovh": {DailyCap: true}})
	assert.Equal(t, "policy", veridianPolicyTableSource(ws, false))
	assert.Equal(t, "default", veridianPolicyTableSource(ws, true), "seul le plafond est surchargé")
}
