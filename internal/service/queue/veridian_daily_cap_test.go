package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

// Réutilise les helpers du package (newVeridianThrottleTestEnv, veridianTestEntry)
// définis dans veridian_provider_throttle_test.go.

func veridianTestWorkspaceWithCaps(classCaps map[string]int, perRecipientCap int) *domain.Workspace {
	return &domain.Workspace{
		ID: "ws-1",
		Settings: domain.WorkspaceSettings{
			VeridianProviderClassDailyCap: classCaps,
			VeridianPerRecipientDailyCap:  perRecipientCap,
		},
		Integrations: []domain.Integration{
			{
				ID: "int-1",
				EmailProvider: domain.EmailProvider{
					Kind:               domain.EmailProviderKindSMTP,
					RateLimitPerMinute: 6000,
				},
			},
		},
	}
}

func TestVeridianStartOfDayUTC(t *testing.T) {
	in := time.Date(2026, 6, 14, 15, 30, 45, 999, time.FixedZone("CEST", 2*3600))
	got := veridianStartOfDayUTC(in)
	// 15:30 CEST = 13:30 UTC → minuit UTC du 14.
	assert.Equal(t, time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC), got)
	assert.Equal(t, time.UTC, got.Location())
}

func TestVeridianResolveDailyCaps(t *testing.T) {
	t.Run("payload overrides workspace", func(t *testing.T) {
		ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 100}, 9)
		entry := veridianTestEntry("e", "a@gmail.com", domain.EmailQueuePayload{
			VeridianProviderClassDailyCap: map[string]int{"google": 1},
			VeridianPerRecipientDailyCap:  1,
		})
		classCaps, perRecipient := veridianResolveDailyCaps(ws, nil, entry)
		assert.Equal(t, map[string]int{"google": 1}, classCaps)
		assert.Equal(t, 1, perRecipient)
	})

	t.Run("falls back to workspace when payload empty", func(t *testing.T) {
		ws := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 5}, 3)
		entry := veridianTestEntry("e", "a@outlook.com", domain.EmailQueuePayload{})
		classCaps, perRecipient := veridianResolveDailyCaps(ws, nil, entry)
		assert.Equal(t, map[string]int{"microsoft": 5}, classCaps)
		assert.Equal(t, 3, perRecipient)
	})

	t.Run("nil workspace safe", func(t *testing.T) {
		entry := veridianTestEntry("e", "a@gmail.com", domain.EmailQueuePayload{VeridianPerRecipientDailyCap: 2})
		classCaps, perRecipient := veridianResolveDailyCaps(nil, nil, entry)
		assert.Nil(t, classCaps)
		assert.Equal(t, 2, perRecipient)
	})
}

