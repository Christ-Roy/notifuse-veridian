package queue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Lot 2 « vérité d'un profil » : EffectivePlan contre les décisions RÉELLES
// du worker. Chaque scénario pose une configuration et des compteurs du jour ;
// le test appelle (1) veridianSelectSendableIntegration + veridianReserveDailyQuota,
// c'est-à-dire exactement ce que processEntry exécute avant d'ouvrir SMTP, et
// (2) VeridianEffectivePlan sur les MÊMES compteurs. Les deux doivent répondre
// pareil pour chaque classe de destinataire. Si quelqu'un modifie une porte du
// worker sans modifier le plan (ou l'inverse), ce test rougit.

const planParityWorkspaceID = "ws-1"

// planParityWorld : les compteurs du jour que voient à la fois le worker (via le
// mock du repository) et le plan (via VeridianPlanObserved).
type planParityWorld struct {
	profileReserved int
	domainSent      map[string]int
	senderSent      map[string]int
	domainClassSent map[string]map[string]int
	complaints      map[string]int
}

func (w planParityWorld) observed(profileID string) domain.VeridianPlanObserved {
	return domain.VeridianPlanObserved{
		ProfileReserved: w.profileReserved,
		ProfileAccepted: w.profileReserved,
		DomainSent:      w.domainSent,
		SenderSent:      w.senderSent,
		DomainClassSent: w.domainClassSent,
	}
}

// planParityRepo : repository de test. Les lectures viennent du mock gomock
// configuré sur le monde ; la réservation atomique applique « used < cap »
// sur les mêmes compteurs, comme le fait la base.
type planParityRepo struct {
	domain.MessageHistoryRepository
	mu    sync.Mutex
	world planParityWorld
}

func (r *planParityRepo) ReserveDailyQuota(_ context.Context, _ string, res domain.VeridianDailyQuotaReservation) (domain.VeridianDailyQuotaReservationResult, error) {
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
func (r *planParityRepo) ReleaseDailyQuota(context.Context, string, string, string) error { return nil }
func (r *planParityRepo) ListUnclassifiedSuccessfulMessagesSince(context.Context, string, time.Time) ([]domain.VeridianUnclassifiedSuccessfulMessage, error) {
	return nil, nil
}
func (r *planParityRepo) SetMessageProviderClassIfEmpty(context.Context, string, string, string) error {
	return nil
}

func planParityArmRepo(env *veridianThrottleTestEnv, world planParityWorld) {
	m := env.mockMessageHistoryRepo
	m.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), planParityWorkspaceID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, d string, _ time.Time) (int, error) { return world.complaints[d], nil }).AnyTimes()
	m.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), planParityWorkspaceID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, d string, _ time.Time) (int, error) { return world.domainSent[d], nil }).AnyTimes()
	m.EXPECT().CountHardBouncedSinceForSenderDomain(gomock.Any(), planParityWorkspaceID, gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	m.EXPECT().ReputationCountsByClassSinceForSenderDomain(gomock.Any(), planParityWorkspaceID, gomock.Any(), gomock.Any()).
		Return(map[string]domain.VeridianReputationCounts{}, nil).AnyTimes()
	m.EXPECT().RecentClassOutcomesForSenderDomain(gomock.Any(), planParityWorkspaceID, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(0, 0, nil).AnyTimes()
	m.EXPECT().CountSentSinceForClassAndSenderDomain(gomock.Any(), planParityWorkspaceID, gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, class, d string, _ time.Time) (int, error) {
			return world.domainClassSent[d][class], nil
		}).AnyTimes()
	m.EXPECT().CountSentSinceForSender(gomock.Any(), planParityWorkspaceID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, email string, _ time.Time) (int, error) {
			return world.senderSent[email], nil
		}).AnyTimes()
	m.EXPECT().CountSentSinceForContact(gomock.Any(), planParityWorkspaceID, gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	env.worker.messageHistoryRepo = &planParityRepo{MessageHistoryRepository: m, world: world}
}

