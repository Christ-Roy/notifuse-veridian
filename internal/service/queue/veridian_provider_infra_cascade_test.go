package queue

// === Veridian patch — R2 cascade par INFRA d'envoi ===
// Tests de la cascade à 3 niveaux (broadcast → infra EmailProvider → workspace)
// pour les RATES (veridian_provider_throttle.go) et les CAPS journaliers
// (veridian_daily_cap.go). Priorités vérifiées : le niveau le plus spécifique
// non vide gagne, et un provider nil (legacy) saute le niveau infra =
// comportement strictement pré-R2 (non-régression).

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

func wsWithRates(rates map[string]float64) *domain.Workspace {
	return &domain.Workspace{ID: "ws-1", Settings: domain.WorkspaceSettings{VeridianProviderClassRates: rates}}
}

func wsWithCaps(classCaps map[string]int, perRecipient int) *domain.Workspace {
	return &domain.Workspace{ID: "ws-1", Settings: domain.WorkspaceSettings{
		VeridianProviderClassDailyCap: classCaps,
		VeridianPerRecipientDailyCap:  perRecipient,
	}}
}

func entryWithRatesPayload(rates map[string]float64) *domain.EmailQueueEntry {
	return &domain.EmailQueueEntry{
		ContactEmail: "x@gmail.com",
		Payload:      domain.EmailQueuePayload{VeridianProviderClassRates: rates},
	}
}

func TestVeridianResolveProviderClassRates_Cascade(t *testing.T) {
	wsRates := map[string]float64{"google": 10}
	infraRates := map[string]float64{"google": 5}
	bcastRates := map[string]float64{"google": 1}

	t.Run("infra prime sur workspace", func(t *testing.T) {
		provider := &domain.EmailProvider{VeridianProviderClassRates: infraRates}
		got := veridianResolveProviderClassRates(wsWithRates(wsRates), provider, entryWithRatesPayload(nil))
		assert.Equal(t, infraRates, got)
	})

	t.Run("broadcast prime sur infra", func(t *testing.T) {
		provider := &domain.EmailProvider{VeridianProviderClassRates: infraRates}
		got := veridianResolveProviderClassRates(wsWithRates(wsRates), provider, entryWithRatesPayload(bcastRates))
		assert.Equal(t, bcastRates, got)
	})

	t.Run("infra seul (workspace vide)", func(t *testing.T) {
		provider := &domain.EmailProvider{VeridianProviderClassRates: infraRates}
		got := veridianResolveProviderClassRates(wsWithRates(nil), provider, entryWithRatesPayload(nil))
		assert.Equal(t, infraRates, got)
	})

	t.Run("provider nil -> hérite workspace (pré-R2)", func(t *testing.T) {
		got := veridianResolveProviderClassRates(wsWithRates(wsRates), nil, entryWithRatesPayload(nil))
		assert.Equal(t, wsRates, got)
	})

	t.Run("provider sans rates -> hérite workspace", func(t *testing.T) {
		provider := &domain.EmailProvider{} // pas de rates Veridian
		got := veridianResolveProviderClassRates(wsWithRates(wsRates), provider, entryWithRatesPayload(nil))
		assert.Equal(t, wsRates, got)
	})

	t.Run("rien configuré -> nil (no-op)", func(t *testing.T) {
		got := veridianResolveProviderClassRates(wsWithRates(nil), &domain.EmailProvider{}, entryWithRatesPayload(nil))
		assert.Nil(t, got)
	})
}

