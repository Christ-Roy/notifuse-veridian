package queue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Lot 6 : capacité du jour d'un profil. Même méthode que le lot 2 : le plan
// (VeridianEffectivePlan) contre les décisions RÉELLES du worker.
//
// 1. Plafonds : on rejoue le worker (sélection + réservation atomique) classe après
//    classe jusqu'à ce qu'il refuse tout, et on exige que le nombre d'envois admis soit
//    EXACTEMENT la capacité restante du plan (somme des classes bornée par les portes
//    de profil).
// 2. Débit : le limiteur réel du worker (ProviderClassRateLimiter) rejoué seconde par
//    seconde sur la fenêtre, le fusible compris, donne la capacité de débit du plan à un
//    envoi près (le jeton de départ).
// 3. Fenêtre : les minutes d'ouverture comptées par le plan sont celles que
//    IsWithinWindow (la porte du worker) compte une à une.

// capParityRepo : comme planParityRepo, mais les compteurs avancent à chaque réservation
// réussie (le test les incrémente à l'envoi), ce que fait la base.
type capParityRepo struct {
	domain.MessageHistoryRepository
	mu    sync.Mutex
	world *planParityWorld
}

func (r *capParityRepo) ReserveDailyQuota(_ context.Context, _ string, res domain.VeridianDailyQuotaReservation) (domain.VeridianDailyQuotaReservationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	used := 0
	switch res.Key.Kind {
	case domain.VeridianDailyQuotaKindProfile:
		used = r.world.profileReserved
	case domain.VeridianDailyQuotaKindWarmup:
		used = r.world.domainSent[res.Key.SenderDomain]
	case domain.VeridianDailyQuotaKindProviderClass:
		used = r.world.domainClassSent[res.Key.SenderDomain][res.Key.ProviderClass]
	}
	return domain.VeridianDailyQuotaReservationResult{Reserved: used < res.Cap, Used: used}, nil
}
func (r *capParityRepo) ReleaseDailyQuota(context.Context, string, string, string) error { return nil }
func (r *capParityRepo) ListUnclassifiedSuccessfulMessagesSince(context.Context, string, time.Time) ([]domain.VeridianUnclassifiedSuccessfulMessage, error) {
	return nil, nil
}
func (r *capParityRepo) SetMessageProviderClassIfEmpty(context.Context, string, string, string) error {
	return nil
}

// capWorkerSends : ce que processEntry exécute avant SMTP pour un destinataire de la classe.
func capWorkerSends(env *veridianThrottleTestEnv, ws *domain.Workspace, class string) bool {
	integration := ws.GetIntegrationByID("prof")
	entry := veridianTestEntry("e-"+class, "lead@client-"+class+".example", domain.EmailQueuePayload{})
	entry.IntegrationID = "prof"
	entry.Payload.FromAddress = "hello@envoi.example"
	entry.Payload.FromName = "Veridian"
	entry.Payload.VeridianProviderClass = class
	sel := env.worker.veridianSelectSendableIntegration(ws, entry, integration)
	if sel.Candidate == nil {
		return false
	}
	entry.IntegrationID = sel.Candidate.IntegrationID
	entry.Payload.FromAddress = sel.Candidate.FromAddress
	_, _, capped := env.worker.veridianReserveDailyQuota(ws, &integration.EmailProvider, entry)
	return !capped
}

// capAdmitAll rejoue le worker en tournant sur les 11 classes jusqu'à ce qu'il refuse
// tout. Chaque envoi admis avance les mêmes compteurs que la base. Rend les envois admis
// au total et par classe.
func capAdmitAll(t *testing.T, env *veridianThrottleTestEnv, ws *domain.Workspace, world *planParityWorld) (int, map[string]int) {
	total, perClass := 0, map[string]int{}
	for pass := 0; pass < 5000; pass++ {
		progressed := false
		for _, class := range domain.VeridianAllProviderClasses() {
			if !capWorkerSends(env, ws, class) {
				continue
			}
			progressed = true
			total++
			perClass[class]++
			world.profileReserved++
			world.domainSent["envoi.example"]++
			world.senderSent["hello@envoi.example"]++
			if world.domainClassSent["envoi.example"] == nil {
				world.domainClassSent["envoi.example"] = map[string]int{}
			}
			world.domainClassSent["envoi.example"][class]++
		}
		if !progressed {
			return total, perClass
		}
	}
	t.Fatal("le worker ne s'arrête jamais : plafond manquant dans le scénario")
	return 0, nil
}

