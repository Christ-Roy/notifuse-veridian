package queue

import (
	"fmt"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scission de freemail_fr (22/10/2026, fiche 69). Un contact déjà étiqueté freemail_fr
// par ODH, ou une entrée de file figée avec ce tag, doit être classé chez son vrai FAI
// à l'envoi, sans migration. Si rien ne le rattache à un FAI, il reste freemail_fr.
func TestVeridianClassify_LegacyFreemailTagIsSplitByFAI(t *testing.T) {
	worker, _ := newVeridianDailyCapFakeEnv(t)
	worker.SetVeridianMXClassifier(domain.NewVeridianMXClassifier(veridianMapMXResolver{
		"cabinet-orange.fr":  {"smtp-in.orange.fr"},
		"cabinet-sfr.fr":     {"smtp-in.sfr.fr"},
		"cabinet-free.fr":    {"mx1.free.fr", "mx2.free.fr"},
		"cabinet-poste.fr":   {"smtpz4.laposte.net"},
		"cabinet-bbox.fr":    {"mx.bbox.fr"},
		"cabinet-google.fr":  {"aspmx.l.google.com"},
		"cabinet-ovh.fr":     {"mx1.mail.ovh.net"},
		"cabinet-inconnu.fr": {"mail.perso-inconnu.example"},
	}))
	for _, tc := range []struct {
		email string
		want  string
	}{
		// suffixe d'adresse : zéro DNS
		{"a@orange.fr", domain.ProviderClassOrange},
		{"a@wanadoo.fr", domain.ProviderClassOrange},
		{"a@sfr.fr", domain.ProviderClassSFRAltice},
		{"a@neuf.fr", domain.ProviderClassSFRAltice},
		{"a@free.fr", domain.ProviderClassFree},
		{"a@laposte.net", domain.ProviderClassLaPoste},
		{"a@bbox.fr", domain.ProviderClassBouygues},
		// domaine pro hébergé chez le FAI : le MX tranche
		{"a@cabinet-orange.fr", domain.ProviderClassOrange},
		{"a@cabinet-sfr.fr", domain.ProviderClassSFRAltice},
		{"a@cabinet-free.fr", domain.ProviderClassFree},
		{"a@cabinet-poste.fr", domain.ProviderClassLaPoste},
		{"a@cabinet-bbox.fr", domain.ProviderClassBouygues},
		// rien ne le rattache à un FAI : la classe historique reste, bridée par ses défauts
		{"a@cabinet-inconnu.fr", domain.ProviderClassFreemailFR},
		{"a@sans-dns.example", domain.ProviderClassFreemailFR},
		{"a@gmx.fr", domain.ProviderClassFreemailFR},
		// un tag périmé ne fait pas basculer le contact hors de la famille FAI
		{"a@cabinet-google.fr", domain.ProviderClassFreemailFR},
		{"a@cabinet-ovh.fr", domain.ProviderClassFreemailFR},
	} {
		entry := veridianTestEntryFrom("t-"+tc.email, tc.email, "bot@send.fr",
			domain.EmailQueuePayload{VeridianProviderClass: domain.ProviderClassFreemailFR})
		assert.Equal(t, tc.want, worker.veridianClassifyRecipient(entry), tc.email)
	}
}

// Le plafond d'un FAI est le sien : 10 envois chez Orange n'entament pas celui de Free,
// y compris pour des entrées étiquetées freemail_fr.
func TestVeridianDailyCapGate_LegacyTaggedEntriesCountPerFAI(t *testing.T) {
	enableVeridianClassDefaults(t)
	worker, fakeRepo := newVeridianDailyCapFakeEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)

	capOrange := domain.VeridianDefaultCapForClass(domain.ProviderClassOrange)
	capFree := domain.VeridianDefaultCapForClass(domain.ProviderClassFree)
	assert.Less(t, capOrange, capFree, "Orange, le plus sévère, a le plafond le plus bas")

	send := func(id, email string) bool {
		entry := veridianTestEntryFrom(id, email, "bot@send.fr",
			domain.EmailQueuePayload{VeridianProviderClass: domain.ProviderClassFreemailFR})
		// Le worker fige la classe avant les portes (worker.go) : même geste.
		entry.Payload.VeridianProviderClass = worker.veridianClassifyRecipient(entry)
		if _, capped := worker.veridianDailyCapGate(ws, nil, entry); capped {
			return false
		}
		fakeRepo.record(entry.Payload.VeridianProviderClass, "send.fr", entry.ContactEmail, time.Now().UTC())
		return true
	}
	for i := 0; i < capOrange; i++ {
		assert.True(t, send(fmt.Sprintf("o%d", i), fmt.Sprintf("o%d@orange.fr", i)), "orange %d", i)
	}
	assert.False(t, send("o-over", "over@orange.fr"), "Orange plafonné à %d", capOrange)
	assert.True(t, send("f1", "f1@free.fr"), "Free n'est pas ralenti par Orange")
	assert.True(t, send("s1", "s1@sfr.fr"), "SFR n'est pas ralenti par Orange")
}

// La réservation atomique (autorité du plafond) compte sous la classe fine, pas sous freemail_fr,
// même quand l'entrée arrive avec le tag historique et que la classe n'a pas été refigée avant.
func TestVeridianReserveDailyQuota_LegacyFreemailTagReservesUnderFAI(t *testing.T) {
	enableVeridianClassDefaults(t)
	for email, want := range map[string]string{
		"lead@orange.fr": domain.ProviderClassOrange,
		"lead@sfr.fr":    domain.ProviderClassSFRAltice,
		"lead@free.fr":   domain.ProviderClassFree,
	} {
		env := newVeridianThrottleTestEnv(t)
		repo := &quotaTestRepository{outcomes: map[string]quotaTestOutcome{
			domain.VeridianDailyQuotaKindProviderClass: {result: domain.VeridianDailyQuotaReservationResult{Reserved: true, Used: 1}},
		}}
		env.worker.messageHistoryRepo = repo
		entry := veridianTestEntryFrom("legacy", email, "bot@send.test", domain.EmailQueuePayload{VeridianProviderClass: domain.ProviderClassFreemailFR})

		_, _, blocked := env.worker.veridianReserveDailyQuota(veridianTestWorkspaceWithCaps(nil, 0), nil, entry)
		require.False(t, blocked)
		require.Len(t, repo.specs, 1, email)
		assert.Equal(t, want, repo.specs[0].Key.ProviderClass, email)
		assert.Equal(t, domain.VeridianDefaultCapForClass(want), repo.specs[0].Cap, email)
	}
}
