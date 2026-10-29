package policy

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
)

// Test contre un vrai PostgreSQL : VERIDIAN_POLICY_TEST_DSN="postgres://user:pass@host:5432/db?sslmode=disable".
// Absent : ignoré (la CI Go de ce paquet reste sans base). Il joue la migration V66 telle qu'enregistrée, puis les
// requêtes du dépôt, dont la réécriture des mails en file à un changement de priorité.
func pgDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("VERIDIAN_POLICY_TEST_DSN")
	if dsn == "" {
		t.Skip("VERIDIAN_POLICY_TEST_DSN non posé")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())
	ctx := context.Background()
	for _, s := range []string{
		`DROP TABLE IF EXISTS veridian_policy_override, veridian_send_priority, veridian_policy_settings, email_queue, broadcasts, automations CASCADE`,
		`CREATE TABLE email_queue (id VARCHAR(36) PRIMARY KEY, status VARCHAR(20) NOT NULL DEFAULT 'pending', priority INTEGER NOT NULL DEFAULT 5,
			source_type VARCHAR(20) NOT NULL, source_id VARCHAR(36) NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE broadcasts (id VARCHAR(255) PRIMARY KEY, name VARCHAR(255) NOT NULL, status VARCHAR(20) NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE automations (id VARCHAR(36) PRIMARY KEY, name VARCHAR(255) NOT NULL, status VARCHAR(20) DEFAULT 'draft', created_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`,
	} {
		_, err := db.ExecContext(ctx, s)
		require.NoError(t, err, s)
	}
	// La migration V66 EXACTEMENT telle qu'enregistrée, jouée deux fois (idempotence).
	var stmts []string
	for _, sp := range veridianmig.Default.List() {
		if sp.Number == 66 {
			stmts = sp.Workspace
		}
	}
	require.NotEmpty(t, stmts, "V66 enregistrée")
	for i := 0; i < 2; i++ {
		for _, s := range stmts {
			_, err := db.ExecContext(ctx, s)
			require.NoError(t, err, "V66 passe %d", i+1)
		}
	}
	return db
}

func TestPG_PriorityRequeueTouchePendingEtPauseSeulement(t *testing.T) {
	db := pgDB(t)
	ctx := context.Background()
	st := NewStoreWithDB(db)
	_, err := db.ExecContext(ctx, `INSERT INTO broadcasts (id, name, status) VALUES ('b1','zz-sprint-camp','draft'),('b2','autre','draft')`)
	require.NoError(t, err)
	rows := []struct {
		id, status, src string
		prio            int
	}{
		{"e1", "pending", "b1", 5}, {"e2", "paused", "b1", 5}, {"e3", "pending", "b1", 1}, // transactionnel : jamais touché
		{"e4", "sent", "b1", 5}, {"e5", "processing", "b1", 5}, {"e6", "failed", "b1", 5}, // pas en attente : jamais touchés
		{"e7", "pending", "b2", 5}, // autre source : jamais touchée
	}
	for _, r := range rows {
		_, err := db.ExecContext(ctx, `INSERT INTO email_queue (id, status, priority, source_type, source_id) VALUES ($1,$2,$3,'broadcast',$4)`, r.id, r.status, r.prio, r.src)
		require.NoError(t, err)
	}
	prio := func(id string) int {
		var p int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT priority FROM email_queue WHERE id=$1`, id).Scan(&p))
		return p
	}

	n, err := st.SetPriority(ctx, "ws", "broadcast", "b1", 2, "agent")
	require.NoError(t, err)
	assert.EqualValues(t, 2, n, "pending + paused de la source")
	assert.Equal(t, 2, prio("e1"))
	assert.Equal(t, 2, prio("e2"), "en pause : repart avec la nouvelle priorité")
	assert.Equal(t, 1, prio("e3"), "le transactionnel de séquence ne bouge jamais")
	assert.Equal(t, 5, prio("e4"), "déjà parti")
	assert.Equal(t, 5, prio("e5"), "en cours d'envoi")
	assert.Equal(t, 5, prio("e6"))
	assert.Equal(t, 5, prio("e7"), "autre campagne")

	// Les priorités se lisent, la liste joint la file et les noms.
	srcs, err := st.ListSources(ctx, "ws")
	require.NoError(t, err)
	byID := map[string]PrioritySource{}
	for _, s := range srcs {
		byID[s.SourceID] = s
	}
	assert.Equal(t, 2, byID["b1"].Priority)
	assert.True(t, byID["b1"].Explicit)
	assert.Equal(t, 3, byID["b1"].Pending, "e1 et e3 pending, e2 paused : trois mails attendent")
	assert.Equal(t, 5, byID["b2"].Priority)

	// Une deuxième pose change la valeur sans doublon ; le retrait remet 5 sur ce qui attend.
	n, err = st.SetPriority(ctx, "ws", "broadcast", "b1", 9, "agent")
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
	n, err = st.ClearPriority(ctx, "ws", "broadcast", "b1")
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
	assert.Equal(t, 5, prio("e1"))
	assert.Equal(t, 1, prio("e3"))
	m, err := st.ListPriorities(ctx, "ws")
	require.NoError(t, err)
	assert.Empty(t, m)

	// La contrainte de la table refuse 1 et 10 même si le code était contourné.
	_, err = db.ExecContext(ctx, `INSERT INTO veridian_send_priority (source_type, source_id, priority) VALUES ('broadcast','x',1)`)
	assert.Error(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO veridian_send_priority (source_type, source_id, priority) VALUES ('broadcast','x',10)`)
	assert.Error(t, err)
}