func capAllClasses(n int) map[string]int {
	out := map[string]int{}
	for _, class := range domain.VeridianAllProviderClasses() {
		out[class] = n
	}
	return out
}

func capPlan(env *veridianThrottleTestEnv, ws *domain.Workspace, world *planParityWorld, now time.Time, share map[string]int) domain.VeridianEffectivePlan {
	provider := &ws.GetIntegrationByID("prof").EmailProvider
	reputation := map[string]VeridianReputationStatus{}
	if status, err := VeridianComputeReputationStatus(context.Background(), env.mockMessageHistoryRepo, planParityWorkspaceID, "envoi.example", provider, now); err == nil {
		reputation["envoi.example"] = status
	}
	obs := world.observed("prof")
	obs.DomainClassSent = world.domainClassSent
	return VeridianEffectivePlan(VeridianPlanInput{
		Workspace: ws, IntegrationID: "prof", Now: now, Observed: obs, Reputation: reputation, StockShare: share,
	})
}

func TestVeridianCapacity_ParityWithWorkerDecisions(t *testing.T) {
	startedToday := time.Now().UTC().Add(-time.Hour)
	// nc : toutes les classes (11 historiques + 7 classes fines du 10/10, qui ont chacune
	// leur compteur et leur plafond dans capAllClasses).
	nc := len(domain.VeridianAllProviderClasses())
	cases := []struct {
		name     string
		provider domain.EmailProvider
		settings domain.WorkspaceSettings
		world    func() *planParityWorld
		// total attendu (indépendant du plan : calculé à la main), 0 si on ne le fixe pas.
		want int
		// gate attendue de la borne de profil quand elle borne.
		limiting string
	}{
		{
			name:     "plafonds de classe seuls : la somme des classes",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: capAllClasses(3)},
			want:     3 * nc, limiting: domain.VeridianCapacityLimitClassCaps,
		},
		{
			name:     "plafond plat sous la somme : le plat borne (18 x 5 = 90, plat 20)",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: capAllClasses(5), VeridianProfileDailyCap: 20},
			want:     20, limiting: domain.VeridianPlanGateProfileCap,
		},
		{
			name:     "plafond plat AU-DESSUS de la somme : la somme des classes borne (le plat n'est plus la limite)",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: capAllClasses(2), VeridianProfileDailyCap: 500},
			want:     2 * nc, limiting: domain.VeridianCapacityLimitClassCaps,
		},
		{
			name:     "sans plafond plat : la somme des classes seule",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: capAllClasses(4)},
			want:     4 * nc, limiting: domain.VeridianCapacityLimitClassCaps,
		},
		{
			name:     "chauffe sous la somme : la chauffe borne",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: capAllClasses(5), VeridianWarmupStartedAt: &startedToday, VeridianWarmupSchedule: []int{12}},
			want:     12, limiting: domain.VeridianPlanGateWarmup,
		},
		{
			name:     "plafond par expediteur sous la somme",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: capAllClasses(5), VeridianPerSenderDailyCap: 9},
			want:     9, limiting: domain.VeridianPlanGatePerSender,
		},
		{
			name:     "fusible : une plainte divise tous les plafonds de classe par 4 (8 -> 2)",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: capAllClasses(8)},
			world: func() *planParityWorld {
				w := newCapWorld()
				w.complaints = map[string]int{"envoi.example": 1}
				return w
			},
			want: 2 * nc, limiting: domain.VeridianCapacityLimitClassCaps,
		},
		{
			name:     "classe exclue : sa capacite est nulle, la somme baisse d'une classe",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: capAllClasses(4), VeridianExcludedProviderClasses: []string{"microsoft", "ionos"}},
			want:     4 * (nc - 2), limiting: domain.VeridianCapacityLimitClassCaps,
		},
		{
			name:     "envois deja faits : la capacite restante est ce qui reste",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: capAllClasses(4)},
			world: func() *planParityWorld {
				w := newCapWorld()
				w.profileReserved = 5
				w.domainSent["envoi.example"] = 5
				w.senderSent["hello@envoi.example"] = 5
				w.domainClassSent["envoi.example"] = map[string]int{"google": 4, "microsoft": 1}
				return w
			},
			want: 4 * nc, limiting: domain.VeridianCapacityLimitClassCaps,
		},
		{
			name:     "plafonds de classe du workspace herites",
			settings: domain.WorkspaceSettings{VeridianProviderClassDailyCap: capAllClasses(3)},
			want:     3 * nc, limiting: domain.VeridianCapacityLimitClassCaps,
		},
		{
			name:     "classes sans plafond + plafond plat : le plat borne seul",
			provider: domain.EmailProvider{VeridianProfileDailyCap: 17},
			want:     17, limiting: domain.VeridianPlanGateProfileCap,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newVeridianThrottleTestEnv(t)
			ws := planParityWorkspace(tc.provider, tc.settings)
			world := newCapWorld()
			if tc.world != nil {
				world = tc.world()
			}
			planParityArmRepo(env, *world)
			env.worker.messageHistoryRepo = &capParityRepo{MessageHistoryRepository: env.mockMessageHistoryRepo, world: world}

			now := time.Now().UTC()
			before := capPlan(env, ws, world, now, nil)
			require.True(t, before.Capacity.Applicable)
			sentBefore := before.Capacity.Sent
			require.NotNil(t, before.Capacity.RemainingConfigured, "capacite bornee attendue")

			admitted, _ := capAdmitAll(t, env, ws, world)

			// Parité : le worker admet EXACTEMENT ce que le plan annonçait.
			assert.Equal(t, *before.Capacity.RemainingConfigured, admitted, "le worker n'a pas admis la capacité restante annoncée par le plan")
			assert.Equal(t, tc.want, sentBefore+*before.Capacity.RemainingConfigured, "capacité du jour (calculée à la main)")
			require.NotNil(t, before.Capacity.Configured)
			assert.Equal(t, tc.want, *before.Capacity.Configured, "la capacité du jour compte les envois déjà faits")
			assert.Equal(t, tc.limiting, before.Capacity.Limiting)

			// Après avoir tout consommé : plus rien de restant, capacité = envoyés.
			after := capPlan(env, ws, world, now, nil)
			require.NotNil(t, after.Capacity.RemainingConfigured)
			assert.Equal(t, 0, *after.Capacity.RemainingConfigured)
		})
	}
}

