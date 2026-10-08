package queue

import (
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// Fusible de réputation PROPORTIONNÉ (07/10/2026) : ces tests provoquent chaque
// palier pour prouver que le gate RALENTIT (÷2, ÷4) au lieu d'arrêter, qu'une
// plainte seule n'arrête rien, et que seul un refus en bloc (>50 % de 5.7.x sur
// les 20 derniers envois) arrête un couple (le failover prend alors le relais).

const repTestDomain = "messagerie-nord-776.fr"

func veridianReputationTestEntry(fromAddress string) *domain.EmailQueueEntry {
	return veridianReputationTestEntryClass(fromAddress, "")
}

// veridianReputationTestEntryClass fixe la classe du destinataire dans le payload
// (sinon le classifier MX de test rend corporate_selfhost).
func veridianReputationTestEntryClass(fromAddress, class string) *domain.EmailQueueEntry {
	return veridianTestEntry("rep-e1", "victim@example.com", domain.EmailQueuePayload{
		FromAddress:           fromAddress,
		VeridianProviderClass: class,
	})
}

// veridianExpectRepCounts arme les lectures du fusible pour un domaine émetteur :
// plaintes, envois du domaine, ventilation par classe. Les lectures sont AnyTimes
// (le nombre d'appels dépend de l'ordre d'essai des candidats).
func veridianExpectRepCounts(env *veridianThrottleTestEnv, domainName string, complaints, sent int, byClass map[string]domain.VeridianReputationCounts) {
	env.mockMessageHistoryRepo.EXPECT().
		CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", domainName, gomock.Any()).Return(complaints, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", domainName, gomock.Any()).Return(sent, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().
		CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws-1", domainName, gomock.Any()).Return(0, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().
		ReputationCountsByClassSinceForSenderDomain(gomock.Any(), "ws-1", domainName, gomock.Any()).Return(byClass, nil).AnyTimes()
}

// veridianExpectRecent arme la lecture des N derniers envois d'un couple.
func veridianExpectRecent(env *veridianThrottleTestEnv, domainName, class string, sent, policy int) {
	env.mockMessageHistoryRepo.EXPECT().
		RecentClassOutcomesForSenderDomain(gomock.Any(), "ws-1", domainName, class, veridianBlockLastN, gomock.Any()).
		Return(sent, policy, nil).AnyTimes()
}

// repFactor lit le facteur posé par le gate pour ce couple. Depuis le lot 4 le
// facteur est propre au profil candidat (clé ws|profil|domaine|classe) : on prend le
// plus haut facteur vivant de toutes les clés qui portent ce domaine et cette classe.
func repFactor(env *veridianThrottleTestEnv, domainName, class string) int {
	best := env.worker.reputationFactors.get("ws-1", domainName, class)
	env.worker.reputationFactors.m.Range(func(k, _ interface{}) bool {
		key := k.(string)
		if strings.HasPrefix(key, "ws-1|") && strings.HasSuffix(key, "|"+domainName+"|"+class) {
			if f := env.worker.reputationFactors.load(key); f > best {
				best = f
			}
		}
		return true
	})
	return best
}

func TestVeridianReputationGate_NoSenderDomainIsNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	// Aucune attribution d'infra possible : aucun COUNT appelé (gomock refuserait).
	delay, stopped := env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntry(""))
	assert.False(t, stopped)
	assert.Zero(t, delay)
}

func TestVeridianReputationGate_HealthyCoupleRunsAtFullRate(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	veridianExpectRepCounts(env, repTestDomain, 0, 100, map[string]domain.VeridianReputationCounts{"ovh": {Sent: 100, HardBounces: 2}}) // 2% < 3%

	_, stopped := env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntryClass("r@"+repTestDomain, "ovh"))
	assert.False(t, stopped)
	assert.Equal(t, 1, repFactor(env, repTestDomain, "ovh"))
}

