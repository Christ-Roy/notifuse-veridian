package domain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianNormalizeDecisionLogLevel(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"off", VeridianDecisionLogOff},
		{"all", VeridianDecisionLogAll},
		{"transitions", VeridianDecisionLogTransitions},
		{"  OFF ", VeridianDecisionLogOff},
		{"All", VeridianDecisionLogAll},
		{"", VeridianDecisionLogTransitions},
		{"verbose", VeridianDecisionLogTransitions},
		{"offf", VeridianDecisionLogTransitions},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, VeridianNormalizeDecisionLogLevel(tt.in))
		})
	}
}

func TestVeridianValidOutcome(t *testing.T) {
	for _, o := range []string{
		VeridianOutcomeSent, VeridianOutcomeDeferred, VeridianOutcomeFailed,
		VeridianOutcomeDiscarded, VeridianOutcomeExited, VeridianOutcomeRecomputed,
	} {
		assert.True(t, VeridianValidOutcome(o), o)
	}
	for _, o := range []string{"", "SENT", "queued", "unknown", " sent"} {
		assert.False(t, VeridianValidOutcome(o), o)
	}
}

// Les codes sont un contrat stable (stockés en base, lus par le CLI et la console).
func TestVeridianReasonCodes_StableValues(t *testing.T) {
	want := map[string]string{
		VeridianReasonNotExamined:      "not_examined",
		VeridianReasonWindowClosed:     "window_closed",
		VeridianReasonCapacity:         "capacity",
		VeridianReasonClassRate:        "class_rate",
		VeridianReasonReputationStop:   "reputation_stopped",
		VeridianReasonExcludedClass:    "excluded_class",
		VeridianReasonProfilePaused:    "profile_paused",
		VeridianReasonNoProfile:        "no_profile_in_pool",
		VeridianReasonCircuitOpen:      "circuit_open",
		VeridianReasonAnchorWait:       "anchor_wait",
		VeridianReasonQuotaDenied:      "quota_denied",
		VeridianReasonRenderFailed:     "render_failed",
		VeridianReasonGuardRetry:       "guard_retry",
		VeridianReasonAutomationPaused: "automation_paused",
		VeridianReasonSendError:        "send_error",
		VeridianReasonDeferredLegacy:   "deferred_legacy",
		VeridianReasonInFlight:         "in_flight",
		VeridianReasonOrphanParked:     "orphan_parked",
	}
	for got, exp := range want {
		assert.Equal(t, exp, got)
	}
}

func TestVeridianQueueReasons_UniqueAndComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range VeridianQueueReasons {
		assert.NotEmpty(t, r)
		assert.False(t, seen[r], "doublon %s", r)
		seen[r] = true
	}
	// L'orphelin n'est pas une raison d'attente d'une entrée de file (pas d'entrée).
	assert.False(t, seen[VeridianReasonOrphanParked])
	// Tous les autres codes y figurent.
	for _, r := range []string{
		VeridianReasonNotExamined, VeridianReasonWindowClosed, VeridianReasonCapacity,
		VeridianReasonClassRate, VeridianReasonReputationStop, VeridianReasonExcludedClass,
		VeridianReasonProfilePaused, VeridianReasonNoProfile, VeridianReasonCircuitOpen,
		VeridianReasonAnchorWait, VeridianReasonQuotaDenied, VeridianReasonRenderFailed,
		VeridianReasonGuardRetry, VeridianReasonAutomationPaused, VeridianReasonSendError,
		VeridianReasonDeferredLegacy, VeridianReasonInFlight,
	} {
		assert.True(t, seen[r], "manque %s", r)
	}
	assert.Len(t, VeridianQueueReasons, 17)
}

func TestVeridianQueueGroupBy_Dimensions(t *testing.T) {
	assert.Equal(t, []string{"automation", "node", "reason", "profile", "class"}, VeridianQueueGroupBy)
}