func newCapWorld() *planParityWorld {
	return &planParityWorld{
		domainSent:      map[string]int{},
		senderSent:      map[string]int{},
		domainClassSent: map[string]map[string]int{},
	}
}

// Les classes sans plafond ni débit ne sont PAS comptées à 0 : sans borne de profil, la
// capacité est illimitée (nil), et elle ne devient pas 0 par défaut.
func TestVeridianCapacity_UnlimitedWithoutAnyBound(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := planParityWorkspace(domain.EmailProvider{}, domain.WorkspaceSettings{})
	world := newCapWorld()
	planParityArmRepo(env, *world)
	plan := capPlan(env, ws, world, time.Now().UTC(), nil)
	assert.Nil(t, plan.Capacity.Configured)
	assert.Nil(t, plan.Capacity.Useful)
	assert.Equal(t, domain.VeridianCapacityLimitNone, plan.Capacity.Limiting)
	for _, c := range plan.Classes {
		assert.Nil(t, c.CapacityRemaining, c.Class)
		assert.Equal(t, domain.VeridianCapacityLimiterNone, c.CapacityLimiter)
	}
}

// --- débit x minutes d'ouverture ---

var capWeekdayWindow = &domain.VeridianSendingWindow{Days: []int{1, 2, 3, 4, 5}, StartHour: 8, EndHour: 19, Timezone: "UTC"}

// Robert (10/10) : 0,12/min ovh, 0,053 google, 0,027 yahoo, plafonds 150/100/80/50, fenêtre
// 8h-19h du lundi au vendredi, fusible ÷2 et ÷4, ionos exclu.
func capRobertProvider() domain.EmailProvider {
	return domain.EmailProvider{
		VeridianSendingWindow: capWeekdayWindow,
		VeridianProviderClassRates: map[string]float64{
			"ovh": 0.12, "other_hoster": 0.12, "corporate_selfhost": 0.12,
			"google": 0.053, "microsoft": 0.053, "corporate": 0.053,
			"yahoo_aol": 0.027, "freemail_fr": 0.027, "ionos": 0.027, "apple_icloud": 0.027, "security_gateway": 0.027,
		},
		VeridianProviderClassDailyCap: map[string]int{
			"ovh": 150, "other_hoster": 150, "corporate_selfhost": 150, "google": 150, "microsoft": 150, "corporate": 150,
			"yahoo_aol": 80, "freemail_fr": 80, "ionos": 100, "apple_icloud": 50, "security_gateway": 50,
		},
		VeridianExcludedProviderClasses: []string{"ionos"},
	}
}

