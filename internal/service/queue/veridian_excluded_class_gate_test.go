package queue

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Les tests de ce fichier réutilisent le harness du pré-filtre
// (veridian_prefilter_test.go : newPrefilterEnv, prefilterWorkspace,
// expectPermanentSkip, veridianTestEntry) — même package queue, même contrat de
// skip PERMANENT (MarkAsProcessing → message_history FailedAt → Delete, jamais
// SendEmail). L'absence d'EXPECT sur SendEmail + ctrl.Finish() prouve qu'AUCUN
// SMTP n'est ouvert pour une classe exclue.

// excludedWorkspace construit un workspace mono-intégration SMTP avec une liste
// d'exclusion posée AU NIVEAU demandé (workspace / infra). Les tests payload
// posent l'exclusion directement sur l'entrée.
func excludedWorkspace(workspaceExcluded, infraExcluded []string) *domain.Workspace {
	ws := prefilterWorkspace()
	ws.Settings.VeridianExcludedProviderClasses = workspaceExcluded
	ws.Integrations[0].EmailProvider.VeridianExcludedProviderClasses = infraExcluded
	return ws
}

// resolverForMX renvoie un resolver MX de test mappant un domaine custom vers un
// hostname MX (pour piloter la classification MX du destinataire dans le gate).
func resolverForMX(mxByDomain map[string][]string) *prefilterTestResolver {
	return &prefilterTestResolver{mxByDomain: mxByDomain}
}

// TestExcludedClassGate_PayloadExclusionSkips : microsoft exclu via le payload
// (broadcast) → un destinataire microsoft (suffixe connu hotmail.com) part en
// échec PERMANENT, aucun SMTP ouvert.
func TestExcludedClassGate_PayloadExclusionSkips(t *testing.T) {
	env := newPrefilterEnv(t, &prefilterTestResolver{})
	entry := veridianTestEntry("e1", "prospect@hotmail.com", domain.EmailQueuePayload{
		VeridianExcludedProviderClasses: []string{"microsoft"},
	})
	env.expectPermanentSkip(t, "e1")
	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestExcludedClassGate_WorkspaceExclusionSkips : microsoft exclu au niveau
// WORKSPACE (fallback) → skip permanent. Couvre le niveau le plus général de la
// cascade.
func TestExcludedClassGate_WorkspaceExclusionSkips(t *testing.T) {
	env := newPrefilterEnv(t, &prefilterTestResolver{})
	entry := veridianTestEntry("e1", "prospect@outlook.fr", domain.EmailQueuePayload{})
	env.expectPermanentSkip(t, "e1")
	env.worker.processEntry(excludedWorkspace([]string{"microsoft"}, nil), entry)
}

// TestExcludedClassGate_InfraExclusionSkips : microsoft exclu au niveau INFRA
// (EmailProvider) → skip permanent. Couvre le niveau intermédiaire de la cascade.
func TestExcludedClassGate_InfraExclusionSkips(t *testing.T) {
	env := newPrefilterEnv(t, &prefilterTestResolver{})
	entry := veridianTestEntry("e1", "prospect@live.com", domain.EmailQueuePayload{})
	env.expectPermanentSkip(t, "e1")
	env.worker.processEntry(excludedWorkspace(nil, []string{"microsoft"}), entry)
}

// TestExcludedClassGate_PayloadWinsOverWorkspace : la cascade prend le niveau le
// plus spécifique. Le payload exclut google (≠ classe du destinataire microsoft),
// le workspace exclut microsoft → comme le payload GAGNE et n'exclut pas
// microsoft, le destinataire microsoft PASSE (envoi complet).
func TestExcludedClassGate_PayloadWinsOverWorkspace(t *testing.T) {
	env := newPrefilterEnv(t, &prefilterTestResolver{})
	ws := excludedWorkspace([]string{"microsoft"}, nil)
	entry := veridianTestEntry("e1", "prospect@hotmail.com", domain.EmailQueuePayload{
		VeridianExcludedProviderClasses: []string{"google"}, // payload gagne, microsoft non exclu
	})
	env.queue.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Return(nil)
	env.history.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.queue.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)
	env.worker.processEntry(ws, entry)
}

// TestExcludedClassGate_NonExcludedClassPasses : NON-RÉGRESSION. microsoft exclu,
// mais le destinataire est google → il PASSE et part en envoi complet (le reste
// du broadcast part normalement).
func TestExcludedClassGate_NonExcludedClassPasses(t *testing.T) {
	env := newPrefilterEnv(t, &prefilterTestResolver{})
	entry := veridianTestEntry("e1", "prospect@gmail.com", domain.EmailQueuePayload{
		VeridianExcludedProviderClasses: []string{"microsoft"},
	})
	env.queue.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Return(nil)
	env.history.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.queue.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)
	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestExcludedClassGate_NoExclusionIsNoop : NON-RÉGRESSION STRICTE. Aucune
// exclusion à aucun niveau → le gate est un no-op, l'adresse (gmail, suffixe
// connu) part en envoi complet sans même classer (le resolver vide prouve qu'on
// ne plante pas, et gmail court-circuite tout lookup).
func TestExcludedClassGate_NoExclusionIsNoop(t *testing.T) {
	env := newPrefilterEnv(t, &prefilterTestResolver{})
	entry := veridianTestEntry("e1", "prospect@gmail.com", domain.EmailQueuePayload{})
	env.queue.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Return(nil)
	env.history.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.queue.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)
	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestExcludedClassGate_MXClassifiedExclusionSkips : un domaine CUSTOM hébergé