// Paliers de ralentissement d'un couple : <seuil = normal, [seuil ; 2 x seuil[ = ÷2,
// >= 2 x seuil = ÷4. Jamais d'arrêt, quel que soit le taux de rejets durs.
func TestVeridianReputationGate_ProgressiveSlowdown_Halves_ThenQuarters_NeverStops(t *testing.T) {
	ws := &domain.Workspace{ID: "ws-1"}
	p8 := &domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}

	cases := []struct {
		name   string
		sent   int
		hard   int
		policy int
		prov   *domain.EmailProvider
		factor int
	}{
		{"5% sous 8% : normal", 20, 1, 0, p8, 1},
		{"10% (1 a 2 x 8%) : ÷2", 20, 2, 0, p8, 2},
		{"15% juste sous 2 x 8% : ÷2", 20, 3, 0, p8, 2},
		{"20% (>= 2 x 8%) : ÷4", 20, 4, 0, p8, 4},
		{"100% de rejets durs : ÷4, PAS d'arret", 40, 40, 0, p8, 4},
		{"defaut 3% : 5% = ÷2", 20, 1, 0, &domain.EmailProvider{}, 2},
		{"defaut 3% : 10% = ÷4", 20, 2, 0, &domain.EmailProvider{}, 4},
		{"refus de politique 10% a 8% : ÷2", 20, 0, 2, p8, 2},
		{"refus de politique 20% a 8% : ÷4", 20, 0, 4, p8, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newVeridianThrottleTestEnv(t)
			veridianExpectRepCounts(env, repTestDomain, 0, tc.sent, map[string]domain.VeridianReputationCounts{
				"ionos": {Sent: tc.sent, HardBounces: tc.hard, PolicyRefusals: tc.policy},
			})
			delay, stopped := env.worker.veridianReputationGate(ws, tc.prov, veridianReputationTestEntryClass("r@"+repTestDomain, "ionos"))
			assert.False(t, stopped, "un taux de rejets ralentit, il n'arrete jamais")
			assert.Zero(t, delay)
			assert.Equal(t, tc.factor, repFactor(env, repTestDomain, "ionos"))
		})
	}
}

// Minimum de volume avant toute reaction : 1 rejet sur 3, 19 rejets sur 19.
func TestVeridianReputationGate_MinimumSendsBeforeAnyReaction(t *testing.T) {
	ws := &domain.Workspace{ID: "ws-1"}
	for _, c := range []domain.VeridianReputationCounts{
		{Sent: 3, HardBounces: 1},
		{Sent: 19, HardBounces: 19},
		{Sent: 19, PolicyRefusals: 19},
	} {
		env := newVeridianThrottleTestEnv(t)
		veridianExpectRepCounts(env, repTestDomain, 0, c.Sent, map[string]domain.VeridianReputationCounts{"ionos": c})
		_, stopped := env.worker.veridianReputationGate(ws, &domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}, veridianReputationTestEntryClass("r@"+repTestDomain, "ionos"))
		assert.False(t, stopped)
		assert.Equal(t, 1, repFactor(env, repTestDomain, "ionos"), "sous 20 envois : aucune reaction (%+v)", c)
	}
}

// Une plainte seule n'arrete RIEN : tout le domaine ralentit ÷4 (toutes classes,
// même sans envois), une alerte est levée (log + état exposé).
func TestVeridianReputationGate_ComplaintAloneNeverStops_SlowsWholeDomainBy4(t *testing.T) {
	ws := &domain.Workspace{ID: "ws-1"}
	for _, class := range []string{"ovh", "ionos", "microsoft"} {
		env := newVeridianThrottleTestEnv(t)
		veridianExpectRepCounts(env, repTestDomain, 1, 50, map[string]domain.VeridianReputationCounts{"ovh": {Sent: 50}})
		delay, stopped := env.worker.veridianReputationGate(ws, &domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.15}, veridianReputationTestEntryClass("r@"+repTestDomain, class))
		assert.False(t, stopped, "une plainte n'arrete pas (%s)", class)
		assert.Zero(t, delay)
		assert.Equal(t, 4, repFactor(env, repTestDomain, class), "plainte : ÷4 sur la classe %s", class)
	}

	// Plainte sur un domaine sans aucun envoi : ralenti aussi, jamais arrete.
	env := newVeridianThrottleTestEnv(t)
	veridianExpectRepCounts(env, repTestDomain, 1, 0, nil)
	_, stopped := env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntryClass("r@"+repTestDomain, "ovh"))
	assert.False(t, stopped)
	assert.Equal(t, 4, repFactor(env, repTestDomain, "ovh"))
}