// mercredi 7 octobre 2026, 08:00 UTC : la fenêtre vient de s'ouvrir (660 minutes devant).
var capWednesdayOpening = time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)

func TestVeridianCapacity_RateTimesRemainingWindow(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := planParityWorkspace(capRobertProvider(), domain.WorkspaceSettings{})
	world := newCapWorld()
	planParityArmRepo(env, *world)

	plan := capPlan(env, ws, world, capWednesdayOpening, nil)
	want := map[string]int{
		"ovh": 79, "other_hoster": 79, "corporate_selfhost": 79, // 0,12 x 660
		"google": 34, "microsoft": 34, "corporate": 34, // 0,053 x 660 = 34,98
		"yahoo_aol": 17, "freemail_fr": 17, "apple_icloud": 17, "security_gateway": 17, // 0,027 x 660 = 17,82
		"ionos": 0, // exclue
	}
	// Les 7 classes fines (10/10) héritent du débit d'other_hoster tant qu'elles n'ont pas
	// d'entrée propre : 7 x 79 de plus (héritage : domain.VeridianRateForClass).
	for _, fine := range []string{"infomaniak", "gandi", "hostinger", "o2switch", "lws", "scaleway", "website_builder"} {
		want[fine] = 79
	}
	sum := 0
	for class, w := range want {
		c := planClass(plan, class)
		require.NotNil(t, c.CapacityRemaining, class)
		assert.Equal(t, w, *c.CapacityRemaining, class)
		sum += w
	}
	assert.Equal(t, domain.VeridianCapacityLimiterExcluded, planClass(plan, "ionos").CapacityLimiter)
	assert.Equal(t, domain.VeridianCapacityLimiterRate, planClass(plan, "ovh").CapacityLimiter)
	require.NotNil(t, plan.Capacity.Configured)
	assert.Equal(t, sum, *plan.Capacity.Configured)
	assert.Equal(t, domain.VeridianCapacityLimitClassRates, plan.Capacity.Limiting)
	assert.Equal(t, 660, plan.Capacity.WindowMinutesLeft)
	assert.Equal(t, 660, plan.Capacity.WindowMinutesFull)
	// Les plafonds de classe (150...) ne mordent jamais ici : c'est le débit qui borne.
	require.NotNil(t, plan.Capacity.FullDayConfigured)
	assert.Equal(t, sum, *plan.Capacity.FullDayConfigured)
	// Et la valeur est celle de l'ancien calcul de plafonds de classe, bien plus haute.
	require.NotNil(t, plan.DailyCapToday)
}

func TestVeridianCapacity_ShrinksAsTheWindowElapses(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := planParityWorkspace(capRobertProvider(), domain.WorkspaceSettings{})
	world := newCapWorld()
	planParityArmRepo(env, *world)

	noon := capPlan(env, ws, world, time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC), nil) // 4 h = 240 min
	assert.Equal(t, 240, noon.Capacity.WindowMinutesLeft)
	require.NotNil(t, planClass(noon, "ovh").CapacityRemaining)
	assert.Equal(t, 28, *planClass(noon, "ovh").CapacityRemaining) // 0,12 x 240 = 28,8
	// Les envois déjà faits comptent dans la capacité du jour mais pas dans le restant.
	world.domainClassSent["envoi.example"] = map[string]int{"ovh": 40}
	later := capPlan(env, ws, world, time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC), nil)
	assert.Equal(t, 28, *planClass(later, "ovh").CapacityRemaining)
	assert.Equal(t, 68, *planClass(later, "ovh").CapacityConfigured)
}