func TestVeridianDailyCapGate_NoConfigIsNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	// Aucun cap : aucun COUNT ne doit être appelé, jamais cappé.
	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_PerRecipientReached(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 1) // 1 mail/jour/destinataire
	entry := veridianTestEntry("e1", "victim@gmail.com", domain.EmailQueuePayload{})

	// Déjà 1 envoi aujourd'hui → cap atteint → skip.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "victim@gmail.com", gomock.Any()).
		Return(1, nil)

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_PerRecipientUnderCapPasses(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 2)
	entry := veridianTestEntry("e1", "ok@gmail.com", domain.EmailQueuePayload{})

	// 1 envoi < cap 2 → passe.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "ok@gmail.com", gomock.Any()).
		Return(1, nil)

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_ClassReached(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 50}, 0)
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})

	// gmail.com → classe google, COUNT exact sur la classe persistée = 50 ≥ cap → skip.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClass(gomock.Any(), "ws-1", "google", gomock.Any()).
		Return(50, nil)

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_ClassUnderCapPasses(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 50}, 0)
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClass(gomock.Any(), "ws-1", "google", gomock.Any()).
		Return(49, nil)

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_CorporateClassCountedExactly(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"corporate": 10}, 0)
	// Classe `corporate` portée explicitement (tag amont) : depuis le Lot 4, un
	// domaine custom inconnu est classé par MX (→ corporate_selfhost) ; la classe
	// HISTORIQUE `corporate` reste un tag amont valide. Le COUNT est désormais un
	// match EXACT sur la colonne persistée (fix 28/09) : plus de logique
	// d'exclusion par liste de domaines, une classe = une chaîne, un compteur.
	entry := veridianTestEntry("e1", "ceo@acme-corp.com", domain.EmailQueuePayload{
		VeridianProviderClass: domain.ProviderClassCorporate,
	})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClass(gomock.Any(), "ws-1", "corporate", gomock.Any()).
		Return(10, nil)

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_PerRecipientWinsOverClass(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Le destinataire est testé EN PREMIER. S'il est atteint, on skip sans
	// même interroger le COUNT classe (le plus restrictif gagne, court-circuit).
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1000}, 1)
	entry := veridianTestEntry("e1", "victim@gmail.com", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "victim@gmail.com", gomock.Any()).
		Return(1, nil)
	// CountSentSinceForDomains NE doit PAS être appelé (court-circuit) — pas d'EXPECT.

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_ClassCheckedWhenRecipientUnderCap(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 5}, 10)
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})

	// destinataire sous le cap (0 < 10) → on continue vers le cap classe.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "lead@gmail.com", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClass(gomock.Any(), "ws-1", "google", gomock.Any()).
		Return(5, nil) // classe atteinte → skip

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_CountErrorFailsClosed(t *testing.T) {
	t.Run("per-recipient count error blocks SMTP", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		ws := veridianTestWorkspaceWithCaps(nil, 1)
		entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForContact(gomock.Any(), "ws-1", "a@gmail.com", gomock.Any()).
			Return(0, errors.New("db down"))

		delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.True(t, capped)
		assert.Equal(t, veridianDailyCapRecheckInterval, delay)
	})

	t.Run("class count error blocks SMTP", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1}, 0)
		entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForClass(gomock.Any(), "ws-1", "google", gomock.Any()).
			Return(0, errors.New("db down"))

		delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.True(t, capped)
		assert.Equal(t, veridianDailyCapRecheckInterval, delay)
	})
}

func TestVeridianDailyCapGate_ClassWithoutCapForResolvedClassSkipsCount(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// cap posé seulement sur microsoft, le destinataire est gmail (google) →
	// la classe google n'a pas de cap → aucun COUNT classe, passe.
	ws := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 5}, 0)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func veridianWarmupTestProvider(startedAt time.Time, schedule []int, stepDays int) *domain.EmailProvider {
	return &domain.EmailProvider{
		Kind:                    domain.EmailProviderKindSMTP,
		RateLimitPerMinute:      6000,
		VeridianWarmupStartedAt: &startedAt,
		VeridianWarmupSchedule:  schedule,
		VeridianWarmupStepDays:  stepDays,
	}
}

func TestVeridianWarmupCap(t *testing.T) {
	base := time.Now().UTC()
	assert.Equal(t, 0, veridianWarmupCap(nil, nil, base), "nil provider = no warmup")
	assert.Equal(t, 0, veridianWarmupCap(nil, &domain.EmailProvider{}, base), "no warmup config = 0")

	// Démarré aujourd'hui, courbe [1,2,5], palier 1 jour → jour 0 = 1.
	p := veridianWarmupTestProvider(base, []int{1, 2, 5}, 1)
	assert.Equal(t, 1, veridianWarmupCap(nil, p, base))
}

