package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

// fakeQueueExplainService enregistre les appels recus (aucun mock genere pour ce service).
type fakeQueueExplainService struct {
	explainCalls, decisionsCalls, recomputeCalls int

	gotWS        string
	gotExplain   domain.VeridianQueueExplainFilter
	gotDecisions domain.VeridianSendDecisionFilter
	gotRecompute domain.VeridianQueueRecomputeRequest

	explainOut   *domain.VeridianQueueExplain
	decisionsOut []*domain.VeridianSendDecision
	nextCursor   string
	level        string
	recomputeN   int
	err          error
}

func (f *fakeQueueExplainService) Explain(_ context.Context, ws string, fl domain.VeridianQueueExplainFilter) (*domain.VeridianQueueExplain, error) {
	f.explainCalls++
	f.gotWS, f.gotExplain = ws, fl
	return f.explainOut, f.err
}
func (f *fakeQueueExplainService) Decisions(_ context.Context, ws string, fl domain.VeridianSendDecisionFilter) ([]*domain.VeridianSendDecision, string, string, error) {
	f.decisionsCalls++
	f.gotWS, f.gotDecisions = ws, fl
	return f.decisionsOut, f.nextCursor, f.level, f.err
}
func (f *fakeQueueExplainService) Recompute(_ context.Context, r domain.VeridianQueueRecomputeRequest) (int, error) {
	f.recomputeCalls++
	f.gotRecompute = r
	return f.recomputeN, f.err
}

func newQueueExplainHandler(t *testing.T) (*VeridianQueueExplainHandler, *fakeQueueExplainService) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	svc := &fakeQueueExplainService{}
	h := NewVeridianQueueExplainHandler(svc, func() ([]byte, error) { return []byte("test-secret"), nil }, log)
	return h, svc
}

func jsonBody(t *testing.T, v interface{}) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewReader(b)
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
	return e["error"]
}

// Routes: GET et POST pour explain/decisions, POST seul pour recompute. Sans jeton: 401
// (donc le service n'est jamais atteint) ; methode non declaree: 405 (pas de catchall SPA).
func TestVeridianQueueExplainHandler_RegisterRoutes(t *testing.T) {
	h, svc := newQueueExplainHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/veridian/queue.explain?workspace_id=ws1", http.StatusUnauthorized},
		{http.MethodPost, "/api/veridian/queue.explain", http.StatusUnauthorized},
		{http.MethodGet, "/api/veridian/decisions.list?workspace_id=ws1", http.StatusUnauthorized},
		{http.MethodPost, "/api/veridian/decisions.list", http.StatusUnauthorized},
		{http.MethodPost, "/api/veridian/queue.recompute", http.StatusUnauthorized},
		{http.MethodGet, "/api/veridian/queue.recompute?workspace_id=ws1", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/api/veridian/queue.explain?workspace_id=ws1", http.StatusMethodNotAllowed},
		{http.MethodPut, "/api/veridian/decisions.list", http.StatusMethodNotAllowed},
		{http.MethodPut, "/api/veridian/queue.recompute", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			assert.Equal(t, tc.want, rec.Code)
		})
	}
	assert.Equal(t, 0, svc.explainCalls+svc.decisionsCalls+svc.recomputeCalls, "aucun appel service sans authentification")

	t.Run("jeton invalide -> 401", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/veridian/queue.explain?workspace_id=ws1", nil)
		r.Header.Set("Authorization", "Bearer pas-un-jwt")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, 0, svc.explainCalls)
	})
}

