package reconcile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Les requêtes de l'invariant 1 sont jouées sur un vrai PostgreSQL par
// TestIntegration_EnvoyeEtReponseRattachee ; ici, le jugement et ses textes.
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