// planParityRecipients : un destinataire par classe, classé sans réseau.
var planParityRecipients = map[string]string{
	domain.ProviderClassGoogle:            "lead@gmail.com",
	domain.ProviderClassMicrosoft:         "lead@outlook.com",
	domain.ProviderClassYahooAol:          "lead@yahoo.com",
	domain.ProviderClassCorporateSelfhost: "lead@client-inconnu.example",
}

func planParityWorkspace(provider domain.EmailProvider, settings domain.WorkspaceSettings) *domain.Workspace {
	verified := time.Now().Add(-48 * time.Hour)
	provider.Kind = domain.EmailProviderKindSMTP
	if provider.SMTP == nil {
		provider.SMTP = &domain.SMTPSettings{Host: "smtp.relai.example", Port: 587}
	}
	provider.RateLimitPerMinute = 6000
	provider.VeridianTransportVerifiedAt = &verified
	provider.Senders = []domain.EmailSender{{ID: "s1", Email: "hello@envoi.example", Name: "Veridian", IsDefault: true}}
	settings.VeridianMarketingEmailProviderIDs = []string{"prof"}
	return &domain.Workspace{
		ID:           planParityWorkspaceID,
		Settings:     settings,
		Integrations: []domain.Integration{{ID: "prof", Name: "profil", Type: domain.IntegrationTypeEmail, EmailProvider: provider}},
	}
}

// workerWouldSend rejoue ce que processEntry exécute avant SMTP : sélection du
// candidat (toutes les portes à compteur) puis réservation atomique.
func workerWouldSend(env *veridianThrottleTestEnv, ws *domain.Workspace, recipient string) bool {
	integration := ws.GetIntegrationByID("prof")
	entry := veridianTestEntry("e-"+recipient, recipient, domain.EmailQueuePayload{})
	entry.IntegrationID = "prof"
	entry.Payload.FromAddress = "hello@envoi.example"
	entry.Payload.FromName = "Veridian"
	entry.Payload.VeridianProviderClass = env.worker.veridianClassifyRecipient(entry)
	sel := env.worker.veridianSelectSendableIntegration(ws, entry, integration)
	if sel.Candidate == nil {
		return false
	}
	entry.IntegrationID = sel.Candidate.IntegrationID
	entry.Payload.FromAddress = sel.Candidate.FromAddress
	_, _, capped := env.worker.veridianReserveDailyQuota(ws, &integration.EmailProvider, entry)
	return !capped
}

func planParityPlan(env *veridianThrottleTestEnv, ws *domain.Workspace, world planParityWorld) domain.VeridianEffectivePlan {
	now := time.Now().UTC()
	provider := &ws.GetIntegrationByID("prof").EmailProvider
	reputation := map[string]VeridianReputationStatus{}
	for _, d := range []string{"envoi.example"} {
		status, err := VeridianComputeReputationStatus(context.Background(), env.mockMessageHistoryRepo, planParityWorkspaceID, d, provider, now)
		if err == nil {
			reputation[d] = status
		}
	}
	return VeridianEffectivePlan(VeridianPlanInput{
		Workspace: ws, IntegrationID: "prof", Now: now,
		Observed: world.observed("prof"), Reputation: reputation,
	})
}

func planClass(plan domain.VeridianEffectivePlan, class string) domain.VeridianPlanClass {
	for _, c := range plan.Classes {
		if c.Class == class {
			return c
		}
	}
	return domain.VeridianPlanClass{}
}

func intp(v int) *int { return &v }