func TestVeridianQueueExplainHandler_Explain(t *testing.T) {
	t.Run("GET: tous les filtres de la query arrivent au service", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		svc.explainOut = &domain.VeridianQueueExplain{WorkspaceID: "ws1", Total: 42, Groups: []domain.VeridianQueueGroup{{Reason: "window_closed", Count: 42}}}
		r := httptest.NewRequest(http.MethodGet, "/api/veridian/queue.explain?workspace_id=ws1&group_by=node,%20reason,,class"+
			"&automation_id=a1&node_id=j0a&reason=window_closed&profile_id=p1&class=ovh&status=pending&entry_id=", nil)
		rec := httptest.NewRecorder()
		h.handleExplain(rec, r)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "ws1", svc.gotWS)
		assert.Equal(t, domain.VeridianQueueExplainFilter{
			GroupBy: []string{"node", "reason", "class"}, AutomationID: "a1", NodeID: "j0a", Reason: "window_closed",
			ProfileID: "p1", Class: "ovh", Status: "pending",
		}, svc.gotExplain)
		var out domain.VeridianQueueExplain
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, int64(42), out.Total)
		assert.Equal(t, "window_closed", out.Groups[0].Reason)
	})

	t.Run("POST: corps JSON, group_by en tableau, la query l'emporte", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		svc.explainOut = &domain.VeridianQueueExplain{}
		r := httptest.NewRequest(http.MethodPost, "/api/veridian/queue.explain?node_id=depuis-query",
			jsonBody(t, map[string]interface{}{
				"workspace_id": "ws1", "group_by": []string{"automation", "profile"},
				"node_id": "depuis-corps", "entry_id": "e1",
			}))
		rec := httptest.NewRecorder()
		h.handleExplain(rec, r)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, []string{"automation", "profile"}, svc.gotExplain.GroupBy)
		assert.Equal(t, "depuis-query", svc.gotExplain.NodeID)
		assert.Equal(t, "e1", svc.gotExplain.EntryID)
	})

	t.Run("workspace_id manquant -> 400, service non appele", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		rec := httptest.NewRecorder()
		h.handleExplain(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/queue.explain", nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, errorOf(t, rec), "workspace_id")
		assert.Equal(t, 0, svc.explainCalls)
	})

	t.Run("corps JSON invalide -> 400", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		rec := httptest.NewRecorder()
		h.handleExplain(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/queue.explain", bytes.NewReader([]byte("{pas du json"))))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, 0, svc.explainCalls)
	})

	t.Run("erreurs du service -> bon statut", func(t *testing.T) {
		cases := []struct {
			name string
			err  error
			want int
		}{
			{"entree absente", service.ErrVeridianQueueEntryNotFound, http.StatusNotFound},
			{"entree absente enveloppee", fmt.Errorf("x: %w", service.ErrVeridianQueueEntryNotFound), http.StatusNotFound},
			{"valeur invalide", errors.New(`invalid group_by value "zz"`), http.StatusBadRequest},
			{"droit manquant", domain.NewPermissionError(domain.PermissionResourceAutomations, domain.PermissionTypeRead, "no"), http.StatusForbidden},
			{"jeton revoque", &domain.ErrAuthenticationFailed{}, http.StatusUnauthorized},
			{"panne", errors.New("db down"), http.StatusInternalServerError},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h, svc := newQueueExplainHandler(t)
				svc.err = tc.err
				rec := httptest.NewRecorder()
				h.handleExplain(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/queue.explain?workspace_id=ws1", nil))
				assert.Equal(t, tc.want, rec.Code)
			})
		}
	})
}

