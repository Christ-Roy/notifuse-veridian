package queue

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
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
