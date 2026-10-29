package reconcile

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJudgeStatut_EveryCauseHasFrenchTextAndConsistentAction(t *testing.T) {
	all := append(append([]statutCause{}, statutCauses...), statutSentCauses...)
	for _, c := range all {
		tx, ok := statutTexts[c.code]
		require.True(t, ok, "texte manquant pour la cause %s", c.code)
		f := judgeStatut([]statutRow{{Cause: c.code, AutomationID: "seq", Automation: "Séquence", Count: 3, Sample: []string{"a@x.fr"}}})
		require.Len(t, f, 1, c.code)
		assert.NotEmpty(t, f[0].Title)
		assert.NotEmpty(t, f[0].Explanation)
		assert.Equal(t, c.code+":seq", f[0].SubjectRef)
		assert.NotContains(t, f[0].Title+f[0].Explanation, " — ", "pas de tiret cadratin entouré d'espaces")
		if tx.actionKind == "" {
			assert.Nil(t, f[0].ProposedAction, "un fait passé n'a pas de réparation : %s", c.code)
		} else {
			require.NotNil(t, f[0].ProposedAction, c.code)
			assert.Equal(t, tx.actionKind, f[0].ProposedAction.Kind)
			assert.Equal(t, c.code, f[0].ProposedAction.Params["cause"])
			assert.Equal(t, "seq", f[0].ProposedAction.Params["automation_id"])
		}
		if c.enrolled {
			assert.Equal(t, ActionExitContacts, tx.actionKind, c.code)
			assert.NotEmpty(t, c.exitReason, "une cause d'inscription fixe la raison de sortie : %s", c.code)
		}
	}
}

func TestJudgeStatut_RepliedStillEnrolled(t *testing.T) {
	old := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	fs := judgeStatut([]statutRow{{Cause: causeReplied, AutomationID: "p5", Automation: "Prospection ASD 3 mails", Count: 2, Sample: []string{"a@x.fr", "b@x.fr"}, Oldest: &old}})
	require.Len(t, fs, 1)
	f := fs[0]
	assert.Equal(t, SeverityWarning, f.Severity)
	assert.Equal(t, "2 contacts ont répondu mais restent encore inscrits à « Prospection ASD 3 mails »", f.Title)
	assert.Equal(t, "2026-10-28T08:00:00Z", f.Evidence["oldest_at"])
	assert.Equal(t, "Sortir 2 contacts de la séquence", f.ProposedAction.Label)
}

func TestJudgeStatut_SingularAndExcludedIsCritical(t *testing.T) {
	fs := judgeStatut([]statutRow{
		{Cause: causeExcluded, AutomationID: "p5", Automation: "Prospection", Count: 1},
		{Cause: causeSentUnsub, AutomationID: "p5", Automation: "Prospection", Count: 1},
	})
	require.Len(t, fs, 2)
	for _, f := range fs {
		assert.Equal(t, SeverityCritical, f.Severity, f.SubjectRef)
	}
	assert.Equal(t, "1 contact d'une liste d'exclusion est inscrit à « Prospection »", fs[0].Title)
	assert.Equal(t, "1 contact a reçu un mail de « Prospection » après sa désinscription", fs[1].Title)
}

func TestJudgeStatut_CleanAndEmptyRows(t *testing.T) {
	assert.Empty(t, judgeStatut(nil))
	assert.Empty(t, judgeStatut([]statutRow{{Cause: causeReplied, AutomationID: "a", Count: 0}}))
	assert.Empty(t, judgeStatut([]statutRow{{Cause: "inconnue", AutomationID: "a", Count: 4}}))
}

func TestStatutQueries_ShapeAndGuards(t *testing.T) {
	for _, c := range statutCauses {
		q := c.groupQuery()
		assert.Contains(t, q, "$1", c.code)
		assert.NotContains(t, strings.ToUpper(q), "UPDATE ", "un contrôle ne modifie rien : %s", c.code)
		assert.NotContains(t, strings.ToUpper(q), "DELETE ", c.code)
	}
	// Une séquence qui gère la réponse elle-même n'est jamais jugée sur la réponse.
	replied, _ := statutCauseByCode(causeReplied)
	assert.Contains(t, replied.from, "reply_branch")
	qr, _ := statutCauseByCode(causeQueueReplied)
	assert.Contains(t, qr.from, "reply_branch")
	// Les envois déjà partis ne se ciblent pas pour réparation.
	_, found := statutCauseByCode(causeSentReply)
	assert.False(t, found)
}