func TestPG_SurchargesUpsertPartielEtRetrait(t *testing.T) {
	db := pgDB(t)
	ctx := context.Background()
	st := NewStoreWithDB(db)
	require.NoError(t, st.UpsertOverride(ctx, "ws", Override{Class: "ovh", DailyCap: 7, Reason: "rebonds", UpdatedBy: "agent"}))
	require.NoError(t, st.UpsertOverride(ctx, "ws", Override{Class: "ovh", SlowdownThreshold: 0.05, UpdatedBy: "agent2"}))
	ov, err := st.ListOverrides(ctx, "ws")
	require.NoError(t, err)
	require.Len(t, ov, 1)
	assert.Equal(t, 7, ov[0].DailyCap, "la première règle est conservée")
	assert.InDelta(t, 0.05, ov[0].SlowdownThreshold, 1e-9)
	assert.Equal(t, "rebonds", ov[0].Reason, "la raison est conservée si la nouvelle est vide")
	assert.Equal(t, "agent2", ov[0].UpdatedBy)

	ok, err := st.ClearOverride(ctx, "ws", "ovh", []string{RuleDailyCap})
	require.NoError(t, err)
	assert.True(t, ok)
	ov, _ = st.ListOverrides(ctx, "ws")
	require.Len(t, ov, 1)
	assert.Zero(t, ov[0].DailyCap)
	_, err = st.ClearOverride(ctx, "ws", "ovh", nil)
	require.NoError(t, err)
	ov, _ = st.ListOverrides(ctx, "ws")
	assert.Empty(t, ov, "plus aucune valeur : la ligne disparaît")

	// Bornes de la table : une valeur hors plage est refusée par la base elle-même.
	_, err = db.ExecContext(ctx, `INSERT INTO veridian_policy_override (class, daily_cap) VALUES ('ovh', 5000)`)
	assert.Error(t, err)

	// Réglages JSON.
	require.NoError(t, st.SetSetting(ctx, "ws", "stock", []byte(`{"a":1}`), "agent"))
	require.NoError(t, st.SetSetting(ctx, "ws", "stock", []byte(`{"a":2}`), "agent"))
	raw, err := st.GetSetting(ctx, "ws", "stock")
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":2}`, string(raw))
}
