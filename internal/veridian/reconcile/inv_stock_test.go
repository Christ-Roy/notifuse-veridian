package reconcile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJudgeStock_ExcludedClientsInflateStock(t *testing.T) {
	// Rapport UX N1 : 5 371 annoncés, 771 clients exclus, 4 600 prospectables.
	fs := judgeStock(stockRow{Served: 5371, Base: 4600, ExcludedInServed: 771})
	require.Len(t, fs, 1)
	f := fs[0]
	assert.Equal(t, "stock:total", f.SubjectRef)
	assert.Equal(t, "Stock restant : 5 371 annoncés, 4 600 réels", f.Title)
	assert.Contains(t, f.Explanation, "771 personnes de la différence sont des personnes d'une liste d'exclusion")
	assert.NotContains(t, f.Explanation, "ne s'expliquent pas")
}

func TestJudgeStock_UnexplainedGapAndEqual(t *testing.T) {
	assert.Empty(t, judgeStock(stockRow{Served: 100, Base: 100}))
	fs := judgeStock(stockRow{Served: 110, Base: 100, ExcludedInServed: 4})
	require.Len(t, fs, 1)
	assert.Contains(t, fs[0].Explanation, "6 personnes de la différence ne s'expliquent pas")
	under := judgeStock(stockRow{Served: 90, Base: 100})
	require.Len(t, under, 1)
	assert.Contains(t, under[0].Explanation, "10 de plus")
}

func TestJudgeQueueDup(t *testing.T) {
	fs := judgeQueueDup([]queueDupRow{{SourceType: "automation", SourceID: "p5", Name: "Prospection", Count: 3, Sample: []string{"a@x.fr"}}})
	require.Len(t, fs, 1)
	f := fs[0]
	assert.Equal(t, SeverityCritical, f.Severity)
	assert.Equal(t, "file_deja_envoye:p5", f.SubjectRef)
	assert.Equal(t, ActionCancelQueue, f.ProposedAction.Kind)
	assert.Equal(t, causeQueueDup, f.ProposedAction.Params["cause"])
	assert.Contains(t, f.Title, "3 envois en file pour « Prospection »")
	assert.Empty(t, judgeQueueDup([]queueDupRow{{SourceID: "p5", Count: 0}}))
}