func TestVeridianDailyCapGate_WarmupOverridesStaticClassCap(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Workspace pose un cap statique généreux (google 1000), mais l'infra est en
	// warmup jour 0 (cap 1) → le warmup PRIME → 1 envoi TOTAL déjà fait depuis ce
	// domaine émetteur aujourd'hui = skip. Le warmup compte le TOTAL par domaine
	// émetteur (toutes classes), PAS par classe : CountSentSinceForDomains NE doit
	// PAS être appelé.
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1000}, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{1, 2, 5}, 1)
	entry := veridianTestEntryFrom("e1", "lead@gmail.com", "bot@send.fr", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "send.fr", gomock.Any()).
		Return(1, nil) // 1 >= warmupCap(1) → skip

	delay, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.True(t, capped, "le cap warmup (1) prime sur le cap statique (1000)")
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_WarmupAppliesEvenWithoutStaticClassCap(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Aucun cap statique configuré nulle part, mais l'infra est en warmup → le cap
	// warmup s'enforce quand même, sur le TOTAL par domaine émetteur.
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{2, 5}, 1)
	entry := veridianTestEntryFrom("e1", "lead@gmail.com", "bot@send.fr", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "send.fr", gomock.Any()).
		Return(1, nil) // 1 < warmupCap(2) → passe

	delay, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.False(t, capped, "jour 0 cap=2, déjà 1 envoyé → passe")
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_WarmupIsTotalAcrossClasses(t *testing.T) {
	// CŒUR DU FIX (Bug 1) : le warmup plafonne le TOTAL de l'infra, pas 5×classes.
	// Un envoi google ET un envoi microsoft depuis le MÊME domaine émetteur partagent
	// le compteur. À cap=2, après 2 envois (peu importe leur classe), le 3e (n'importe
	// quelle classe) est bloqué. On le prouve en frappant le gate avec une classe puis
	// l'autre : les deux comptent via le MÊME CountSentSinceForSenderDomain("send.fr").
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{2}, 1) // jour 0 cap=2

	for _, recipient := range []string{"lead@gmail.com" /*google*/, "lead@outlook.com" /*microsoft*/} {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestEntryFrom("e", recipient, "bot@send.fr", domain.EmailQueuePayload{})
		// Quelle que soit la classe destinataire, le COUNT est le TOTAL du domaine
		// émetteur (pas un COUNT par classe) → déjà 2 → bloqué.
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "send.fr", gomock.Any()).
			Return(2, nil)
		_, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
		assert.True(t, capped, "le total de l'infra (2) plafonne %s sans distinction de classe", recipient)
	}
}

func TestVeridianDailyCapGate_WarmupEnforcedOnMXClass(t *testing.T) {
	// CŒUR DU FIX (Bug 2) : le warmup s'enforce maintenant sur les classes MX, que le
	// COUNT-par-classe bypassait (VeridianDomainsForClass renvoie [] → COUNT 0 → jamais
	// capé). Destinataire @ovh.com → classifié en classe MX (ovh) ; le warmup compte
	// quand même le total par domaine émetteur → bloqué. CountSentSinceForDomains*
	// (chemin par classe) NE doit PAS être appelé.
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{3}, 1) // jour 0 cap=3
	// Tag amont = ovh (classe MX) pour forcer le chemin MX sans lookup réseau en test.
	entry := veridianTestEntryFrom("e1", "lead@some-corp.fr", "bot@send.fr", domain.EmailQueuePayload{
		VeridianProviderClass: domain.ProviderClassOVH,
	})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "send.fr", gomock.Any()).
		Return(3, nil) // 3 >= cap 3 → skip, MÊME pour une classe MX
	// CountSentSinceForDomains / CountSentSinceForDomainsAndSenderDomain : aucun EXPECT
	// → le warmup ne passe PAS par le COUNT-par-classe (anti-bypass MX).

	delay, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.True(t, capped, "le warmup s'enforce sur les classes MX (fix Bug 2)")
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_WarmupLegacyNoSenderNotEnforced(t *testing.T) {
	// Pas de FROM exploitable (legacy / pré-V53) → aucune infra attribuable → le warmup
	// ne peut pas s'enforcer (best-effort, pass). AUCUN COUNT appelé. Le cap-classe
	// statique n'est PAS non plus évalué (warmup gouverne le plafond de l'infra).
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1}, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{1}, 1)
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{}) // pas de FromAddress

	delay, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.False(t, capped, "sans domaine émetteur, le warmup n'est pas enforçable (fallback)")
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_WarmupCountErrorFailsClosed(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{1}, 1)
	entry := veridianTestEntryFrom("e1", "lead@gmail.com", "bot@send.fr", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "send.fr", gomock.Any()).
		Return(0, errors.New("db down"))

	delay, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_NoWarmupNoConfigStillNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Provider sans warmup + pas de cap → toujours no-op (non-régression).
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	provider := &domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, RateLimitPerMinute: 6000}
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	delay, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_PayloadTagDrivesClass(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Le tag payload force la classe microsoft même si l'email est corporate.
	ws := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 1}, 0)
	entry := veridianTestEntry("e1", "ceo@acme-corp.com", domain.EmailQueuePayload{
		VeridianProviderClass: domain.ProviderClassMicrosoft,
	})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClass(gomock.Any(), "ws-1", "microsoft", gomock.Any()).
		Return(1, nil)

	_, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
}

