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
