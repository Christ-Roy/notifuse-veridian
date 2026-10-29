package policy

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockStore(t *testing.T) (Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewStoreWithDB(db), mock
}

func q(s string) string { return regexp.QuoteMeta(s) }

func TestStore_ListOverrides(t *testing.T) {
	st, mock := newMockStore(t)
	now := time.Now()
	mock.ExpectQuery(q("FROM veridian_policy_override ORDER BY class")).WillReturnRows(
		sqlmock.NewRows([]string{"class", "daily_cap", "rate_per_min", "warmup_share", "slowdown_threshold", "reason", "updated_by", "updated_at"}).
			AddRow("ovh", 7, 0.0, 0.0, 0.05, "rebonds", "agent", now))
	out, err := st.ListOverrides(context.Background(), "ws")
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, Override{Class: "ovh", DailyCap: 7, SlowdownThreshold: 0.05, Reason: "rebonds", UpdatedBy: "agent", UpdatedAt: now}, out[0])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_UpsertOverrideConserveLesValeursNonFournies(t *testing.T) {
	st, mock := newMockStore(t)
	mock.ExpectExec(q("ON CONFLICT (class) DO UPDATE SET")).
		WithArgs("ovh", 7, 0.0, 0.0, 0.0, "", "agent").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.UpsertOverride(context.Background(), "ws", Override{Class: "ovh", DailyCap: 7, UpdatedBy: "agent"}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_ClearOverrideListeBlancheDeColonnes(t *testing.T) {
	st, mock := newMockStore(t)
	// Une règle inconnue (injection) est refusée AVANT toute requête.
	_, err := st.ClearOverride(context.Background(), "ws", "ovh", []string{"daily_cap = NULL; DROP TABLE contacts; --"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "règle inconnue", "refusée par la liste blanche, pas par la base")
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectBegin()
	mock.ExpectExec(q("UPDATE veridian_policy_override SET daily_cap = NULL, updated_at = NOW() WHERE class = $1")).
		WithArgs("ovh").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q("DELETE FROM veridian_policy_override WHERE class = $1")).WithArgs("ovh").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	ok, err := st.ClearOverride(context.Background(), "ws", "ovh", []string{RuleDailyCap})
	require.NoError(t, err)
	assert.True(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_SetPriorityEcritLaLigneEtLesMailsEnAttenteDansUneTransaction(t *testing.T) {
	st, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(q("INSERT INTO veridian_send_priority")).WithArgs("broadcast", "b1", 2, "agent").WillReturnResult(sqlmock.NewResult(0, 1))
	// Les mails de priorité 1 (transactionnel de séquence) ne sont jamais réécrits, ceux en pause oui.
	mock.ExpectExec(q("status IN ('pending','paused') AND priority >= 2 AND priority <> $1")).
		WithArgs(2, "broadcast", "b1").WillReturnResult(sqlmock.NewResult(0, 41))
	mock.ExpectCommit()
	n, err := st.SetPriority(context.Background(), "ws", "broadcast", "b1", 2, "agent")
	require.NoError(t, err)
	assert.EqualValues(t, 41, n)
	require.NoError(t, mock.ExpectationsWereMet())

	// Hors plage : refusé sans toucher la base.
	for _, p := range []int{0, 1, 10} {
		_, err = st.SetPriority(context.Background(), "ws", "broadcast", "b1", p, "agent")
		assert.Error(t, err, p)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_ClearPriorityRemetLeDefautSurLesMailsEnAttente(t *testing.T) {
	st, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(q("DELETE FROM veridian_send_priority WHERE source_type = $1 AND source_id = $2")).WithArgs("automation", "a1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q("UPDATE email_queue SET priority = $1")).WithArgs(5, "automation", "a1").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	n, err := st.ClearPriority(context.Background(), "ws", "automation", "a1")
	require.NoError(t, err)
	assert.EqualValues(t, 3, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_ListSourcesJointPrioritesEtFile(t *testing.T) {
	st, mock := newMockStore(t)
	mock.ExpectQuery(q("FROM veridian_send_priority")).WillReturnRows(
		sqlmock.NewRows([]string{"source_type", "source_id", "priority", "updated_by", "updated_at"}).AddRow("broadcast", "b1", 2, "agent", time.Now()))
	mock.ExpectQuery(q("FROM email_queue WHERE status IN ('pending','paused') GROUP BY 1, 2")).WillReturnRows(
		sqlmock.NewRows([]string{"source_type", "source_id", "count"}).AddRow("broadcast", "b1", 12))
	mock.ExpectQuery(q("FROM broadcasts")).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status"}).AddRow("b1", "zz-sprint-camp", "draft").AddRow("b2", "autre", "sent"))
	mock.ExpectQuery(q("FROM automations WHERE deleted_at IS NULL")).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status"}).AddRow("a1", "seq", "active"))
	out, err := st.ListSources(context.Background(), "ws")
	require.NoError(t, err)
	require.Len(t, out, 3)
	assert.Equal(t, 2, out[0].Priority)
	assert.True(t, out[0].Explicit)
	assert.Equal(t, 12, out[0].Pending)
	assert.Equal(t, 5, out[1].Priority)
	assert.False(t, out[1].Explicit)
	assert.Equal(t, "automation", out[2].SourceType)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_SourceNameEtReglages(t *testing.T) {
	st, mock := newMockStore(t)
	mock.ExpectQuery(q("SELECT name FROM broadcasts WHERE id = $1")).WithArgs("b1").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("camp"))
	n, ok, err := st.SourceName(context.Background(), "ws", "broadcast", "b1")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "camp", n)
	mock.ExpectQuery(q("SELECT name FROM broadcasts WHERE id = $1")).WithArgs("nope").WillReturnRows(sqlmock.NewRows([]string{"name"}))
	_, ok, err = st.SourceName(context.Background(), "ws", "broadcast", "nope")
	require.NoError(t, err)
	assert.False(t, ok)
	_, _, err = st.SourceName(context.Background(), "ws", "segment", "x")
	assert.Error(t, err)

	mock.ExpectQuery(q("FROM veridian_policy_settings WHERE key = $1")).WithArgs("stock").WillReturnRows(sqlmock.NewRows([]string{"value"}))
	raw, err := st.GetSetting(context.Background(), "ws", "stock")
	require.NoError(t, err)
	assert.Nil(t, raw)
	require.NoError(t, mock.ExpectationsWereMet())
}