// --- Cap par classe KEYÉ PAR INFRA ÉMETTRICE (warm-up multi-domaine, 2026-06-18) ---

// veridianTestEntryFrom construit une entrée avec une adresse FROM (sender) figée,
// pour exercer l'attribution du cap-classe à l'infra émettrice.
func veridianTestEntryFrom(id, recipient, from string, payload domain.EmailQueuePayload) *domain.EmailQueueEntry {
	payload.FromAddress = from
	return veridianTestEntry(id, recipient, payload)
}

func TestVeridianDailyCapGate_ClassPerInfra_UsesSenderDomainCount(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1}, 0)
	// Sender sur agences-veridian.fr : le cap-classe doit compter PAR CE DOMAINE,
	// pas workspace-global. CountSentSinceForClass (global) NE doit PAS être appelé.
	entry := veridianTestEntryFrom("e1", "lead@gmail.com", "bot1@agences-veridian.fr", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "agences-veridian.fr", gomock.Any()).
		Return(1, nil) // cette infra a déjà atteint son quota google du jour → skip

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_ClassPerInfra_TwoInfrasCappedIndependently(t *testing.T) {
	// Cœur du ticket : deux domaines d'envoi distincts frappant la MÊME classe
	// (google) sous le même cap. Le compteur est séparé par infra → l'une au quota
	// ne bloque pas l'autre.
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1}, 0)

	t.Run("infra A au quota → skip", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestEntryFrom("eA", "lead@gmail.com", "a@infra-a.fr", domain.EmailQueuePayload{})
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "infra-a.fr", gomock.Any()).
			Return(1, nil) // 1 >= cap 1 → bloqué
		_, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.True(t, capped, "infra-a au quota doit être bloquée")
	})

	t.Run("infra B sous le quota → passe (indépendant de A)", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestEntryFrom("eB", "lead@gmail.com", "b@infra-b.fr", domain.EmailQueuePayload{})
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "infra-b.fr", gomock.Any()).
			Return(0, nil) // 0 < cap 1 → passe, même si A est au quota
		_, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.False(t, capped, "infra-b sous le quota ne doit PAS être bloquée par le quota de A")
	})
}

func TestVeridianDailyCapGate_ClassPerInfra_SeveralAddressesSameDomainShareCount(t *testing.T) {
	// Les N adresses d'un MÊME domaine partagent la réputation → comptent ensemble.
	// Deux senders différents mais MÊME domaine émetteur → même clé de COUNT.
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 5}, 0)

	for _, from := range []string{"alice@agences-veridian.fr", "bob@agences-veridian.fr"} {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestEntryFrom("e", "lead@gmail.com", from, domain.EmailQueuePayload{})
		// Quel que soit l'alias, la clé de COUNT est le domaine commun.
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "agences-veridian.fr", gomock.Any()).
			Return(5, nil) // domaine au quota → skip pour TOUTE adresse du domaine
		_, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.True(t, capped, "toutes les adresses du domaine partagent le compteur de classe")
	}
}