func TestVeridianEffectivePlan_ParityWithWorkerDecisions(t *testing.T) {
	today := int(time.Now().UTC().Weekday())
	closedDay := (today + 3) % 7
	startedToday := time.Now().UTC().Add(-time.Hour)

	type expect struct {
		sendable     map[string]bool // classes dont le worker doit dire non
		dailyCap     *int
		limiting     string
		remaining    *int
		blockedClass map[string]string
	}
	cases := []struct {
		name     string
		provider domain.EmailProvider
		settings domain.WorkspaceSettings
		world    planParityWorld
		want     expect
	}{
		{
			name:  "sans aucun plafond tout part",
			world: planParityWorld{},
			want:  expect{limiting: domain.VeridianPlanGateNone},
		},
		{
			name:     "chauffe 10, 9 envoyes : reste 1",
			provider: domain.EmailProvider{VeridianWarmupStartedAt: &startedToday, VeridianWarmupSchedule: []int{10}},
			world:    planParityWorld{domainSent: map[string]int{"envoi.example": 9}},
			want:     expect{dailyCap: intp(10), limiting: domain.VeridianPlanGateWarmup, remaining: intp(1)},
		},
		{
			name:     "chauffe 10, 10 envoyes : plus rien, nulle part",
			provider: domain.EmailProvider{VeridianWarmupStartedAt: &startedToday, VeridianWarmupSchedule: []int{10}},
			world:    planParityWorld{domainSent: map[string]int{"envoi.example": 10}},
			want: expect{dailyCap: intp(10), limiting: domain.VeridianPlanGateWarmup, remaining: intp(0),
				sendable: map[string]bool{"google": false, "microsoft": false, "yahoo_aol": false, "corporate_selfhost": false}},
		},
		{
			name:     "plafond profil 15 atteint (reservation atomique)",
			provider: domain.EmailProvider{VeridianProfileDailyCap: 15},
			world:    planParityWorld{profileReserved: 15},
			want: expect{dailyCap: intp(15), limiting: domain.VeridianPlanGateProfileCap, remaining: intp(0),
				sendable: map[string]bool{"google": false, "microsoft": false, "yahoo_aol": false, "corporate_selfhost": false}},
		},
		{
			name:     "plafond profil 15, 14 reserves : reste 1",
			provider: domain.EmailProvider{VeridianProfileDailyCap: 15},
			world:    planParityWorld{profileReserved: 14},
			want:     expect{dailyCap: intp(15), limiting: domain.VeridianPlanGateProfileCap, remaining: intp(1)},
		},
		{
			name:     "gmail sans plafond declare : le defaut de 30 s'applique",
			provider: domain.EmailProvider{SMTP: &domain.SMTPSettings{Host: "smtp.gmail.com", Port: 587}},
			world:    planParityWorld{profileReserved: 30},
			want: expect{dailyCap: intp(30), limiting: domain.VeridianPlanGateProfileCap, remaining: intp(0),
				sendable: map[string]bool{"google": false, "microsoft": false, "yahoo_aol": false, "corporate_selfhost": false}},
		},
		{
			name:     "plafond par expediteur 4 atteint",
			provider: domain.EmailProvider{VeridianPerSenderDailyCap: 4},
			world:    planParityWorld{senderSent: map[string]int{"hello@envoi.example": 4}},
			want: expect{dailyCap: intp(4), limiting: domain.VeridianPlanGatePerSender, remaining: intp(0),
				sendable: map[string]bool{"google": false, "microsoft": false, "yahoo_aol": false, "corporate_selfhost": false}},
		},
		{
			name:     "plafond par expediteur 4, 3 envoyes : reste 1",
			provider: domain.EmailProvider{VeridianPerSenderDailyCap: 4},
			world:    planParityWorld{senderSent: map[string]int{"hello@envoi.example": 3}},
			want:     expect{dailyCap: intp(4), limiting: domain.VeridianPlanGatePerSender, remaining: intp(1)},
		},
		{
			name:     "plafonds par classe du profil : google plein, les autres libres",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: map[string]int{"google": 5, "microsoft": 3}},
			world:    planParityWorld{domainClassSent: map[string]map[string]int{"envoi.example": {"google": 5, "microsoft": 1}}},
			want: expect{sendable: map[string]bool{"google": false}, limiting: domain.VeridianPlanGateNone,
				blockedClass: map[string]string{"google": domain.VeridianPlanBlockClassCap}},
		},
		{
			name:     "table de classe du profil REMPLACE celle du workspace (microsoft du workspace ignore)",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: map[string]int{"google": 5}},
			settings: domain.WorkspaceSettings{VeridianProviderClassDailyCap: map[string]int{"microsoft": 1}},
			world:    planParityWorld{domainClassSent: map[string]map[string]int{"envoi.example": {"microsoft": 9}}},
			want:     expect{limiting: domain.VeridianPlanGateNone},
		},
		{
			name:     "plafonds de classe du workspace hérités par le profil",
			settings: domain.WorkspaceSettings{VeridianProviderClassDailyCap: map[string]int{"microsoft": 2}},
			world:    planParityWorld{domainClassSent: map[string]map[string]int{"envoi.example": {"microsoft": 2}}},
			want: expect{sendable: map[string]bool{"microsoft": false}, limiting: domain.VeridianPlanGateNone,
				blockedClass: map[string]string{"microsoft": domain.VeridianPlanBlockClassCap}},
		},
		{
			name:     "plainte : plafond de classe 10 divise par 4 = 3, atteint a 3",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: map[string]int{"google": 10}},
			world: planParityWorld{
				complaints:      map[string]int{"envoi.example": 1},
				domainClassSent: map[string]map[string]int{"envoi.example": {"google": 3}},
			},
			want: expect{sendable: map[string]bool{"google": false}, limiting: domain.VeridianPlanGateNone,
				blockedClass: map[string]string{"google": domain.VeridianPlanBlockClassCap}},
		},
		{
			name:     "plainte : 2 envoyes sur un plafond ralenti a 3, reste 1",
			provider: domain.EmailProvider{VeridianProviderClassDailyCap: map[string]int{"google": 10}},
			world: planParityWorld{
				complaints:      map[string]int{"envoi.example": 1},
				domainClassSent: map[string]map[string]int{"envoi.example": {"google": 2}},
			},
			want: expect{limiting: domain.VeridianPlanGateNone},
		},
		{
			name:     "chauffe ET plafond de classe : les deux comptent (reservation)",
			provider: domain.EmailProvider{VeridianWarmupStartedAt: &startedToday, VeridianWarmupSchedule: []int{50}, VeridianProviderClassDailyCap: map[string]int{"google": 2}},
			world:    planParityWorld{domainSent: map[string]int{"envoi.example": 2}, domainClassSent: map[string]map[string]int{"envoi.example": {"google": 2}}},
			want: expect{dailyCap: intp(50), limiting: domain.VeridianPlanGateWarmup, remaining: intp(48),
				sendable: map[string]bool{"google": false}, blockedClass: map[string]string{"google": domain.VeridianPlanBlockClassCap}},
		},
		{
			name:     "plusieurs portes : la plus basse fixe le plafond du jour",
			provider: domain.EmailProvider{VeridianProfileDailyCap: 15, VeridianPerSenderDailyCap: 100, VeridianWarmupStartedAt: &startedToday, VeridianWarmupSchedule: []int{40}},
			world:    planParityWorld{profileReserved: 5, domainSent: map[string]int{"envoi.example": 5}, senderSent: map[string]int{"hello@envoi.example": 5}},
			want:     expect{dailyCap: intp(15), limiting: domain.VeridianPlanGateProfileCap, remaining: intp(10)},
		},
		{
			name:     "profil en pause : rien ne part",
			provider: domain.EmailProvider{VeridianPaused: true},
			want: expect{limiting: domain.VeridianPlanGateNone,
				sendable: map[string]bool{"google": false, "microsoft": false, "yahoo_aol": false, "corporate_selfhost": false}},
		},
		{
			name:     "classe exclue : seule cette classe est bloquee",
			provider: domain.EmailProvider{VeridianExcludedProviderClasses: []string{"microsoft"}},
			want: expect{limiting: domain.VeridianPlanGateNone, sendable: map[string]bool{"microsoft": false},
				blockedClass: map[string]string{"microsoft": domain.VeridianPlanBlockExcludedClass}},
		},
		{
			name:     "fenetre fermee aujourd'hui : rien ne part",
			provider: domain.EmailProvider{VeridianSendingWindow: &domain.VeridianSendingWindow{Days: []int{closedDay}, StartHour: 0, EndHour: 24, Timezone: "UTC"}},
			want: expect{limiting: domain.VeridianPlanGateNone,
				sendable: map[string]bool{"google": false, "microsoft": false, "yahoo_aol": false, "corporate_selfhost": false}},
		},
		{
			name:     "fenetre ouverte toute la semaine : tout part",
			provider: domain.EmailProvider{VeridianSendingWindow: &domain.VeridianSendingWindow{Days: []int{0, 1, 2, 3, 4, 5, 6}, StartHour: 0, EndHour: 24, Timezone: "UTC"}},
			want:     expect{limiting: domain.VeridianPlanGateNone},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newVeridianThrottleTestEnv(t)
			ws := planParityWorkspace(tc.provider, tc.settings)
			planParityArmRepo(env, tc.world)

			plan := planParityPlan(env, ws, tc.world)
			require.True(t, plan.Applicable)

			for class, recipient := range planParityRecipients {
				workerSays := workerWouldSend(env, ws, recipient)
				planSays := planClass(plan, class).SendableNow
				expected := true
				if v, ok := tc.want.sendable[class]; ok {
					expected = v
				}
				assert.Equal(t, expected, workerSays, "le worker, classe %s", class)
				assert.Equal(t, workerSays, planSays, "le plan doit dire comme le worker, classe %s (bloque par: %q)", class, planClass(plan, class).BlockedBy)
				if reason, ok := tc.want.blockedClass[class]; ok {
					assert.Equal(t, reason, planClass(plan, class).BlockedBy, "raison, classe %s", class)
				}
			}

			assert.Equal(t, tc.want.limiting, plan.LimitingGate)
			if tc.want.dailyCap == nil {
				assert.Nil(t, plan.DailyCapToday)
			} else if assert.NotNil(t, plan.DailyCapToday) {
				assert.Equal(t, *tc.want.dailyCap, *plan.DailyCapToday)
			}
			if tc.want.remaining != nil && assert.NotNil(t, plan.RemainingToday) {
				assert.Equal(t, *tc.want.remaining, *plan.RemainingToday)
			}
		})
	}
}

