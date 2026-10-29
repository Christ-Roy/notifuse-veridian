package reconcile

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)

func TestJudgeOrphans_RepairableAndNot(t *testing.T) {
	fs := judgeOrphans([]orphanRow{
		{ID: "m1", Contact: "a@x.fr", Subject: "Re: Votre site", At: t0, Candidate: "out1"},
		{ID: "m2", Contact: "b@x.fr", Subject: "Bonjour", At: t0.Add(-time.Hour)},
	})
	require.Len(t, fs, 1)
	f := fs[0]
	assert.Equal(t, "orphelines:reply_human", f.SubjectRef)
	assert.Equal(t, "2 réponses humaines sans envoi rattaché", f.Title)
	assert.Equal(t, "2 orphelines dont 1 rattachable", f.Observed)
	assert.Contains(t, f.Explanation, "2 réponses humaines ne sont reliées à aucun envoi par les références du mail, mais 1 a un envoi du même contact")
	require.NotNil(t, f.ProposedAction)
	assert.Equal(t, ActionLinkReplies, f.ProposedAction.Kind)
	assert.Equal(t, "Rattacher 1 réponse à l'envoi probable", f.ProposedAction.Label)
}

func TestJudgeOrphans_NoCandidateMeansNoAction(t *testing.T) {
	fs := judgeOrphans([]orphanRow{{ID: "m2", Contact: "b@x.fr", Subject: "Bonjour", At: t0}})
	require.Len(t, fs, 1)
	assert.Nil(t, fs[0].ProposedAction)
	assert.Contains(t, fs[0].Explanation, "1 réponse humaine n'est reliée à aucun envoi")
	assert.Contains(t, fs[0].Explanation, "Elle n'apparaît dans aucun fil")
	assert.Empty(t, judgeOrphans(nil))
}

func TestJudgeEnvoye_ThreeCauses(t *testing.T) {
	old := t0
	fs := judgeEnvoye([]envoyeRow{
		{Cause: causeNoOutcome, Count: 3, Sample: []string{"id1"}, Oldest: &old},
		{Cause: causeBounceLost, Count: 1, Sample: []string{"x@y.fr"}},
		{Cause: causeNotMirrored, Count: 12},
		{Cause: causeNoOutcome, Count: 0},
	})
	require.Len(t, fs, 3)
	by := map[string]Finding{}
	for _, f := range fs {
		by[f.SubjectRef] = f
	}
	assert.Equal(t, SeverityCritical, by["envoye:sans_issue"].Severity)
	assert.Equal(t, "3 mails sans issue depuis plus de 48 heures", by["envoye:sans_issue"].Title)
	assert.Equal(t, "2026-10-29T10:00:00Z", by["envoye:sans_issue"].Evidence["oldest_at"])
	assert.Equal(t, "1 rapport de non-remise définitif reçu sans effet sur l'historique", by["envoye:rebond_non_applique"].Title)
	assert.Equal(t, SeverityWarning, by["envoye:absent_de_la_boite"].Severity)
	assert.Empty(t, judgeEnvoye(nil))
}