func TestVeridianDailyCapGate_ClassPerInfra_LegacyNoSenderFallsBackToGlobal(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1}, 0)
	// Pas de FromAddress (legacy / pré-V53) → fallback au COUNT workspace-global.
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClass(gomock.Any(), "ws-1", "google", gomock.Any()).
		Return(1, nil)
	// CountSentSinceForClassAndSenderDomain NE doit PAS être appelé (pas d'EXPECT).

	_, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
}

func TestVeridianDailyCapGate_ClassPerInfra_CountErrorFailsClosed(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1}, 0)
	entry := veridianTestEntryFrom("e1", "lead@gmail.com", "bot@agences-veridian.fr", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "agences-veridian.fr", gomock.Any()).
		Return(0, errors.New("db down"))

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianCountClassForInfra_SenderDomainDerivation(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	since := veridianStartOfDayUTC(time.Now())
	const class = "google"

	t.Run("derives lowercase domain from mixed-case FROM", func(t *testing.T) {
		entry := veridianTestEntryFrom("e", "lead@gmail.com", "Bot@Agences-Veridian.FR", domain.EmailQueuePayload{})
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", class, "agences-veridian.fr", since).
			Return(2, nil)
		got, err := env.worker.veridianCountClassForInfra("ws-1", class, entry, since)
		assert.NoError(t, err)
		assert.Equal(t, 2, got)
	})

	t.Run("empty FROM → workspace-global count", func(t *testing.T) {
		entry := veridianTestEntry("e", "lead@gmail.com", domain.EmailQueuePayload{})
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForClass(gomock.Any(), "ws-1", class, since).
			Return(9, nil)
		got, err := env.worker.veridianCountClassForInfra("ws-1", class, entry, since)
		assert.NoError(t, err)
		assert.Equal(t, 9, got)
	})
}

// TestVeridianDailyCapGate_MXClass_PerIntegrationRotation prouve le "et en plus
// par intégration" demandé : deux intégrations (domaines d'envoi distincts)
// frappant la MÊME classe dérivée du MX tournent chacune sur leur PROPRE
// plafond, sans se marcher dessus ni se neutraliser.
func TestVeridianDailyCapGate_MXClass_PerIntegrationRotation(t *testing.T) {
	ws := veridianTestWorkspaceWithCaps(map[string]int{domain.ProviderClassOtherHoster: 5}, 0)

	t.Run("integration nord-propre-1 au plafond -> bloquée", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestEntryFrom("e1", "lead@custom-host.example", "bot@nord-propre-1.fr", domain.EmailQueuePayload{
			VeridianProviderClass: domain.ProviderClassOtherHoster,
		})
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", domain.ProviderClassOtherHoster, "nord-propre-1.fr", gomock.Any()).
			Return(5, nil)
		_, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.True(t, capped, "nord-propre-1 a atteint son plafond other_hoster")
	})

	t.Run("integration relai-agence-2 sous son plafond -> passe (indépendant de nord-propre-1)", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestEntryFrom("e2", "lead@custom-host.example", "bot@relai-agence-2.fr", domain.EmailQueuePayload{
			VeridianProviderClass: domain.ProviderClassOtherHoster,
		})
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", domain.ProviderClassOtherHoster, "relai-agence-2.fr", gomock.Any()).
			Return(2, nil)
		_, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.False(t, capped, "relai-agence-2 est sous son propre plafond, non affectée par nord-propre-1")
	})
}

