package domain

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubReplyStatsService : implémentation triviale prouvant que l'interface
// VeridianReplyStatsService est satisfiable et que sa signature est stable
// (contrat consommé par le handler HTTP). Pas de logique métier ici — le
// service réel est testé dans internal/service.
type stubReplyStatsService struct {
	gotReq *VeridianReplyStatsRequest
	stats  *VeridianReplyStats
}

func (s *stubReplyStatsService) GetReplyStats(_ context.Context, req *VeridianReplyStatsRequest) (*VeridianReplyStats, error) {
	s.gotReq = req
	return s.stats, nil
}

func TestVeridianReplyStatsService_InterfaceSatisfied(t *testing.T) {
	var _ VeridianReplyStatsService = (*stubReplyStatsService)(nil)
}

// La réponse est un contrat AVEC LE FRONT (console/src/services/api/veridian_reply_stats.ts
// attend exactement {"replied": N}). On pin la forme JSON pour qu'un renommage de
// champ casse ici plutôt que silencieusement à l'exécution (la carte KPI lirait 0).
func TestVeridianReplyStats_JSONShape(t *testing.T) {
	out, err := json.Marshal(VeridianReplyStats{Replied: 42})
	require.NoError(t, err)
	assert.JSONEq(t, `{"replied":42}`, string(out))

	// Round-trip : un payload backend se relit en struct sans perte.
	var back VeridianReplyStats
	require.NoError(t, json.Unmarshal([]byte(`{"replied":7}`), &back))
	assert.Equal(t, 7, back.Replied)
}

// La requête expose workspace_id en JSON (le handler accepte POST body OU query) ;
// Since/Until sont des bornes INTERNES résolues par le handler depuis start/end —
// elles ne doivent PAS apparaître dans le JSON (tag "-"), sinon un client pourrait
// croire qu'on les accepte telles quelles (alors qu'on attend start/end ISO).
func TestVeridianReplyStatsRequest_JSONShape(t *testing.T) {
	req := VeridianReplyStatsRequest{
		WorkspaceID: "ws-1",
		Since:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Until:       time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC),
	}
	out, err := json.Marshal(req)
	require.NoError(t, err)
	assert.JSONEq(t, `{"workspace_id":"ws-1"}`, string(out))

	// Décodage d'un body : workspace_id se lit, Since/Until restent zéro (résolus
	// ailleurs depuis start/end).
	var back VeridianReplyStatsRequest
	require.NoError(t, json.Unmarshal([]byte(`{"workspace_id":"ws-2"}`), &back))
	assert.Equal(t, "ws-2", back.WorkspaceID)
	assert.True(t, back.Since.IsZero())
	assert.True(t, back.Until.IsZero())
}
