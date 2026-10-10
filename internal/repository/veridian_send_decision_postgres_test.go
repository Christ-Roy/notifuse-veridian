package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDecision(id string, at time.Time, email, outcome, reason string) *domain.VeridianSendDecision {
	return &domain.VeridianSendDecision{
		ID: id, At: at, EntryID: "e-" + id, MessageID: "m-" + id, ContactEmail: email,
		AutomationID: "autoA", NodeID: "j0a", Outcome: outcome, Reason: reason, ProfileID: "nord",
		Trace: &domain.VeridianSendTrace{
			Class: "google", Level: "full",
			Candidates: []domain.VeridianCandidateTrace{{
				Profile: "nord", Outcome: "blocked",
				Gates: []domain.VeridianGateRecord{{Gate: "window", Verdict: "block", Value: "sam 12:03", Limit: "lun-ven 08:00-19:00", DelayS: 165600}},
			}},
			Decision: domain.VeridianTraceDecision{Outcome: outcome, Reason: reason},
		},
	}
}

func TestVeridianSendDecisionRepository_RoundTripFiltersAndCursor(t *testing.T) {
	db := lot1TestDB(t)
	repo := NewVeridianSendDecisionRepositoryWithDB(db)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)

	for i := 0; i < 5; i++ {
		d := testDecision(fmt.Sprintf("d%d", i), base.Add(time.Duration(i)*time.Minute), "lead@x.fr", "deferred", "window_closed")
		require.NoError(t, repo.Insert(ctx, "ws", d))
	}
	other := testDecision("z", base.Add(10*time.Minute), "other@x.fr", "sent", "")
	other.AutomationID, other.NodeID = "autoB", "j4"
	require.NoError(t, repo.Insert(ctx, "ws", other))

	list, next, err := repo.List(ctx, "ws", domain.VeridianSendDecisionFilter{Email: "LEAD@x.fr ", Limit: 2, WithTrace: true})
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "d4", list[0].ID, "le plus recent d'abord")
	require.NotNil(t, list[0].Trace)
	assert.Equal(t, "block", list[0].Trace.Candidates[0].Gates[0].Verdict)
	assert.Equal(t, "sam 12:03", list[0].Trace.Candidates[0].Gates[0].Value)
	require.NotEmpty(t, next)

	page2, next2, err := repo.List(ctx, "ws", domain.VeridianSendDecisionFilter{Email: "lead@x.fr", Limit: 2, Cursor: next})
	require.NoError(t, err)
	require.Len(t, page2, 2)
	assert.Equal(t, "d2", page2[0].ID)
	assert.Nil(t, page2[0].Trace, "la trace n'est lue que sur demande")
	page3, next3, err := repo.List(ctx, "ws", domain.VeridianSendDecisionFilter{Email: "lead@x.fr", Limit: 2, Cursor: next2})
	require.NoError(t, err)
	assert.Len(t, page3, 1)
	assert.Empty(t, next3, "fin de liste")

	for name, f := range map[string]domain.VeridianSendDecisionFilter{
		"outcome": {Outcome: "sent"}, "node": {NodeID: "j4"}, "automation": {AutomationID: "autoB"},
		"entry": {EntryID: "e-z"},
	} {
		got, _, err := repo.List(ctx, "ws", f)
		require.NoError(t, err, name)
		require.Len(t, got, 1, name)
		assert.Equal(t, "z", got[0].ID, name)
	}
	byReason, _, err := repo.List(ctx, "ws", domain.VeridianSendDecisionFilter{Reason: "window_closed"})
	require.NoError(t, err)
	assert.Len(t, byReason, 5)
	since := base.Add(150 * time.Second)
	recent, _, err := repo.List(ctx, "ws", domain.VeridianSendDecisionFilter{Since: &since, Email: "lead@x.fr"})
	require.NoError(t, err)
	assert.Len(t, recent, 2, "d3 et d4 seulement")

	_, _, err = repo.List(ctx, "ws", domain.VeridianSendDecisionFilter{Cursor: "%%%"})
	assert.Error(t, err)
}