func TestVeridianQueueRecomputeRequest_Validate(t *testing.T) {
	ok := func() VeridianQueueRecomputeRequest {
		return VeridianQueueRecomputeRequest{WorkspaceID: "ws", AutomationID: "auto", Limit: 100}
	}
	tests := []struct {
		name    string
		mutate  func(r *VeridianQueueRecomputeRequest)
		wantErr string
	}{
		{"valid automation", func(r *VeridianQueueRecomputeRequest) {}, ""},
		{"valid entry_ids only", func(r *VeridianQueueRecomputeRequest) { r.AutomationID = ""; r.EntryIDs = []string{"e1"} }, ""},
		{"limit lower bound", func(r *VeridianQueueRecomputeRequest) { r.Limit = 1 }, ""},
		{"limit upper bound", func(r *VeridianQueueRecomputeRequest) { r.Limit = VeridianQueueRecomputeMaxLimit }, ""},
		{"missing workspace", func(r *VeridianQueueRecomputeRequest) { r.WorkspaceID = "" }, "workspace_id"},
		{"blank workspace", func(r *VeridianQueueRecomputeRequest) { r.WorkspaceID = "  " }, "workspace_id"},
		{"no scope", func(r *VeridianQueueRecomputeRequest) { r.AutomationID = "" }, "never the whole workspace"},
		{"blank automation no entries", func(r *VeridianQueueRecomputeRequest) { r.AutomationID = " " }, "never the whole workspace"},
		{"reason alone is not a scope", func(r *VeridianQueueRecomputeRequest) { r.AutomationID = ""; r.Reason = VeridianReasonCapacity }, "never the whole workspace"},
		{"limit zero", func(r *VeridianQueueRecomputeRequest) { r.Limit = 0 }, "limit"},
		{"limit negative", func(r *VeridianQueueRecomputeRequest) { r.Limit = -3 }, "limit"},
		{"limit too high", func(r *VeridianQueueRecomputeRequest) { r.Limit = VeridianQueueRecomputeMaxLimit + 1 }, "limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ok()
			tt.mutate(&r)
			err := r.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			var typed ErrVeridianQueueRecompute
			assert.ErrorAs(t, err, &typed, "doit être l'erreur de validation typée (400)")
		})
	}
}

func TestVeridianSendTrace_JSONContract(t *testing.T) {
	until := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	tr := VeridianSendTrace{
		Level:  "full",
		Anchor: &VeridianTraceAnchor{Profile: "p1", Available: false},
		Candidates: []VeridianCandidateTrace{{
			Profile: "p1", Outcome: "blocked",
			Gates: []VeridianGateRecord{{Gate: VeridianGateDailyCap, Verdict: VeridianVerdictBlock, Value: 50, Limit: 50}},
		}},
		Decision: VeridianTraceDecision{Outcome: VeridianOutcomeDeferred, Reason: VeridianReasonCapacity, Until: &until},
	}
	b, err := json.Marshal(tr)
	require.NoError(t, err)
	s := string(b)
	// omitempty : pas de bruit pour les champs vides.
	assert.NotContains(t, s, `"class"`)
	assert.NotContains(t, s, `"delay_s"`)
	assert.NotContains(t, s, `"detail"`)
	// Une porte à valeur 0 sans limite ne sérialise pas "limit".
	gb, err := json.Marshal(VeridianGateRecord{Gate: VeridianGateWindow, Verdict: VeridianVerdictPass})
	require.NoError(t, err)
	assert.JSONEq(t, `{"gate":"window","verdict":"pass"}`, string(gb))

	var back VeridianSendTrace
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, VeridianReasonCapacity, back.Decision.Reason)
	require.NotNil(t, back.Decision.Until)
	assert.True(t, until.Equal(*back.Decision.Until))
	require.NotNil(t, back.Anchor)
	assert.False(t, back.Anchor.Available)
	require.Len(t, back.Candidates, 1)
	assert.EqualValues(t, 50, back.Candidates[0].Gates[0].Limit)
}