// TestVeridianDailyCapGate_MXClass_ResumesNextDay prouve le "report au
// lendemain, jamais abandonné" : le gate ne fait que REPORTER (reschedule),
// jamais échouer/supprimer l'entrée ; et une fois le jour calendaire UTC changé
// (minuit passé), le compteur "depuis since" repart de zéro pour la même classe.
func TestVeridianDailyCapGate_MXClass_ResumesNextDay(t *testing.T) {
	ws := veridianTestWorkspaceWithCaps(map[string]int{domain.ProviderClassOtherHoster: 3}, 0)

	t.Run("plafond atteint aujourd'hui -> reporté (jamais abandonné)", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestEntryFrom("e1", "lead@custom-host.example", "bot@send.fr", domain.EmailQueuePayload{
			VeridianProviderClass: domain.ProviderClassOtherHoster,
		})
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", domain.ProviderClassOtherHoster, "send.fr", gomock.Any()).
			Return(3, nil)
		delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.True(t, capped, "le plafond atteint REPORTE l'entrée, il ne l'abandonne jamais")
		assert.Equal(t, veridianDailyCapRecheckInterval, delay, "reprogrammation bornée, pas de suppression")
	})

	t.Run("nouveau jour calendaire UTC -> le compteur du jour repart de zéro", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestEntryFrom("e2", "lead@custom-host.example", "bot@send.fr", domain.EmailQueuePayload{
			VeridianProviderClass: domain.ProviderClassOtherHoster,
		})
		// Le gate calcule `since` = minuit UTC du jour COURANT à chaque appel
		// (time.Now(), pas un état persistant) : le mock renvoie 0 pour simuler
		// un jour neuf où rien n'a encore été envoyé depuis ce since.
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", domain.ProviderClassOtherHoster, "send.fr", gomock.Any()).
			Return(0, nil)
		_, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.False(t, capped, "un nouveau jour calendaire UTC repart avec un compteur à zéro")
	})
}

// TestVeridianStartOfDayUTC_MidnightEuropeParisEdge prouve le cas limite
// explicitement demandé : le jour calendaire du plafond est TOUJOURS le jour UTC,
// jamais le jour Europe/Paris. À l'été (CEST, UTC+2), 01h00 Paris = 23h00 UTC LA
// VEILLE : ce n'est PAS encore un nouveau jour de plafond avant 02h00 Paris. En
// hiver (CET, UTC+1), la bascule est à 01h00 Paris. Un opérateur qui suppose "le
// plafond repart à minuit heure de Paris" se trompe de ±1-2h ; ce test fixe le
// comportement réel pour que Robert (ou un futur agent) ne suppose pas l'inverse.
func TestVeridianStartOfDayUTC_MidnightEuropeParisEdge(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	assert.NoError(t, err)

	t.Run("été (CEST, UTC+2) : 01h00 Paris est ENCORE la veille en UTC", func(t *testing.T) {
		// 2026-07-14 01:00 Europe/Paris = 2026-07-13 23:00 UTC.
		parisTime := time.Date(2026, 7, 14, 1, 0, 0, 0, paris)
		got := veridianStartOfDayUTC(parisTime)
		assert.Equal(t, time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), got,
			"à 01h Paris l'été, le jour UTC (et donc le plafond) est encore celui de la veille")
	})

	t.Run("été (CEST, UTC+2) : 02h00 Paris bascule sur le nouveau jour UTC", func(t *testing.T) {
		// 2026-07-14 02:00 Europe/Paris = 2026-07-14 00:00 UTC pile.
		parisTime := time.Date(2026, 7, 14, 2, 0, 0, 0, paris)
		got := veridianStartOfDayUTC(parisTime)
		assert.Equal(t, time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC), got,
			"à 02h Paris l'été, minuit UTC vient de sonner : nouveau jour de plafond")
	})

	t.Run("hiver (CET, UTC+1) : minuit Paris est encore la veille en UTC", func(t *testing.T) {
		// 2026-01-14 00:30 Europe/Paris = 2026-01-13 23:30 UTC.
		parisTime := time.Date(2026, 1, 14, 0, 30, 0, 0, paris)
		got := veridianStartOfDayUTC(parisTime)
		assert.Equal(t, time.Date(2026, 1, 13, 0, 0, 0, 0, time.UTC), got,
			"à 00h30 Paris l'hiver, le jour UTC est encore celui de la veille")
	})

	t.Run("hiver (CET, UTC+1) : 01h00 Paris bascule sur le nouveau jour UTC", func(t *testing.T) {
		// 2026-01-14 01:00 Europe/Paris = 2026-01-14 00:00 UTC pile.
		parisTime := time.Date(2026, 1, 14, 1, 0, 0, 0, paris)
		got := veridianStartOfDayUTC(parisTime)
		assert.Equal(t, time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC), got,
			"à 01h Paris l'hiver, minuit UTC vient de sonner : nouveau jour de plafond")
	})
}