func TestVeridianSendDecisionRepository_PurgeElagagesThenDeletesThenCaps(t *testing.T) {
	db := lot1TestDB(t)
	r := &veridianSendDecisionRepository{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, r.Insert(ctx, "ws", testDecision("fresh", now, "a@x.fr", "sent", "")))
	require.NoError(t, r.Insert(ctx, "ws", testDecision("old20", now.Add(-20*24*time.Hour), "a@x.fr", "deferred", "capacity")))
	require.NoError(t, r.Insert(ctx, "ws", testDecision("old100", now.Add(-100*24*time.Hour), "a@x.fr", "deferred", "capacity")))

	r.purge(ctx, db)

	var traceFresh, trace20 sql.NullString
	require.NoError(t, db.QueryRow(`SELECT trace::text FROM veridian_send_decisions WHERE id='fresh'`).Scan(&traceFresh))
	require.NoError(t, db.QueryRow(`SELECT trace::text FROM veridian_send_decisions WHERE id='old20'`).Scan(&trace20))
	assert.True(t, traceFresh.Valid, "trace recente conservee")
	assert.False(t, trace20.Valid, "trace de plus de 14 jours elaguee, la ligne-resume reste")
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM veridian_send_decisions WHERE id='old100'`).Scan(&n))
	assert.Equal(t, 0, n, "plus de 90 jours : supprimee")
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM veridian_send_decisions WHERE id='old20'`).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestVeridianSendDecisionRepository_PurgeRunsOpportunistically(t *testing.T) {
	db := lot1TestDB(t)
	r := &veridianSendDecisionRepository{db: db}
	ctx := context.Background()
	require.NoError(t, r.Insert(ctx, "ws", testDecision("old100", time.Now().UTC().Add(-100*24*time.Hour), "a@x.fr", "deferred", "capacity")))
	for i := 0; i < veridianDecisionPurgeEvery-1; i++ {
		d := &domain.VeridianSendDecision{ID: fmt.Sprintf("n%d", i), At: time.Now().UTC(), ContactEmail: "a@x.fr", Outcome: "sent"}
		require.NoError(t, r.Insert(ctx, "ws", d))
	}
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM veridian_send_decisions WHERE id='old100'`).Scan(&n))
	assert.Equal(t, 0, n, "la 1000e insertion declenche la retention, sans tache planifiee")
}

func TestVeridianSendDecisionTrace_NeverCarriesSecrets(t *testing.T) {
	b, err := json.Marshal(testDecision("x", time.Now(), "a@x.fr", "sent", ""))
	require.NoError(t, err)
	for _, forbidden := range []string{"password", "secret", "api_key", "html", "subject", "token"} {
		assert.NotContains(t, strings.ToLower(string(b)), forbidden)
	}
}

func TestVeridianSendDecisionCursor_RoundTripAndRejectsGarbage(t *testing.T) {
	at := time.Date(2026, 10, 10, 12, 0, 0, 123456000, time.UTC)
	c := encodeDecisionCursor(at, "id-1")
	gotAt, gotID, err := decodeDecisionCursor(c)
	require.NoError(t, err)
	assert.True(t, at.Equal(gotAt))
	assert.Equal(t, "id-1", gotID)
	for _, bad := range []string{"", "%%%", "YWJj"} {
		_, _, err := decodeDecisionCursor(bad)
		assert.Error(t, err, bad)
	}
}

func TestVeridianSendDecisionRepository_InsertSQLUsesNullsForEmptyFields(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`INSERT INTO veridian_send_decisions`).
		WithArgs("d1", sqlmock.AnyArg(), nil, nil, "a@x.fr", nil, nil, "sent", nil, nil, nil, nil, false, nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	err = NewVeridianSendDecisionRepositoryWithDB(db).Insert(context.Background(), "ws",
		&domain.VeridianSendDecision{ID: "d1", At: time.Now(), ContactEmail: "a@x.fr", Outcome: "sent"})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewVeridianSendDecisionRepository_Constructor(t *testing.T) {
	assert.NotNil(t, NewVeridianSendDecisionRepository(nil))
}
