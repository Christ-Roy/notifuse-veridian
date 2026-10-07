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