// Le plan lit le MÊME plafond que la porte : on le prouve en faisant varier le
// paramètre. Un plafond de classe passé de 5 à 8 doit bouger dans le plan ET
// dans la décision du worker au même seuil (une mesure qui ne bouge pas quand son
// paramètre bouge ne le mesure pas).
func TestVeridianEffectivePlan_PlanMovesWithTheCapTheWorkerReads(t *testing.T) {
	for _, classCap := range []int{5, 8} {
		env := newVeridianThrottleTestEnv(t)
		world := planParityWorld{domainClassSent: map[string]map[string]int{"envoi.example": {"google": 6}}}
		ws := planParityWorkspace(domain.EmailProvider{VeridianProviderClassDailyCap: map[string]int{"google": classCap}}, domain.WorkspaceSettings{})
		planParityArmRepo(env, world)
		plan := planParityPlan(env, ws, world)
		google := planClass(plan, "google")
		require.NotNil(t, google.DailyCap)
		assert.Equal(t, classCap, *google.DailyCap)
		assert.Equal(t, classCap > 6, workerWouldSend(env, ws, "lead@gmail.com"), "cap %d", classCap)
		assert.Equal(t, classCap > 6, google.SendableNow, "cap %d", classCap)
	}
}

func TestVeridianEffectivePlan_SlowdownFactorAndReasonPerCouple(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	world := planParityWorld{complaints: map[string]int{"envoi.example": 2}}
	ws := planParityWorkspace(domain.EmailProvider{VeridianProviderClassRates: map[string]float64{"google": 8}}, domain.WorkspaceSettings{})
	planParityArmRepo(env, world)
	plan := planParityPlan(env, ws, world)
	assert.True(t, plan.ReputationAlert)
	assert.Equal(t, 2, plan.Complaints7d)
	assert.Equal(t, 4, plan.DomainSlowdownFactor)
	google := planClass(plan, "google")
	assert.Equal(t, 4, google.Factor)
	assert.Equal(t, "complaint", google.Reason)
	assert.InDelta(t, 2.0, google.RatePerMin, 0.0001, "8/min divisé par 4")
	assert.InDelta(t, 8.0, google.RateConfigured, 0.0001)
	assert.False(t, google.Stopped, "une plainte ralentit, ne stoppe jamais")
	assert.True(t, google.SendableNow)
	assert.Equal(t, "profile", plan.ClassRatesSource)
}