// Refus en bloc : seul cas d'arret. >50 % de 5.7.x sur les 20 derniers envois.
func TestVeridianReputationGate_BulkRefusalStopsOnlyThatCouple(t *testing.T) {
	ws := &domain.Workspace{ID: "ws-1"}
	byClass := map[string]domain.VeridianReputationCounts{
		"ionos": {Sent: 40, PolicyRefusals: 16},
		"ovh":   {Sent: 60, PolicyRefusals: 1},
	}

	env := newVeridianThrottleTestEnv(t)
	veridianExpectRepCounts(env, repTestDomain, 0, 100, byClass)
	veridianExpectRecent(env, repTestDomain, "ionos", 20, 12) // 60 % > 50 %
	delay, stopped := env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntryClass("r@"+repTestDomain, "ionos"))
	assert.True(t, stopped, "ionos refuse en bloc (12/20 en 5.7.x) : couple arrete")
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)

	_, stopped = env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntryClass("r@"+repTestDomain, "ovh"))
	assert.False(t, stopped, "ovh du MEME profil continue (ralentie ou non, jamais arretee)")

	// Exactement 50 % : pas "plus de 50 %" -> pas d'arret, mais ralenti.
	env = newVeridianThrottleTestEnv(t)
	veridianExpectRepCounts(env, repTestDomain, 0, 100, byClass)
	veridianExpectRecent(env, repTestDomain, "ionos", 20, 10)
	_, stopped = env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntryClass("r@"+repTestDomain, "ionos"))
	assert.False(t, stopped)
	assert.Equal(t, 4, repFactor(env, repTestDomain, "ionos"))

	// Moins de 20 envois recents : pas assez de preuve pour arreter.
	env = newVeridianThrottleTestEnv(t)
	veridianExpectRepCounts(env, repTestDomain, 0, 100, byClass)
	veridianExpectRecent(env, repTestDomain, "ionos", 12, 12)
	_, stopped = env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntryClass("r@"+repTestDomain, "ionos"))
	assert.False(t, stopped)

	// Trop peu de 5.7.x sur 7 jours pour atteindre 50 % de 20 : la lecture des 20
	// derniers n'est meme pas demandee (gomock refuserait l'appel).
	env = newVeridianThrottleTestEnv(t)
	veridianExpectRepCounts(env, repTestDomain, 0, 100, map[string]domain.VeridianReputationCounts{"ionos": {Sent: 100, PolicyRefusals: 10}})
	_, stopped = env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntryClass("r@"+repTestDomain, "ionos"))
	assert.False(t, stopped)
}

// Une plainte ET un refus en bloc : le refus en bloc arrete ce couple seulement.
func TestVeridianReputationGate_ComplaintPlusBulkRefusal_OnlyBulkStops(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	veridianExpectRepCounts(env, repTestDomain, 1, 100, map[string]domain.VeridianReputationCounts{
		"ionos": {Sent: 40, PolicyRefusals: 30}, "ovh": {Sent: 60},
	})
	veridianExpectRecent(env, repTestDomain, "ionos", 20, 18)
	_, stopped := env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntryClass("r@"+repTestDomain, "ionos"))
	assert.True(t, stopped)
	_, stopped = env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntryClass("r@"+repTestDomain, "ovh"))
	assert.False(t, stopped)
	assert.Equal(t, 4, repFactor(env, repTestDomain, "ovh"))
}

// Une erreur de lecture n'arrete rien (ce fusible ne stoppe plus que sur preuve).
func TestVeridianReputationGate_ReadErrorDoesNotStop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	env.mockMessageHistoryRepo.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", repTestDomain, gomock.Any()).Return(0, assert.AnError)
	env.mockMessageHistoryRepo.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), "ws-1", repTestDomain, gomock.Any()).Return(0, assert.AnError)
	_, stopped := env.worker.veridianReputationGate(ws, nil, veridianReputationTestEntry("r@"+repTestDomain))
	assert.False(t, stopped)
}

// === Failover : seul un couple ARRETE bascule ; un couple RALENTI reste sur son profil ===

func TestVeridianSelectSendable_StoppedCoupleFailsOverToOtherProfile(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	ws := veridianTestPoolWorkspace([]string{"nord", "relai"}, []domain.Integration{nord, relai}, nil)

	veridianExpectRepCounts(env, "nord-propre.example", 0, 100, map[string]domain.VeridianReputationCounts{
		"ionos": {Sent: 40, PolicyRefusals: 30}, "ovh": {Sent: 60, HardBounces: 1},
	})
	veridianExpectRecent(env, "nord-propre.example", "ionos", 20, 15)
	veridianExpectRepCounts(env, "relai-agence.example", 0, 100, map[string]domain.VeridianReputationCounts{
		"ionos": {Sent: 25}, "ovh": {Sent: 60, HardBounces: 1},
	})

	ionos := veridianTestEntry("e-ionos", "lead@ionos.example", domain.EmailQueuePayload{FromAddress: "hello@nord-propre.example", VeridianProviderClass: "ionos"})
	ionos.IntegrationID = "nord"
	res := env.worker.veridianSelectSendableIntegration(ws, ionos, &nord)
	require.NotNil(t, res.Candidate, "ionos refuse en bloc chez nord : bascule sur relai")
	assert.Equal(t, "relai", res.Candidate.IntegrationID)
	assert.Equal(t, "nord", ionos.IntegrationID, "la selection ne committe pas : restaure par le defer")

	ovh := veridianTestEntry("e-ovh", "lead@ovh.example", domain.EmailQueuePayload{FromAddress: "hello@nord-propre.example", VeridianProviderClass: "ovh"})
	ovh.IntegrationID = "nord"
	res = env.worker.veridianSelectSendableIntegration(ws, ovh, &nord)
	require.NotNil(t, res.Candidate)
	assert.Equal(t, "nord", res.Candidate.IntegrationID, "ovh n'est pas arretee chez nord : reste sur son profil")
}