func TestVeridianSendTrace_EmptyCandidatesIsArrayNotNull(t *testing.T) {
	// Le consommateur (console/CLI) itère : un nil slice ne doit pas devenir null
	// quand l'appelant initialise correctement ; et "gates"/"candidates" sont toujours émis.
	b, err := json.Marshal(VeridianSendTrace{Level: "reduced", Candidates: []VeridianCandidateTrace{}})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"candidates":[]`)
	assert.Contains(t, string(b), `"decision":{"outcome":""}`)
}

func TestVeridianSendDecision_JSONOmitsEmptyOptionals(t *testing.T) {
	d := VeridianSendDecision{ID: "d1", EntryID: "e1", ContactEmail: "a@b.c", Outcome: VeridianOutcomeSent, At: time.Unix(0, 0).UTC()}
	b, err := json.Marshal(d)
	require.NoError(t, err)
	s := string(b)
	for _, k := range []string{`"trace"`, `"until"`, `"reason"`, `"profile_id"`, `"automation_id"`, `"message_id"`} {
		assert.NotContains(t, s, k)
	}
	assert.Contains(t, s, `"sampled":false`)
	assert.Contains(t, s, `"contact_email":"a@b.c"`)
}

func TestVeridianQueueExplain_JSONNullsAndArrays(t *testing.T) {
	// Les champs sans omitempty sont toujours présents (contrat de l'API queue.explain).
	b, err := json.Marshal(VeridianQueueGroup{Reason: VeridianReasonWindowClosed, Count: 3})
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))
	for _, k := range []string{"automation_id", "node_id", "reason", "count", "never_examined",
		"oldest_created_at", "next_attempt_min", "next_attempt_max", "sample_entry_ids"} {
		assert.Contains(t, m, k)
	}
	assert.Nil(t, m["oldest_created_at"])
	assert.EqualValues(t, 3, m["count"])

	eb, err := json.Marshal(VeridianQueueEntryDetail{ID: "e"})
	require.NoError(t, err)
	assert.NotContains(t, string(eb), "last_error")
	assert.Contains(t, string(eb), `"last_decision":null`)
}

// fakeExplainRepo vérifie que les interfaces exportées restent satisfaisables.
type fakeDecisionRepo struct{ inserted []*VeridianSendDecision }

func (f *fakeDecisionRepo) Insert(_ context.Context, _ string, d *VeridianSendDecision) error {
	f.inserted = append(f.inserted, d)
	return nil
}
func (f *fakeDecisionRepo) List(_ context.Context, _ string, flt VeridianSendDecisionFilter) ([]*VeridianSendDecision, string, error) {
	return f.inserted, strings.Repeat("c", len(flt.Cursor)), nil
}

type fakeExplainRepo struct{}

func (fakeExplainRepo) Explain(context.Context, string, VeridianQueueExplainFilter) (*VeridianQueueExplain, error) {
	return &VeridianQueueExplain{}, nil
}
func (fakeExplainRepo) EntryDetail(context.Context, string, string) (*VeridianQueueEntryDetail, error) {
	return nil, nil
}
func (fakeExplainRepo) Recompute(context.Context, string, VeridianQueueRecomputeRequest) ([]string, error) {
	return nil, nil
}

type fakeExplainService struct{}

func (fakeExplainService) Explain(context.Context, string, VeridianQueueExplainFilter) (*VeridianQueueExplain, error) {
	return nil, nil
}
func (fakeExplainService) Decisions(context.Context, string, VeridianSendDecisionFilter) ([]*VeridianSendDecision, string, string, error) {
	return nil, "", "", nil
}
func (fakeExplainService) Recompute(context.Context, VeridianQueueRecomputeRequest) (int, error) {
	return 0, nil
}

func TestVeridianDecisionInterfaces_Satisfiable(t *testing.T) {
	var repo VeridianSendDecisionRepository = &fakeDecisionRepo{}
	require.NoError(t, repo.Insert(context.Background(), "ws", &VeridianSendDecision{ID: "x"}))
	got, next, err := repo.List(context.Background(), "ws", VeridianSendDecisionFilter{Cursor: "ab"})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "cc", next)

	var _ VeridianQueueExplainRepository = fakeExplainRepo{}
	var _ VeridianQueueExplainService = fakeExplainService{}
}