func TestVeridianEffectivePlan_TransactionalProfileHasNoCommercialGates(t *testing.T) {
	ws := planParityWorkspace(domain.EmailProvider{VeridianProfileDailyCap: 5}, domain.WorkspaceSettings{})
	ws.Settings.VeridianMarketingEmailProviderIDs = nil
	ws.Settings.TransactionalEmailProviderID = "prof"
	plan := VeridianEffectivePlan(VeridianPlanInput{
		Workspace: ws, IntegrationID: "prof", Now: time.Now(),
		Observed: domain.VeridianPlanObserved{SenderSent: map[string]int{"hello@envoi.example": 12}},
	})
	assert.False(t, plan.Applicable)
	assert.Equal(t, domain.VeridianProfileUsageTransactional, plan.Mode)
	assert.Nil(t, plan.DailyCapToday, "le worker ne plafonne pas le transactionnel")
	assert.Equal(t, 12, plan.SentToday, "volume du jour tout de meme visible")
	assert.Empty(t, plan.Gates)
}

func TestVeridianEffectivePlan_UnknownProfileIsNotApplicable(t *testing.T) {
	ws := planParityWorkspace(domain.EmailProvider{}, domain.WorkspaceSettings{})
	plan := VeridianEffectivePlan(VeridianPlanInput{Workspace: ws, IntegrationID: "absent", Now: time.Now()})
	assert.False(t, plan.Applicable)
	assert.Equal(t, domain.VeridianProfileUsageUnassigned, plan.Mode)
}

