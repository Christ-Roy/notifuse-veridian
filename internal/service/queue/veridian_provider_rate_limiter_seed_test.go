package queue

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

// === Veridian — amorçage durable du throttle par classe (mission 2026-10-05) ===
//
// Constat prod (robertbrunon) : 181 envois entre 8h et 9h pour un débit
// nominal combiné d'environ 54/h (2 infras × ~27/h). Le job notifuse s'est
// redéployé plusieurs fois par jour début octobre (`nomad-v raw job history
// notifuse`) ; le throttle par classe est un token-bucket EN MÉMOIRE PURE qui
// perd tout son état à chaque redémarrage. Sans amorçage, chaque redémarrage
// proche de l'ouverture de la fenêtre (8h) regrant un jeton gratuit par
// {intégration, classe} pile au moment où le backlog de la nuit se libère.

// --- 1. AllowSeeded en isolation (zéro worker, zéro mock) ---

func TestProviderClassRateLimiter_AllowSeeded_FreshKeyNoRecentSend_GrantsBurst(t *testing.T) {
	prl := NewProviderClassRateLimiter()
	recentlySent := func() bool { return false } // rien envoyé récemment : comportement non-régression

	assert.True(t, prl.AllowSeeded("int-1", "google", 60, recentlySent), "clé neuve sans envoi récent : le burst reste accordé")
}

func TestProviderClassRateLimiter_AllowSeeded_FreshKeyButRecentDurableSend_DeniesFreeBurst(t *testing.T) {
	prl := NewProviderClassRateLimiter()
	recentlySent := func() bool { return true } // un envoi durable a déjà eu lieu dans l'intervalle

	assert.False(t, prl.AllowSeeded("int-1", "google", 60, recentlySent),
		"clé neuve (ex: juste après un redémarrage) MAIS un envoi durable existe déjà dans l'intervalle : pas de jeton gratuit")

	// Le jeton de démarrage a bien été consommé par l'amorçage : l'appel
	// suivant, même sans redemander recentlySent, reste throttlé (burst=1
	// déjà consommé), exactement comme s'il n'y avait jamais eu de redémarrage.
	assert.False(t, prl.AllowSeeded("int-1", "google", 60, func() bool { return false }))
}

func TestProviderClassRateLimiter_AllowSeeded_OnlyQueriesOnFirstTouch(t *testing.T) {
	prl := NewProviderClassRateLimiter()
	calls := 0
	recentlySent := func() bool { calls++; return false }

	prl.AllowSeeded("int-1", "google", 6000, recentlySent) // premier contact : interroge
	prl.AllowSeeded("int-1", "google", 6000, recentlySent) // clé déjà connue : ne doit PAS réinterroger
	prl.AllowSeeded("int-1", "google", 6000, recentlySent)

	assert.Equal(t, 1, calls, "recentlySent n'est consulté qu'au premier contact avec cette clé")
}

// --- 2. Bout-en-bout via le gate, reproduit le scénario "redémarrage à 8h" ---

// TestEmailQueueWorker_ProviderClassGate_RestartDoesNotGrantFreeTokenAfterDurableSend
// simule exactement l'incident : le process vient de (re)démarrer (limiter
// vide, comme au tout premier appel), mais message_history prouve qu'un
// envoi pour cette classe/ce domaine émetteur a déjà eu lieu il y a quelques
// secondes (bien avant le redémarrage). Sans le correctif, ce premier appel
// post-redémarrage partirait quand même (jeton de burst gratuit) ; avec le
// correctif, il est throttlé comme n'importe quel appel qui suit un envoi
// récent.
func TestEmailQueueWorker_ProviderClassGate_RestartDoesNotGrantFreeTokenAfterDurableSend(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// ~27/h, le débit nominal réel de robertbrunon pour la classe google.
	workspace := veridianTestWorkspace(map[string]float64{"google": 0.45}, 6000)
	entry := veridianTestEntryFrom("e1", "lead@gmail.com", "hello@nord-propre.example", domain.EmailQueuePayload{})

	// Un envoi google depuis ce domaine a eu lieu il y a 5s, DURABLEMENT
	// (message_history) — bien avant que ce process (fraîchement redémarré)
	// n'ait eu la moindre mémoire de cette clé.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "nord-propre.example", gomock.Any()).
		Return(1, nil)

	delay, throttled := env.worker.veridianProviderClassGate(workspace, &workspace.Integrations[0].EmailProvider, entry)
	assert.True(t, throttled, "le redémarrage ne doit pas offrir un envoi gratuit déjà couvert par un envoi durable récent")
	assert.Positive(t, delay)
}

// Non-régression : un process fraîchement démarré SANS aucun envoi durable
// récent sur cette clé envoie normalement dès le premier appel (le
// comportement historique, celui que toutes les autres suites de ce paquet
// vérifient déjà par ailleurs).
func TestEmailQueueWorker_ProviderClassGate_FreshProcessNoRecentSend_StillGrantsFirstBurst(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(map[string]float64{"google": 0.45}, 6000)
	entry := veridianTestEntryFrom("e1", "lead@gmail.com", "hello@nord-propre.example", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "nord-propre.example", gomock.Any()).
		Return(0, nil)

	delay, throttled := env.worker.veridianProviderClassGate(workspace, &workspace.Integrations[0].EmailProvider, entry)
	assert.False(t, throttled)
	assert.Zero(t, delay)
}