func TestVeridianResolveDailyCaps_CascadeWithInfra(t *testing.T) {
	t.Run("infra cap classe prime sur workspace", func(t *testing.T) {
		provider := &domain.EmailProvider{VeridianProviderClassDailyCap: map[string]int{"google": 50}}
		ws := wsWithCaps(map[string]int{"google": 200}, 0)
		entry := &domain.EmailQueueEntry{ContactEmail: "x@gmail.com"}
		classCaps, perRecipient := veridianResolveDailyCaps(ws, provider, entry)
		assert.Equal(t, map[string]int{"google": 50}, classCaps)
		assert.Equal(t, 0, perRecipient)
	})

	t.Run("infra cap destinataire prime sur workspace", func(t *testing.T) {
		provider := &domain.EmailProvider{VeridianPerRecipientDailyCap: 1}
		ws := wsWithCaps(nil, 5)
		entry := &domain.EmailQueueEntry{ContactEmail: "x@gmail.com"}
		classCaps, perRecipient := veridianResolveDailyCaps(ws, provider, entry)
		assert.Nil(t, classCaps)
		assert.Equal(t, 1, perRecipient)
	})

	t.Run("broadcast cap prime sur infra", func(t *testing.T) {
		provider := &domain.EmailProvider{VeridianPerRecipientDailyCap: 5}
		ws := wsWithCaps(nil, 10)
		entry := &domain.EmailQueueEntry{
			ContactEmail: "x@gmail.com",
			Payload:      domain.EmailQueuePayload{VeridianPerRecipientDailyCap: 1},
		}
		_, perRecipient := veridianResolveDailyCaps(ws, provider, entry)
		assert.Equal(t, 1, perRecipient)
	})

	t.Run("cap classe et destinataire résolus indépendamment (infra classe + workspace destinataire)", func(t *testing.T) {
		provider := &domain.EmailProvider{VeridianProviderClassDailyCap: map[string]int{"microsoft": 30}}
		ws := wsWithCaps(map[string]int{"microsoft": 999}, 3) // workspace fournit le cap destinataire
		entry := &domain.EmailQueueEntry{ContactEmail: "x@outlook.com"}
		classCaps, perRecipient := veridianResolveDailyCaps(ws, provider, entry)
		assert.Equal(t, map[string]int{"microsoft": 30}, classCaps) // infra
		assert.Equal(t, 3, perRecipient)                            // workspace (infra ne définit pas)
	})

	t.Run("provider nil -> hérite workspace (pré-R2)", func(t *testing.T) {
		ws := wsWithCaps(map[string]int{"google": 100}, 7)
		entry := &domain.EmailQueueEntry{ContactEmail: "x@gmail.com"}
		classCaps, perRecipient := veridianResolveDailyCaps(ws, nil, entry)
		assert.Equal(t, map[string]int{"google": 100}, classCaps)
		assert.Equal(t, 7, perRecipient)
	})
}

// TestVeridianProviderClassGate_InfraRateApplied prouve que le gate consomme
// bien le débit posé au niveau INFRA quand ni broadcast ni workspace ne le
// fournissent (cascade bout-en-bout, pas juste la résolution).
func TestVeridianProviderClassGate_InfraRateApplied(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(nil, 6000) // workspace SANS rates Veridian
	provider := &domain.EmailProvider{
		Kind:                       domain.EmailProviderKindSMTP,
		RateLimitPerMinute:         6000,
		VeridianProviderClassRates: map[string]float64{"google": 1}, // 1 mail/min sur l'infra
	}
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	// 1er passage : un token dispo (burst), part.
	delay, throttled := env.worker.veridianProviderClassGate(workspace, provider, entry)
	assert.False(t, throttled)
	assert.Zero(t, delay)

	// 2e passage immédiat : token épuisé (1/min), throttlé via le débit INFRA.
	delay, throttled = env.worker.veridianProviderClassGate(workspace, provider, entry)
	assert.True(t, throttled)
	assert.Positive(t, delay)
}

// TestVeridianProviderClassGate_NilProviderNoRegression : provider nil +
// workspace sans rates = no-op strict (comportement pré-R2 inchangé).
func TestVeridianProviderClassGate_NilProviderNoRegression(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(nil, 6000)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	delay, throttled := env.worker.veridianProviderClassGate(workspace, nil, entry)
	assert.False(t, throttled)
	assert.Zero(t, delay)
}