func TestVeridianEffectivePlan_LimitingGateTieBreaksInDeclaredOrder(t *testing.T) {
	ws := planParityWorkspace(domain.EmailProvider{VeridianProfileDailyCap: 20, VeridianPerSenderDailyCap: 20}, domain.WorkspaceSettings{})
	plan := VeridianEffectivePlan(VeridianPlanInput{Workspace: ws, IntegrationID: "prof", Now: time.Now()})
	require.NotNil(t, plan.DailyCapToday)
	assert.Equal(t, 20, *plan.DailyCapToday)
	assert.Equal(t, domain.VeridianPlanGateProfileCap, plan.LimitingGate, "à égalité, profile_cap passe avant per_sender")
	assert.Len(t, plan.Gates, 2)
}

func TestVeridianEffectivePlan_WindowReportsNextOpening(t *testing.T) {
	today := int(time.Now().UTC().Weekday())
	ws := planParityWorkspace(domain.EmailProvider{VeridianSendingWindow: &domain.VeridianSendingWindow{Days: []int{(today + 2) % 7}, StartHour: 9, EndHour: 18, Timezone: "UTC"}}, domain.WorkspaceSettings{})
	plan := VeridianEffectivePlan(VeridianPlanInput{Workspace: ws, IntegrationID: "prof", Now: time.Now()})
	assert.True(t, plan.Window.Configured)
	assert.False(t, plan.Window.OpenNow)
	assert.Equal(t, "profile", plan.Window.Source)
	require.NotNil(t, plan.Window.NextOpenAt)
	assert.True(t, plan.Window.NextOpenAt.After(time.Now()))
	assert.Contains(t, plan.BlockedBy, domain.VeridianPlanBlockWindowClosed)
	assert.False(t, plan.SendableNow)
}

func TestVeridianEffectivePlan_UnverifiedProfileIsOutOfRotation(t *testing.T) {
	ws := planParityWorkspace(domain.EmailProvider{}, domain.WorkspaceSettings{})
	ws.Integrations[0].EmailProvider.VeridianTransportVerifiedAt = nil
	ws.Settings.VeridianMarketingEmailProviderIDs = []string{"prof", "autre"}
	plan := VeridianEffectivePlan(VeridianPlanInput{Workspace: ws, IntegrationID: "prof", Now: time.Now()})
	assert.Contains(t, plan.BlockedBy, domain.VeridianPlanBlockUnverified)
	assert.False(t, plan.SendableNow)
}