// Microsoft (MX *.protection.outlook.com) est classé `microsoft` par le
// classifier MX (Lot 4), donc exclu et skippé — la précision MX vaut pour
// l'exclusion comme pour le throttle/cap.
func TestExcludedClassGate_MXClassifiedExclusionSkips(t *testing.T) {
	res := resolverForMX(map[string][]string{
		"cabinet-dupont.fr": {"cabinet-dupont-fr.mail.protection.outlook.com"},
	})
	env := newPrefilterEnv(t, res)
	entry := veridianTestEntry("e1", "contact@cabinet-dupont.fr", domain.EmailQueuePayload{
		VeridianExcludedProviderClasses: []string{"microsoft"},
	})
	env.expectPermanentSkip(t, "e1")
	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestExcludedClassGate_ContactTagPrimes : le tag amont (payload
// VeridianProviderClass, option B) prime sur la classification MX/suffixe. Un
// destinataire tagué microsoft mais sur un domaine gmail → la classe résolue est
// microsoft (tag), donc exclu et skippé. Cohérent avec le throttle/cap.
func TestExcludedClassGate_ContactTagPrimes(t *testing.T) {
	env := newPrefilterEnv(t, &prefilterTestResolver{})
	entry := veridianTestEntry("e1", "weird@gmail.com", domain.EmailQueuePayload{
		VeridianProviderClass:           "microsoft", // tag amont prime
		VeridianExcludedProviderClasses: []string{"microsoft"},
	})
	env.expectPermanentSkip(t, "e1")
	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestExcludedClassGate_FiresBeforeThrottle : ordre des gates. Une classe à la
// fois EXCLUE et fortement throttlée (rate très bas) doit partir en échec
// PERMANENT (Delete via expectPermanentSkip), PAS être reschedulée par le
// throttle (SetNextRetry). L'absence d'EXPECT sur SetNextRetry + ctrl.Finish()
// garantit que le throttle n'est jamais atteint pour une classe exclue.
func TestExcludedClassGate_FiresBeforeThrottle(t *testing.T) {
	env := newPrefilterEnv(t, &prefilterTestResolver{})
	entry := veridianTestEntry("e1", "prospect@hotmail.com", domain.EmailQueuePayload{
		VeridianExcludedProviderClasses: []string{"microsoft"},
		// Throttle qui, s'il était atteint AVANT l'exclusion, reschedulerait
		// (rate très bas) au lieu de Delete. Il ne doit jamais être atteint.
		VeridianProviderClassRates: map[string]float64{"microsoft": 0.0001},
	})
	env.expectPermanentSkip(t, "e1")
	env.worker.processEntry(prefilterWorkspace(), entry)
}

// --- Fiche 62 : variante structurée veridianExcludedClassVerdict ---

func TestVeridianExcludedClassVerdict_BlockedCarriesClassAndSortedExclusionList(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 6000)
	ws.Settings.VeridianExcludedProviderClasses = []string{"microsoft", "google"} // ordre volontairement non trié
	entry := veridianTestEntry("e1", "prospect@hotmail.com", domain.EmailQueuePayload{})

	v := env.worker.veridianExcludedClassVerdict(ws, nil, entry)
	require.True(t, v.Blocked())
	assert.Equal(t, domain.VeridianGateExcluded, v.Gate)
	assert.Equal(t, domain.VeridianVerdictBlock, v.Verdict)
	assert.Equal(t, "microsoft", v.Value, "valeur = classe du destinataire")
	assert.Equal(t, "microsoft", v.Class)
	assert.Equal(t, []string{"google", "microsoft"}, v.Limit, "limite = classes exclues, en ordre stable")
	assert.Equal(t, domain.VeridianReasonExcludedClass, v.Reason)
	assert.Zero(t, v.Delay, "une exclusion est permanente : pas de délai de re-planification")
}

func TestVeridianExcludedClassVerdict_NonExcludedClassPassesAndStillReportsWhatWasCompared(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 6000)
	ws.Settings.VeridianExcludedProviderClasses = []string{"microsoft"}
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})

	v := env.worker.veridianExcludedClassVerdict(ws, nil, entry)
	assert.False(t, v.Blocked())
	assert.Equal(t, domain.VeridianVerdictPass, v.Verdict)
	assert.Equal(t, "google", v.Value, "la classe du destinataire est tracée même quand elle passe")
	assert.Equal(t, []string{"microsoft"}, v.Limit)
	assert.Empty(t, v.Class, "Class n'est renseignée que pour un blocage")
	assert.Empty(t, v.Reason)
}

func TestVeridianExcludedClassVerdict_NoExclusionConfiguredIsStrictNoopWithoutClassification(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 6000)
	entry := veridianTestEntry("e1", "lead@hotmail.com", domain.EmailQueuePayload{})

	v := env.worker.veridianExcludedClassVerdict(ws, nil, entry)
	assert.False(t, v.Blocked())
	assert.Nil(t, v.Value, "aucune classification (pas de lookup MX) quand rien n'est exclu")
	assert.Nil(t, v.Limit)
	assert.Equal(t, "no exclusion configured", v.Detail)
}

func TestVeridianExcludedClassGate_WrapperMatchesVerdict(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 6000)
	ws.Settings.VeridianExcludedProviderClasses = []string{"microsoft"}

	class, excluded := env.worker.veridianExcludedClassGate(ws, nil, veridianTestEntry("e1", "p@hotmail.com", domain.EmailQueuePayload{}))
	assert.True(t, excluded)
	assert.Equal(t, "microsoft", class)

	class, excluded = env.worker.veridianExcludedClassGate(ws, nil, veridianTestEntry("e2", "p@gmail.com", domain.EmailQueuePayload{}))
	assert.False(t, excluded)
	assert.Empty(t, class)
}