func TestVeridianCapacity_ClosedWindow(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := planParityWorkspace(capRobertProvider(), domain.WorkspaceSettings{})
	world := newCapWorld()
	planParityArmRepo(env, *world)

	for name, at := range map[string]time.Time{
		"samedi":            time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC),
		"mercredi 20h":      time.Date(2026, 10, 7, 20, 0, 0, 0, time.UTC),
		"dimanche":          time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC),
		"mercredi 19h pile": time.Date(2026, 10, 7, 19, 0, 0, 0, time.UTC),
	} {
		t.Run(name, func(t *testing.T) {
			plan := capPlan(env, ws, world, at, nil)
			assert.Equal(t, 0, plan.Capacity.WindowMinutesLeft)
			require.NotNil(t, plan.Capacity.Configured)
			assert.Equal(t, 0, *plan.Capacity.Configured, "rien de plus ne partira aujourd'hui")
			assert.Equal(t, domain.VeridianCapacityLimitWindow, plan.Capacity.Limiting)
			// Mais la capacité d'une journée d'ouverture type reste lisible.
			require.NotNil(t, plan.Capacity.FullDayConfigured)
			assert.Greater(t, *plan.Capacity.FullDayConfigured, 300)
			assert.Equal(t, domain.VeridianCapacityLimiterWindow, planClass(plan, "ovh").CapacityLimiter)
		})
	}
}

func TestVeridianCapacity_SlowdownFactorDividesRateAndCap(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := planParityWorkspace(capRobertProvider(), domain.WorkspaceSettings{})
	world := newCapWorld()
	world.complaints = map[string]int{"envoi.example": 1} // plainte : toutes les classes ÷4
	planParityArmRepo(env, *world)

	plan := capPlan(env, ws, world, capWednesdayOpening, nil)
	// ovh : débit 0,12 / 4 = 0,03 -> 19 (0,03 x 660 = 19,8) ; plafond 150 / 4 = 37.
	require.NotNil(t, planClass(plan, "ovh").CapacityRemaining)
	assert.Equal(t, 19, *planClass(plan, "ovh").CapacityRemaining)
	assert.Equal(t, 4, planClass(plan, "ovh").Factor)
	// google : 0,053 / 4 = 0,01325 -> 8 (8,745).
	assert.Equal(t, 8, *planClass(plan, "google").CapacityRemaining)
}

func TestVeridianCapacity_ClassCapBindsWhenBelowRate(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	p := capRobertProvider()
	p.VeridianProviderClassDailyCap = map[string]int{"ovh": 10, "google": 150}
	ws := planParityWorkspace(p, domain.WorkspaceSettings{})
	world := newCapWorld()
	world.domainClassSent["envoi.example"] = map[string]int{"ovh": 4}
	planParityArmRepo(env, *world)

	plan := capPlan(env, ws, world, capWednesdayOpening, nil)
	ovh := planClass(plan, "ovh")
	require.NotNil(t, ovh.CapacityRemaining)
	assert.Equal(t, 6, *ovh.CapacityRemaining) // 10 - 4, le débit (79) est plus haut
	assert.Equal(t, domain.VeridianCapacityLimiterClassCap, ovh.CapacityLimiter)
}

// Parité du débit : le limiteur réel du worker, rejoué sur la fenêtre, laisse passer la
// capacité du plan (à un envoi près : le jeton de départ du limiteur, burst 1).
func TestVeridianCapacity_RateParityWithRealLimiter(t *testing.T) {
	rates := capRobertProvider().VeridianProviderClassRates
	for _, factor := range []int{1, 2, 4} {
		for class, base := range rates {
			effective := veridianEffectiveClassRate(rates, class, factor)
			assert.InDelta(t, base/float64(factor), effective, 1e-9)

			limiter := NewProviderClassRateLimiter()
			start := capWednesdayOpening
			allowed := 0
			// Une tentative par seconde pendant 660 minutes, exactement comme les re-contrôles
			// du worker, qui n'obtiennent un jeton qu'à l'échéance du débit.
			for sec := 0; sec < 660*60; sec++ {
				at := start.Add(time.Duration(sec) * time.Second)
				if limiter.GetOrCreateLimiter("prof", class, effective).AllowN(at, 1) {
					allowed++
				}
			}
			formula := veridianRateCapacity(effective, 660)
			assert.GreaterOrEqual(t, allowed, formula, "%s ÷%d : le worker laisse passer au moins la capacité annoncée", class, factor)
			assert.LessOrEqual(t, allowed, formula+1, "%s ÷%d : au plus un envoi de plus (jeton de départ)", class, factor)
		}
	}
}

// --- fenêtre ---

