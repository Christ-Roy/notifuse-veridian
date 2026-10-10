package queue

import (
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestVeridianResolveCapLimits_SharedResolution(t *testing.T) {
	provider := &domain.EmailProvider{VeridianProfileDailyCap: 9, VeridianPerSenderDailyCap: 3, VeridianProviderClassDailyCap: map[string]int{"google": 8}}
	ws := &domain.Workspace{ID: "ws-1"}
	lim := veridianResolveCapLimits(ws, provider, &domain.EmailQueueEntry{},
		func() string { return "google" }, func(string) int { return 4 }, time.Now())
	assert.Equal(t, 9, lim.Profile)
	assert.Equal(t, 3, lim.PerSender)
	assert.Equal(t, 8, lim.ClassBase)
	assert.Equal(t, 2, lim.ClassCap, "8 divisé par 4")
	assert.Equal(t, 4, lim.Factor)

	noClass := veridianResolveCapLimits(ws, nil, &domain.EmailQueueEntry{}, func() string { t.Fatal("pas de classification sans table de plafonds"); return "" }, nil, time.Now())
	assert.Zero(t, noClass.ClassBase)
}

func TestVeridianEffectiveClassRate(t *testing.T) {
	rates := map[string]float64{"google": 6}
	assert.InDelta(t, 6.0, veridianEffectiveClassRate(rates, "google", 1), 0.0001)
	assert.InDelta(t, 1.5, veridianEffectiveClassRate(rates, "google", 4), 0.0001)
	assert.Zero(t, veridianEffectiveClassRate(rates, "microsoft", 4))
	assert.InDelta(t, 6.0, veridianEffectiveClassRate(rates, "google", 0), 0.0001, "facteur invalide = 1")
}

func TestVeridianResolveCapLimits_WarmupPerRecipientAndCascade(t *testing.T) {
	started := time.Now().UTC().Add(-time.Hour)
	provider := &domain.EmailProvider{VeridianWarmupStartedAt: &started, VeridianWarmupSchedule: []int{12}, VeridianPerRecipientDailyCap: 2}
	ws := &domain.Workspace{ID: "ws-1", Settings: domain.WorkspaceSettings{VeridianPerSenderDailyCap: 7, VeridianPerRecipientDailyCap: 9}}

	lim := veridianResolveCapLimits(ws, provider, &domain.EmailQueueEntry{}, nil, nil, time.Now())
	assert.Equal(t, 12, lim.Warmup, "rampe de chauffe du jour")
	assert.Equal(t, 2, lim.PerRecipient, "le profil prime sur le workspace")
	assert.Equal(t, 7, lim.PerSender, "le workspace sert de repli")
	assert.Zero(t, lim.Profile, "SMTP sans plafond profil declare")

	// Un override de payload (fige a l'enqueue) prime sur le profil, comme dans les portes.
	entry := &domain.EmailQueueEntry{Payload: domain.EmailQueuePayload{VeridianPerRecipientDailyCap: 1, VeridianPerSenderDailyCap: 3}}
	lim = veridianResolveCapLimits(ws, provider, entry, nil, nil, time.Now())
	assert.Equal(t, 1, lim.PerRecipient)
	assert.Equal(t, 3, lim.PerSender)

	// Gmail : le defaut prudent de 30/jour s'applique sans configuration.
	gmail := &domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, SMTP: &domain.SMTPSettings{Host: "smtp.gmail.com"}}
	assert.Equal(t, domain.VeridianGmailDefaultDailyCap, veridianResolveCapLimits(ws, gmail, &domain.EmailQueueEntry{}, nil, nil, time.Now()).Profile)

	// Fonction pure : un provider nil ne panique pas.
	assert.NotPanics(t, func() { veridianResolveCapLimits(nil, nil, &domain.EmailQueueEntry{}, nil, nil, time.Now()) })
}

// Lot 5 (08/10/2026) : la chauffe avance par JOUR DE COMPTE du profil (date civile du fuseau de
// sa fenetre d'envoi), la meme frontiere que les compteurs du jour. Un warmup demarre a 18h Paris
// passe au palier suivant a minuit Paris. Echec demontre en revenant a « 24 h ecoulees » : le
// palier restait a 1 jusqu'a 18h le lendemain.
func TestVeridianResolveCapLimits_WarmupStepFollowsTheAccountDayOfTheProfile(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	assert.NoError(t, err)
	started := time.Date(2026, 6, 10, 18, 0, 0, 0, paris)
	window := &domain.VeridianSendingWindow{Days: []int{1, 2, 3, 4, 5}, StartHour: 8, EndHour: 19, Timezone: "Europe/Paris"}
	provider := &domain.EmailProvider{
		VeridianWarmupStartedAt: &started, VeridianWarmupSchedule: []int{1, 2, 5}, VeridianSendingWindow: window,
	}
	ws := &domain.Workspace{ID: "ws-1", Settings: domain.WorkspaceSettings{Timezone: "Europe/Paris"}}

	// 00h30 Paris le 11 : six heures et demie apres le debut, mais deja le jour de compte suivant.
	afterMidnight := time.Date(2026, 6, 11, 0, 30, 0, 0, paris)
	assert.Equal(t, 2, veridianResolveCapLimits(ws, provider, &domain.EmailQueueEntry{}, nil, nil, afterMidnight).Warmup)
	// 23h59 Paris le 10 : encore le jour de depart.
	beforeMidnight := time.Date(2026, 6, 10, 23, 59, 0, 0, paris)
	assert.Equal(t, 1, veridianResolveCapLimits(ws, provider, &domain.EmailQueueEntry{}, nil, nil, beforeMidnight).Warmup)
	// 17h59 Paris le 11 : moins de 24 h apres le debut, mais le jour 1 depuis minuit.
	nextAfternoon := time.Date(2026, 6, 11, 17, 59, 0, 0, paris)
	assert.Equal(t, 2, veridianResolveCapLimits(ws, provider, &domain.EmailQueueEntry{}, nil, nil, nextAfternoon).Warmup)
}

// Une classe fine sans réglage propre est bridée comme other_hoster (débit et
// plafond), pas illimitée ; une entrée propre l'emporte ; google reste non bridée.
func TestVeridianGateLimits_FineClassInheritsOtherHoster(t *testing.T) {
	rates := map[string]float64{"other_hoster": 0.12, "gandi": 0.03}
	assert.InDelta(t, 0.12, veridianEffectiveClassRate(rates, "lws", 1), 1e-9)
	assert.InDelta(t, 0.06, veridianEffectiveClassRate(rates, "lws", 2), 1e-9, "le fusible ÷2 s'applique à l'hérité")
	assert.InDelta(t, 0.03, veridianEffectiveClassRate(rates, "gandi", 1), 1e-9)
	assert.Zero(t, veridianEffectiveClassRate(rates, "google", 1))

	provider := &domain.EmailProvider{VeridianProviderClassDailyCap: map[string]int{"other_hoster": 150}}
	lim := veridianResolveCapLimits(&domain.Workspace{ID: "ws-1"}, provider, &domain.EmailQueueEntry{},
		func() string { return "o2switch" }, nil, time.Now())
	assert.Equal(t, 150, lim.ClassBase, "plafond hérité d'other_hoster")
}