func TestVeridianDailyCapGate_ClassCapDividedByReputationFactor(t *testing.T) {
	ws := veridianTestWorkspaceWithCaps(map[string]int{"ionos": 8}, 0)
	entry := func() *domain.EmailQueueEntry {
		return veridianTestEntryFrom("e1", "lead@ionos.example", "r@"+repTestDomain, domain.EmailQueuePayload{VeridianProviderClass: "ionos"})
	}

	// 3 envois du jour : sous le plafond 8, mais au-dessus de 8 ÷ 4 = 2.
	env := newVeridianThrottleTestEnv(t)
	env.mockMessageHistoryRepo.EXPECT().CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "ionos", repTestDomain, gomock.Any()).Return(3, nil).AnyTimes()
	_, capped := env.worker.veridianDailyCapGate(ws, nil, entry())
	assert.False(t, capped, "plafond normal 8, 3 envois : libre")

	env.worker.reputationFactors.set("ws-1", repTestDomain, "ionos", 4)
	_, capped = env.worker.veridianDailyCapGate(ws, nil, entry())
	assert.True(t, capped, "plafond ÷4 = 2, 3 envois : plafonne")
}

// Lot 2 : la porte lit ses plafonds dans la resolution partagee (veridianResolveCapLimits),
// ralentissement du fusible de reputation compris : 8 / 4 = 2 envois vers google.
func TestVeridianDailyCapGate_UsesSharedLimitsWithReputationSlowdown(t *testing.T) {
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 8}, 0)
	entry := veridianTestEntryFrom("e1", "lead@gmail.com", "bot@agences-veridian.fr", domain.EmailQueuePayload{VeridianProviderClass: "google"})

	for _, tc := range []struct {
		name   string
		factor int
		sent   int
		capped bool
	}{
		{"sans ralentissement, 7 sur 8 : passe", 1, 7, false},
		{"sans ralentissement, 8 sur 8 : plafonne", 1, 8, true},
		{"÷4, 1 sur 2 : passe", 4, 1, false},
		{"÷4, 2 sur 2 : plafonne", 4, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newVeridianThrottleTestEnv(t)
			if tc.factor > 1 {
				env.worker.reputationFactors.set("ws-1", "agences-veridian.fr", "google", tc.factor)
			}
			env.mockMessageHistoryRepo.EXPECT().
				CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "agences-veridian.fr", gomock.Any()).
				Return(tc.sent, nil)
			_, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
			assert.Equal(t, tc.capped, capped)
		})
	}
}

// Lot 4 (08/10/2026) : le plafond de chauffe d'un domaine se compte depuis minuit
// heure de Paris quand la fenetre du profil est a Paris.
func TestVeridianDailyCapGate_WarmupCountsSinceParisMidnight(t *testing.T) {
	withFixedClock(t, time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)) // jour de passage a l'heure d'ete
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 60)
	provider := parisWindowProvider()
	started := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	provider.VeridianWarmupStartedAt = &started
	provider.VeridianWarmupSchedule = []int{5}
	entry := veridianTestEntryFrom("e", "lead@gmail.com", "hello@envoi.example", domain.EmailQueuePayload{})

	var since time.Time
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "envoi.example", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, s time.Time) (int, error) { since = s; return 5, nil })

	_, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.True(t, capped, "5 envois du jour pour un palier de 5")
	assert.Equal(t, time.Date(2026, 3, 28, 23, 0, 0, 0, time.UTC), since, "minuit Paris le 29 mars 2026 (UTC+1) = 23h00 UTC la veille")
}
