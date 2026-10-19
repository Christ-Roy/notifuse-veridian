package queue

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

// Chemin PAR DÉFAUT (19/10/2026) : un workspace SANS aucune table de plafonds ni de
// débits est bridé par les défauts serveur, pour TOUTE classe. Preuve du défaut d'avant :
// sur le code précédent (garde « len(classCaps) > 0 »), ces tests voient blocked == 0.

type veridianMapMXResolver map[string][]string

func (m veridianMapMXResolver) LookupMXHosts(_ context.Context, d string) ([]string, error) {
	if h, ok := m[d]; ok {
		return h, nil
	}
	return nil, fmt.Errorf("no MX (test resolver)")
}

func TestVeridianDailyCapGate_DefaultsApplyToEveryClassWithoutConfig(t *testing.T) {
	enableVeridianClassDefaults(t)
	for _, class := range domain.VeridianAllProviderClasses() {
		class := class
		t.Run(class, func(t *testing.T) {
			worker, fakeRepo := newVeridianDailyCapFakeEnv(t)
			ws := veridianTestWorkspaceWithCaps(nil, 0) // AUCUNE configuration
			def := domain.VeridianDefaultCapForClass(class)

			blocked := 0
			attempts := def + 7
			for i := 0; i < attempts; i++ {
				entry := veridianTestEntryFrom(fmt.Sprintf("e%d", i), fmt.Sprintf("lead%d@x-%s.example", i, class), "bot@send.fr",
					domain.EmailQueuePayload{VeridianProviderClass: class})
				if _, capped := worker.veridianDailyCapGate(ws, nil, entry); capped {
					blocked++
					continue
				}
				fakeRepo.record(class, "send.fr", entry.ContactEmail, time.Now().UTC())
			}
			assert.Equal(t, 7, blocked, "le plafond par défaut (%d) doit arrêter l'envoi de la classe %s", def, class)
		})
	}
}

// Domaine custom hébergé Google Workspace : classé google par MX à l'envoi, compté dans
// la classe google (colonne matérialisée), plafonné par le défaut google.
func TestVeridianDailyCapGate_CustomDomainOnGoogleMXCountsAsGoogle(t *testing.T) {
	enableVeridianClassDefaults(t)
	worker, fakeRepo := newVeridianDailyCapFakeEnv(t)
	worker.SetVeridianMXClassifier(domain.NewVeridianMXClassifier(veridianMapMXResolver{
		"cabinet-dupont.fr": {"aspmx.l.google.com", "alt1.aspmx.l.google.com"},
	}))
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	def := domain.VeridianDefaultCapForClass(domain.ProviderClassGoogle)

	blocked := 0
	for i := 0; i < def+5; i++ {
		entry := veridianTestEntryFrom(fmt.Sprintf("g%d", i), fmt.Sprintf("p%d@cabinet-dupont.fr", i), "bot@send.fr", domain.EmailQueuePayload{})
		// Le worker fige la classe avant les portes (worker.go) : on reproduit ce geste.
		entry.Payload.VeridianProviderClass = worker.veridianClassifyRecipient(entry)
		assert.Equal(t, domain.ProviderClassGoogle, entry.Payload.VeridianProviderClass)
		if _, capped := worker.veridianDailyCapGate(ws, nil, entry); capped {
			blocked++
			continue
		}
		fakeRepo.record(entry.Payload.VeridianProviderClass, "send.fr", entry.ContactEmail, time.Now().UTC())
	}
	assert.Equal(t, 5, blocked)
}

// Étiquette non canonique posée à l'import (infomaniak_pro, godaddy...) : ignorée, le MX tranche.
func TestVeridianClassify_NonCanonicalTagFallsBackToMX(t *testing.T) {
	worker, _ := newVeridianDailyCapFakeEnv(t)
	worker.SetVeridianMXClassifier(domain.NewVeridianMXClassifier(veridianMapMXResolver{
		"boite.fr": {"mx1.mail.ovh.net"},
	}))
	entry := veridianTestEntryFrom("t1", "x@boite.fr", "bot@send.fr", domain.EmailQueuePayload{VeridianProviderClass: "godaddy"})
	assert.Equal(t, domain.ProviderClassOVH, worker.veridianClassifyRecipient(entry))
	entry.Payload.VeridianProviderClass = "Hostinger "
	assert.Equal(t, domain.ProviderClassOVH, worker.veridianClassifyRecipient(entry), "casse/espace non canoniques : MX décide aussi")
}

// Sans classifieur MX, le repli suffixe est VISIBLE (compteur) au lieu d'être silencieux.
func TestVeridianClassify_MissingMXClassifierIsCounted(t *testing.T) {
	worker, _ := newVeridianDailyCapFakeEnv(t)
	worker.providerMXClassifier = nil
	entry := veridianTestEntryFrom("t2", "x@boite.fr", "bot@send.fr", domain.EmailQueuePayload{})
	assert.Equal(t, domain.ProviderClassCorporate, worker.veridianClassifyRecipient(entry))
	assert.Equal(t, int64(1), worker.mxFallbackCount.Load())
}

// Débit par défaut : sans table, le 2e envoi immédiat vers une même classe est retenu.
func TestVeridianProviderClassGate_DefaultRateThrottlesWithoutConfig(t *testing.T) {
	enableVeridianClassDefaults(t)
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(nil, 6000)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})
	_, throttled := env.worker.veridianProviderClassGate(workspace, nil, entry)
	assert.False(t, throttled, "1er envoi : jeton de burst")
	delay, throttled := env.worker.veridianProviderClassGate(workspace, nil, entry)
	assert.True(t, throttled, "2e envoi immédiat : retenu par le débit par défaut (%.2f/min)", domain.VeridianDefaultRateForClass(domain.ProviderClassGoogle))
	assert.Greater(t, delay, time.Duration(0))
}

func TestVeridianEffectiveClassRate_DefaultsWhenNoTable(t *testing.T) {
	enableVeridianClassDefaults(t)
	for _, c := range domain.VeridianAllProviderClasses() {
		assert.InDelta(t, domain.VeridianDefaultRateForClass(c), veridianEffectiveClassRate(nil, c, 1), 1e-9, c)
	}
}