func TestVeridianSelectSendable_SlowedCoupleStaysOnItsProfile(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	ws := veridianTestPoolWorkspace([]string{"nord", "relai"}, []domain.Integration{nord, relai}, nil)

	// 30 % de rejets durs chez nord pour ionos : ralenti ÷4, jamais arrete.
	veridianExpectRepCounts(env, "nord-propre.example", 0, 100, map[string]domain.VeridianReputationCounts{"ionos": {Sent: 40, HardBounces: 12}})
	veridianExpectRepCounts(env, "relai-agence.example", 0, 100, map[string]domain.VeridianReputationCounts{"ionos": {Sent: 25}})

	ionos := veridianTestEntry("e-ionos", "lead@ionos.example", domain.EmailQueuePayload{FromAddress: "hello@nord-propre.example", VeridianProviderClass: "ionos"})
	ionos.IntegrationID = "nord"
	res := env.worker.veridianSelectSendableIntegration(ws, ionos, &nord)
	require.NotNil(t, res.Candidate)
	assert.Equal(t, "nord", res.Candidate.IntegrationID, "un couple ralenti n'est pas ecarte")
	assert.Equal(t, 4, repFactor(env, "nord-propre.example", "ionos"))
}

func TestVeridianSelectSendable_AllCouplesStopped_EntryWaits_OtherClassGoes(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	ws := veridianTestPoolWorkspace([]string{"nord", "relai"}, []domain.Integration{nord, relai}, nil)

	stopped := map[string]domain.VeridianReputationCounts{"ionos": {Sent: 40, PolicyRefusals: 30}, "ovh": {Sent: 60}}
	veridianExpectRepCounts(env, "nord-propre.example", 0, 100, stopped)
	veridianExpectRepCounts(env, "relai-agence.example", 0, 100, stopped)
	veridianExpectRecent(env, "nord-propre.example", "ionos", 20, 15)
	veridianExpectRecent(env, "relai-agence.example", "ionos", 20, 15)

	ionos := veridianTestEntry("e-ionos", "lead@ionos.example", domain.EmailQueuePayload{FromAddress: "hello@nord-propre.example", VeridianProviderClass: "ionos"})
	ionos.IntegrationID = "nord"
	res := env.worker.veridianSelectSendableIntegration(ws, ionos, &nord)
	assert.Nil(t, res.Candidate, "ionos arretee chez tous les profils : l'entree attend")
	assert.False(t, res.Permanent, "attente, pas un rejet definitif")
	assert.Equal(t, veridianDailyCapRecheckInterval, res.RetryDelay)

	ovh := veridianTestEntry("e-ovh", "lead@ovh.example", domain.EmailQueuePayload{FromAddress: "hello@nord-propre.example", VeridianProviderClass: "ovh"})
	ovh.IntegrationID = "nord"
	res = env.worker.veridianSelectSendableIntegration(ws, ovh, &nord)
	require.NotNil(t, res.Candidate, "ovh part pendant que ionos attend")
}

// === Application du facteur par les gates de débit et de plafond ===

func TestVeridianSlowCap(t *testing.T) {
	assert.Equal(t, 300, veridianSlowCap(300, 1))
	assert.Equal(t, 150, veridianSlowCap(300, 2))
	assert.Equal(t, 75, veridianSlowCap(300, 4))
	assert.Equal(t, 1, veridianSlowCap(3, 4), "plancher a 1")
	assert.Equal(t, 0, veridianSlowCap(0, 4), "pas de plafond configure : inchange")
}