func TestVeridianQueueExplainHandler_Decisions(t *testing.T) {
	t.Run("GET: filtres, limite, curseur, trace et fenetre transmis ; niveau et curseur rendus", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		svc.decisionsOut = []*domain.VeridianSendDecision{{ID: "d1", Outcome: domain.VeridianOutcomeDeferred}}
		svc.nextCursor, svc.level = "next-1", domain.VeridianDecisionLogAll
		before := time.Now()
		r := httptest.NewRequest(http.MethodGet, "/api/veridian/decisions.list?workspace_id=ws1&automation_id=a1&node_id=j0a"+
			"&email=x@y.fr&entry_id=e1&reason=window_closed&outcome=deferred&since=2h&limit=50&cursor=c0&trace=1", nil)
		rec := httptest.NewRecorder()
		h.handleDecisions(rec, r)

		require.Equal(t, http.StatusOK, rec.Code)
		f := svc.gotDecisions
		assert.Equal(t, "a1", f.AutomationID)
		assert.Equal(t, "j0a", f.NodeID)
		assert.Equal(t, "x@y.fr", f.Email)
		assert.Equal(t, "e1", f.EntryID)
		assert.Equal(t, "window_closed", f.Reason)
		assert.Equal(t, "deferred", f.Outcome)
		assert.Equal(t, 50, f.Limit)
		assert.Equal(t, "c0", f.Cursor)
		assert.True(t, f.WithTrace)
		require.NotNil(t, f.Since)
		assert.WithinDuration(t, before.Add(-2*time.Hour), *f.Since, 5*time.Second)

		var out struct {
			Decisions  []domain.VeridianSendDecision `json:"decisions"`
			NextCursor string                        `json:"next_cursor"`
			Level      string                        `json:"level"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		require.Len(t, out.Decisions, 1)
		assert.Equal(t, "next-1", out.NextCursor)
		assert.Equal(t, domain.VeridianDecisionLogAll, out.Level)
	})

	t.Run("POST avec corps: limite numerique JSON", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		r := httptest.NewRequest(http.MethodPost, "/api/veridian/decisions.list",
			jsonBody(t, map[string]interface{}{"workspace_id": "ws1", "limit": 7, "trace": true}))
		rec := httptest.NewRecorder()
		h.handleDecisions(rec, r)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 7, svc.gotDecisions.Limit)
		assert.True(t, svc.gotDecisions.WithTrace)
	})

	t.Run("liste vide -> [] et non null", func(t *testing.T) {
		h, _ := newQueueExplainHandler(t)
		rec := httptest.NewRecorder()
		h.handleDecisions(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/decisions.list?workspace_id=ws1", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"decisions":[]`)
	})

	t.Run("trace absente par defaut", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		rec := httptest.NewRecorder()
		h.handleDecisions(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/decisions.list?workspace_id=ws1&trace=0", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.False(t, svc.gotDecisions.WithTrace)
	})

	t.Run("entrees invalides -> 400, service non appele", func(t *testing.T) {
		for name, q := range map[string]string{
			"workspace manquant":   "",
			"limite non numerique": "workspace_id=ws1&limit=abc",
			"limite negative":      "workspace_id=ws1&limit=-5",
			"since illisible":      "workspace_id=ws1&since=hier",
			"since negatif":        "workspace_id=ws1&since=-3h",
		} {
			t.Run(name, func(t *testing.T) {
				h, svc := newQueueExplainHandler(t)
				rec := httptest.NewRecorder()
				h.handleDecisions(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/decisions.list?"+q, nil))
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				assert.Equal(t, 0, svc.decisionsCalls)
			})
		}
	})

	t.Run("erreurs du service -> bon statut", func(t *testing.T) {
		for name, tc := range map[string]struct {
			err  error
			want int
		}{
			"issue invalide": {errors.New(`invalid outcome "zz"`), http.StatusBadRequest},
			"droit manquant": {domain.NewPermissionError(domain.PermissionResourceAutomations, domain.PermissionTypeRead, "no"), http.StatusForbidden},
			"panne":          {errors.New("db down"), http.StatusInternalServerError},
		} {
			t.Run(name, func(t *testing.T) {
				h, svc := newQueueExplainHandler(t)
				svc.err = tc.err
				rec := httptest.NewRecorder()
				h.handleDecisions(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/decisions.list?workspace_id=ws1", nil))
				assert.Equal(t, tc.want, rec.Code)
			})
		}
	})
}

func TestVeridianParseSince(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	got, err := veridianParseSince("", now)
	require.NoError(t, err)
	assert.Nil(t, got)

	for in, want := range map[string]time.Time{
		"30m":                  now.Add(-30 * time.Minute),
		"2h":                   now.Add(-2 * time.Hour),
		"7d":                   now.Add(-7 * 24 * time.Hour),
		"2026-10-01T08:00:00Z": time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
	} {
		got, err := veridianParseSince(in, now)
		require.NoError(t, err, in)
		require.NotNil(t, got, in)
		assert.True(t, want.Equal(*got), "%s: attendu %s, obtenu %s", in, want, *got)
	}
	for _, bad := range []string{"hier", "0d", "-1d", "-2h", "0s", "xd"} {
		_, err := veridianParseSince(bad, now)
		assert.Error(t, err, bad)
	}
}

func TestVeridianQueueExplainHandler_Recompute(t *testing.T) {
	t.Run("POST: la demande complete est transmise, compte rendu", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		svc.recomputeN = 12
		r := httptest.NewRequest(http.MethodPost, "/api/veridian/queue.recompute", jsonBody(t, map[string]interface{}{
			"workspace_id": "ws1", "automation_id": "a1", "node_id": "j0a", "reason": "window_closed",
			"profile_id": "p1", "limit": 200, "entry_ids": []string{"e1", " e2 ", ""},
		}))
		rec := httptest.NewRecorder()
		h.handleRecompute(rec, r)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, domain.VeridianQueueRecomputeRequest{
			WorkspaceID: "ws1", AutomationID: "a1", NodeID: "j0a", Reason: "window_closed", ProfileID: "p1",
			EntryIDs: []string{"e1", "e2"}, Limit: 200,
		}, svc.gotRecompute)
		var out map[string]int
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, 12, out["recomputed"])
	})

	t.Run("entry_ids en chaine separee par des virgules (query)", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		rec := httptest.NewRecorder()
		h.handleRecompute(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/queue.recompute?workspace_id=ws1&limit=3&entry_ids=e1,e2", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, []string{"e1", "e2"}, svc.gotRecompute.EntryIDs)
	})

	t.Run("limite non numerique -> 400 sans appeler le service", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		rec := httptest.NewRecorder()
		h.handleRecompute(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/queue.recompute?workspace_id=ws1&automation_id=a1&limit=beaucoup", nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, 0, svc.recomputeCalls)
	})

	t.Run("corps JSON invalide -> 400", func(t *testing.T) {
		h, svc := newQueueExplainHandler(t)
		rec := httptest.NewRecorder()
		h.handleRecompute(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/queue.recompute", bytes.NewReader([]byte("["))))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, 0, svc.recomputeCalls)
	})

	// Le refus des demandes non bornees vient de la validation du domaine, que le service
	// applique : ici on verifie que le handler traduit CETTE erreur en 400 (et pas en 500).
	t.Run("recalcul non borne (refus du domaine) -> 400 avec le message", func(t *testing.T) {
		for name, body := range map[string]map[string]interface{}{
			"sans filtre":        {"workspace_id": "ws1", "limit": 10},
			"limite hors bornes": {"workspace_id": "ws1", "automation_id": "a1", "limit": 5001},
			"limite absente":     {"workspace_id": "ws1", "automation_id": "a1"},
		} {
			t.Run(name, func(t *testing.T) {
				h, svc := newQueueExplainHandler(t)
				svc.err = (&domain.VeridianQueueRecomputeRequest{WorkspaceID: "ws1", Limit: intOf(body["limit"]), AutomationID: strOf(body["automation_id"])}).Validate()
				require.Error(t, svc.err, "le domaine doit refuser ce cas")
				rec := httptest.NewRecorder()
				h.handleRecompute(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/queue.recompute", jsonBody(t, body)))
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				assert.Equal(t, svc.err.Error(), errorOf(t, rec))
			})
		}
	})

	t.Run("erreurs du service -> bon statut", func(t *testing.T) {
		for name, tc := range map[string]struct {
			err  error
			want int
		}{
			"droit d'ecriture manquant": {domain.NewPermissionError(domain.PermissionResourceAutomations, domain.PermissionTypeWrite, "no"), http.StatusForbidden},
			"panne":                     {errors.New("db down"), http.StatusInternalServerError},
		} {
			t.Run(name, func(t *testing.T) {
				h, svc := newQueueExplainHandler(t)
				svc.err = tc.err
				rec := httptest.NewRecorder()
				h.handleRecompute(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/queue.recompute?workspace_id=ws1&automation_id=a1&limit=5", nil))
				assert.Equal(t, tc.want, rec.Code)
			})
		}
	})
}

func intOf(v interface{}) int {
	if n, ok := v.(int); ok {
		return n
	}
	return 0
}

func strOf(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