func TestVeridianWindowOpenMinutes_MatchesTheGateMinuteByMinute(t *testing.T) {
	windows := []*domain.VeridianSendingWindow{
		capWeekdayWindow,
		{Days: []int{1, 2, 3, 4, 5}, StartHour: 8, EndHour: 19, Timezone: "Europe/Paris"},
		{Days: []int{0, 6}, StartHour: 9, StartMinute: 30, EndHour: 17, EndMinute: 15, Timezone: "Europe/Paris"},
		{StartHour: 0, EndHour: 24, Timezone: "America/New_York"},
		{Days: []int{3}, StartHour: 22, EndHour: 24, Timezone: "Asia/Tokyo"},
	}
	// Fin octobre : le passage à l'heure d'hiver (25/10) tombe dans l'intervalle.
	for wi, w := range windows {
		for _, from := range []time.Time{
			time.Date(2026, 10, 24, 5, 17, 0, 0, time.UTC),
			time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 10, 7, 14, 0, 0, 0, time.UTC),
		} {
			to := from.Add(52 * time.Hour)
			count := 0.0
			for m := from; m.Before(to); m = m.Add(time.Minute) {
				if w.IsWithinWindow(m, "Europe/Paris") {
					count++
				}
			}
			assert.InDelta(t, count, w.OpenMinutes(from, to, "Europe/Paris"), 0.001, "fenêtre %d à partir de %s", wi, from)
		}
	}
}

func TestVeridianWindowOpenMinutes_NoWindowIsWholeSpan(t *testing.T) {
	var w *domain.VeridianSendingWindow
	from := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	assert.InDelta(t, 90, w.OpenMinutes(from, from.Add(90*time.Minute), "UTC"), 0.0001)
	assert.Equal(t, 0.0, w.OpenMinutes(from, from, "UTC"))
	assert.InDelta(t, 24*60, w.FullDayMinutes(), 0.0001)
}

// --- stock ---

func TestVeridianCapacity_UsefulIsBoundedByStock(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := planParityWorkspace(capRobertProvider(), domain.WorkspaceSettings{})
	world := newCapWorld()
	planParityArmRepo(env, *world)

	share := map[string]int{"ovh": 30, "other_hoster": 500, "corporate_selfhost": 0}
	plan := capPlan(env, ws, world, capWednesdayOpening, share)
	require.True(t, plan.Capacity.StockKnown)

	ovh := planClass(plan, "ovh")
	require.NotNil(t, ovh.CapacityUseful)
	assert.Equal(t, 30, *ovh.CapacityUseful, "30 contacts dus pour 79 de capacité : 30 sont utiles")
	require.NotNil(t, ovh.Stock)
	assert.Equal(t, 30, *ovh.Stock)
	assert.Equal(t, 79, *ovh.CapacityConfigured)

	hoster := planClass(plan, "other_hoster")
	assert.Equal(t, 79, *hoster.CapacityUseful, "le stock dépasse la capacité : la capacité borne")
	assert.Equal(t, 0, *planClass(plan, "corporate_selfhost").CapacityUseful, "aucun contact dû dans cette classe")
	// Classes absentes de la répartition : stock 0, rien d'utile.
	assert.Equal(t, 0, *planClass(plan, "google").CapacityUseful)

	require.NotNil(t, plan.Capacity.Useful)
	assert.Equal(t, 30+79, *plan.Capacity.Useful)
	require.NotNil(t, plan.Capacity.Unusable)
	assert.Equal(t, *plan.Capacity.Configured-*plan.Capacity.Useful, *plan.Capacity.Unusable)
	assert.Equal(t, "corporate_selfhost", plan.Capacity.StockLimitingClass, "79 de capacité vide : l'écart le plus grand (79 contre 49 pour ovh)")
}

func TestVeridianCapacity_UnknownStockDoesNotReduceUseful(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := planParityWorkspace(capRobertProvider(), domain.WorkspaceSettings{})
	world := newCapWorld()
	planParityArmRepo(env, *world)
	plan := capPlan(env, ws, world, capWednesdayOpening, nil)
	assert.False(t, plan.Capacity.StockKnown)
	require.NotNil(t, plan.Capacity.Useful)
	assert.Equal(t, *plan.Capacity.Configured, *plan.Capacity.Useful)
	assert.Nil(t, planClass(plan, "ovh").Stock)
}