func TestVeridianReputationFactorCache_ExpiresAndDefaultsToOne(t *testing.T) {
	var f veridianReputationFactors
	assert.Equal(t, 1, f.get("ws", "d.fr", "ovh"))
	f.set("ws", "d.fr", "ovh", 4)
	assert.Equal(t, 4, f.get("ws", "d.fr", "ovh"))
	assert.Equal(t, 1, f.get("ws", "d.fr", "ionos"), "autre classe : non ralentie")
	f.m.Store(veridianReputationFactorKey("ws", "d.fr", "ovh"), veridianReputationFactorEntry{factor: 4, at: time.Now().Add(-2 * veridianReputationFactorTTL)})
	assert.Equal(t, 1, f.get("ws", "d.fr", "ovh"), "facteur perime : retour au debit normal")
}

// repLimiterRate lit le débit (jetons/seconde) du limiter {intégration, classe}
// SANS le modifier (GetOrCreateLimiter, lui, réécrit le débit).
func repLimiterRate(t *testing.T, env *veridianThrottleTestEnv, integrationID, class string) float64 {
	t.Helper()
	v, ok := env.worker.providerClassLimiter.limiters.Load(providerClassKey(integrationID, class))
	require.True(t, ok, "limiter {integration, classe} absent")
	return float64(v.(*rate.Limiter).Limit())
}

func TestVeridianComputeReputationStatus_PerCoupleFactorAndReason(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	veridianExpectRepCounts(env, "agence-veridian.fr", 0, 100, map[string]domain.VeridianReputationCounts{
		"ionos": {Sent: 40, PolicyRefusals: 30},
		"ovh":   {Sent: 60, HardBounces: 3},  // 5 % : ÷2 au defaut 3 %
		"other": {Sent: 10, HardBounces: 10}, // sous le minimum
		"":      {Sent: 5, HardBounces: 5},   // sans classe : jamais un couple
		"micro": {Sent: 30, HardBounces: 3},  // 10 % : ÷4 au defaut 3 %
	})
	veridianExpectRecent(env, "agence-veridian.fr", "ionos", 20, 14)

	status, err := VeridianComputeReputationStatus(env.worker.ctx, env.mockMessageHistoryRepo, "ws-1", "agence-veridian.fr", nil, time.Now())
	require.NoError(t, err)
	assert.False(t, status.Alert)
	assert.Equal(t, 1, status.DomainFactor)
	assert.Equal(t, veridianReputationMinSent, status.MinSent)
	assert.Equal(t, []string{"ionos"}, status.StoppedClasses)
	assert.Equal(t, []string{"micro", "ovh"}, status.SlowedClasses)
	byName := map[string]domain.VeridianReputationClassStatus{}
	for _, c := range status.Classes {
		byName[c.Class] = c
	}
	assert.True(t, byName["ionos"].Stopped)
	assert.Equal(t, "bulk_policy_refusal", byName["ionos"].Reason)
	assert.Equal(t, 20, byName["ionos"].RecentSent)
	assert.Equal(t, 2, byName["ovh"].Factor)
	assert.Equal(t, "hard_bounce_rate", byName["ovh"].Reason)
	assert.Equal(t, 4, byName["micro"].Factor)
	assert.Equal(t, 1, byName["other"].Factor)
	assert.Equal(t, 1, byName["unclassified"].Factor)
	assert.Equal(t, 7, status.WindowDays)

	// Coherence avec le gate sur les memes donnees.
	gws := &domain.Workspace{ID: "ws-1"}
	_, stopped := env.worker.veridianReputationGate(gws, nil, veridianReputationTestEntryClass("a@agence-veridian.fr", "ionos"))
	assert.True(t, stopped)
	_, stopped = env.worker.veridianReputationGate(gws, nil, veridianReputationTestEntryClass("a@agence-veridian.fr", "ovh"))
	assert.False(t, stopped)
	assert.Equal(t, 2, repFactor(env, "agence-veridian.fr", "ovh"))
}

func TestVeridianComputeReputationStatus_ComplaintIsAlertNotStop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	veridianExpectRepCounts(env, "agence-veridian.fr", 2, 100, map[string]domain.VeridianReputationCounts{"ovh": {Sent: 100}})
	st, err := VeridianComputeReputationStatus(env.worker.ctx, env.mockMessageHistoryRepo, "ws-1", "agence-veridian.fr", &domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}, time.Now())
	require.NoError(t, err)
	assert.True(t, st.Alert)
	assert.Equal(t, 4, st.DomainFactor)
	assert.Empty(t, st.StoppedClasses, "une plainte n'arrete aucune classe")
	assert.Equal(t, []string{"ovh"}, st.SlowedClasses)
	assert.Equal(t, 4, st.Classes[0].Factor)
	assert.Equal(t, "complaint", st.Classes[0].Reason)
	assert.True(t, st.ThresholdCustom)
	assert.InDelta(t, 0.08, st.Threshold, 1e-9)
}