func TestVeridianCapacity_ProfileGatesBoundTheSum(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	p := capRobertProvider()
	startedToday := capWednesdayOpening.Add(-time.Hour)
	p.VeridianWarmupStartedAt = &startedToday
	p.VeridianWarmupSchedule = []int{300}
	p.VeridianProfileDailyCap = 300
	ws := planParityWorkspace(p, domain.WorkspaceSettings{})
	world := newCapWorld()
	planParityArmRepo(env, *world)

	plan := capPlan(env, ws, world, capWednesdayOpening, nil)
	require.NotNil(t, plan.Capacity.SumRemaining)
	assert.Equal(t, 960, *plan.Capacity.SumRemaining) // 3x79 + 3x34 + 4x17 (ionos exclu) + 7x79 classes fines héritées d'other_hoster
	require.NotNil(t, plan.Capacity.Configured)
	assert.Equal(t, 300, *plan.Capacity.Configured)
	assert.Equal(t, domain.VeridianPlanGateWarmup, plan.Capacity.Limiting, "chauffe et plat à 300 : la chauffe passe en premier")
	assert.Equal(t, 300, plan.Capacity.FlatCap)
	// Retirer le plafond plat ne change RIEN tant que la chauffe est à 300.
	p.VeridianProfileDailyCap = 0
	ws2 := planParityWorkspace(p, domain.WorkspaceSettings{})
	plan2 := capPlan(env, ws2, world, capWednesdayOpening, nil)
	require.NotNil(t, plan2.Capacity.Configured)
	assert.Equal(t, *plan.Capacity.Configured, *plan2.Capacity.Configured)
	assert.Equal(t, 0, plan2.Capacity.FlatCap)
}

// --- partage du stock du pool ---

func TestVeridianAllocateStock(t *testing.T) {
	seventy, thirty := 70, 30
	zero := 0
	t.Run("la somme des parts égale le stock", func(t *testing.T) {
		shares := VeridianAllocateStock(
			map[string]int{"ovh": 100},
			map[string]map[string]*int{"a": {"ovh": &seventy}, "b": {"ovh": &thirty}},
		)
		assert.Equal(t, 70, shares["a"]["ovh"])
		assert.Equal(t, 30, shares["b"]["ovh"])
	})
	t.Run("stock plus petit que la capacité : au prorata, au plus grand reste", func(t *testing.T) {
		shares := VeridianAllocateStock(
			map[string]int{"ovh": 10},
			map[string]map[string]*int{"a": {"ovh": &seventy}, "b": {"ovh": &thirty}},
		)
		assert.Equal(t, 7, shares["a"]["ovh"])
		assert.Equal(t, 3, shares["b"]["ovh"])
		assert.Equal(t, 10, shares["a"]["ovh"]+shares["b"]["ovh"])
	})
	t.Run("restes distribués sans perte", func(t *testing.T) {
		a, b, c := 1, 1, 1
		shares := VeridianAllocateStock(
			map[string]int{"google": 10},
			map[string]map[string]*int{"a": {"google": &a}, "b": {"google": &b}, "c": {"google": &c}},
		)
		assert.Equal(t, 10, shares["a"]["google"]+shares["b"]["google"]+shares["c"]["google"])
	})
	t.Run("un profil qui n'absorbe rien (classe exclue) n'a aucune part", func(t *testing.T) {
		shares := VeridianAllocateStock(
			map[string]int{"ovh": 50},
			map[string]map[string]*int{"a": {"ovh": &seventy}, "b": {"ovh": &zero}},
		)
		assert.Equal(t, 50, shares["a"]["ovh"])
		assert.Equal(t, 0, shares["b"]["ovh"])
	})
	t.Run("capacité sans limite : pesée comme le stock", func(t *testing.T) {
		shares := VeridianAllocateStock(
			map[string]int{"ovh": 40},
			map[string]map[string]*int{"a": {"ovh": nil}, "b": {"ovh": nil}},
		)
		assert.Equal(t, 40, shares["a"]["ovh"]+shares["b"]["ovh"])
	})
	t.Run("aucun stock : zéro partout", func(t *testing.T) {
		shares := VeridianAllocateStock(map[string]int{}, map[string]map[string]*int{"a": {"ovh": &seventy}})
		assert.Equal(t, 0, shares["a"]["ovh"])
	})
}
