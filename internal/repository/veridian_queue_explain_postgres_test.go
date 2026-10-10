package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/domain"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ces tests lisent et ecrivent un VRAI PostgreSQL (le SQL de l'explorateur ne se prouve
// pas avec sqlmock). Lancer avec VERIDIAN_TEST_POSTGRES_DSN pointant sur un serveur
// jetable (une base neuve est creee et detruite par test), p. ex.
// postgres://postgres:...@host:5432/postgres?sslmode=disable. Sans la variable : skip.

func lot1TestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("VERIDIAN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set VERIDIAN_TEST_POSTGRES_DSN to a disposable PostgreSQL server")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("lot1_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })

	u := strings.Replace(dsn, "/postgres?", "/"+name+"?", 1)
	db, err := sql.Open("postgres", u)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, database.InitializeWorkspaceDatabase(db))
	return db
}

type lot1Seed struct{ db *sql.DB }

func (s lot1Seed) automation(t *testing.T, id, name string) {
	_, err := s.db.Exec(`INSERT INTO automations (id, workspace_id, name, status, trigger_config) VALUES ($1,'ws',$2,'live','{}')`, id, name)
	require.NoError(t, err)
}

func (s lot1Seed) contactAutomation(t *testing.T, id, automation, email, node, status string, enteredAgo time.Duration) {
	_, err := s.db.Exec(`INSERT INTO contact_automations (id, automation_id, contact_email, current_node_id, status, entered_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, automation, email, node, status, time.Now().Add(-enteredAgo))
	require.NoError(t, err)
}

type lot1Queue struct {
	id, source, sourceID, email, status, integration string
	nodeID, reason, detail, profile                  string
	nextRetry                                        *time.Time
	examined                                         bool
	class                                            string
}

func (s lot1Seed) queue(t *testing.T, q lot1Queue) {
	if q.status == "" {
		q.status = "pending"
	}
	if q.source == "" {
		q.source = "automation"
	}
	if q.integration == "" {
		q.integration = "nord"
	}
	payload, _ := json.Marshal(map[string]interface{}{"veridian_provider_class": q.class})
	_, err := s.db.Exec(`
		INSERT INTO email_queue (id, status, source_type, source_id, integration_id, provider_kind, contact_email, message_id, template_id, payload,
		                         next_retry_at, node_id, defer_reason, defer_detail, defer_profile, first_examined_at, defer_count)
		VALUES ($1,$2,$3,$4,$5,'smtp',$6,$7,'tpl',$8,$9,NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),
		        CASE WHEN $14 THEN NOW() END, CASE WHEN $14 THEN 1 ELSE NULL END)`,
		q.id, q.status, q.source, q.sourceID, q.integration, q.email, "msg-"+q.id, payload,
		q.nextRetry, q.nodeID, q.reason, q.detail, q.profile, q.examined)
	require.NoError(t, err)
}

func lot1Future(d time.Duration) *time.Time { t := time.Now().Add(d); return &t }

func seedExplainWorld(t *testing.T, db *sql.DB) {
	s := lot1Seed{db}
	s.automation(t, "autoA", "Sequence A")
	for i, e := range []string{"a1@x.fr", "a2@x.fr", "a3@x.fr", "a4@x.fr", "a5@x.fr"} {
		s.contactAutomation(t, fmt.Sprintf("ca%d", i), "autoA", e, "j0a", "sending", time.Hour)
	}
	// a1 : report documente (fenetre fermee), noeud pose a la mise en file
	s.queue(t, lot1Queue{id: "q1", sourceID: "autoA", email: "a1@x.fr", nodeID: "j0a", reason: "window_closed", detail: "", profile: "nord", nextRetry: lot1Future(20 * time.Hour), examined: true, class: "google"})
	// a2 : jamais examinee, aucun node_id (historique d'avant V62 : repli sur le contact_automation)
	s.queue(t, lot1Queue{id: "q2", sourceID: "autoA", email: "a2@x.fr", class: "google"})
	// a3 : report ancien sans raison
	s.queue(t, lot1Queue{id: "q3", sourceID: "autoA", email: "a3@x.fr", nextRetry: lot1Future(time.Hour), class: "microsoft"})
	// a4 : en pause (automation en pause)
	s.queue(t, lot1Queue{id: "q4", sourceID: "autoA", email: "a4@x.fr", status: "paused", class: "google"})
	// a5 : second report de la meme raison
	s.queue(t, lot1Queue{id: "q5", sourceID: "autoA", email: "a5@x.fr", nodeID: "j0a", reason: "window_closed", profile: "nord", nextRetry: lot1Future(21 * time.Hour), examined: true, class: "google"})
	// broadcast : pas d'automation, pas de noeud
	s.queue(t, lot1Queue{id: "q6", source: "broadcast", sourceID: "bc1", email: "b@x.fr", reason: "capacity", detail: "warmup", profile: "relai", nextRetry: lot1Future(time.Hour), examined: true})
	// orphelin : parque en sending sans entree de file
	s.contactAutomation(t, "caO", "autoA", "orphan@x.fr", "j0b", "sending", time.Hour)
}

func TestVeridianQueueExplainRepository_GroupsByReasonWithRealSQL(t *testing.T) {
	db := lot1TestDB(t)
	seedExplainWorld(t, db)
	repo := NewVeridianQueueExplainRepositoryWithDB(db)
	ctx := context.Background()

	out, err := repo.Explain(ctx, "ws", domain.VeridianQueueExplainFilter{GroupBy: []string{"automation", "node", "reason", "profile"}})
	require.NoError(t, err)
	assert.EqualValues(t, 6, out.Total)

	type key struct{ auto, node, reason string }
	got := map[key]domain.VeridianQueueGroup{}
	for _, g := range out.Groups {
		got[key{g.AutomationID, g.NodeID, g.Reason}] = g
	}
	win := got[key{"autoA", "j0a", "window_closed"}]
	assert.EqualValues(t, 2, win.Count)
	assert.Equal(t, "Sequence A", win.AutomationName)
	assert.Equal(t, "nord", win.ProfileID)
	assert.EqualValues(t, 0, win.NeverExamined)
	require.NotNil(t, win.NextAttemptMin)
	assert.Greater(t, time.Until(*win.NextAttemptMax), 20*time.Hour-time.Minute)
	assert.Len(t, win.SampleEntryIDs, 2)

	never := got[key{"autoA", "j0a", "not_examined"}]
	assert.EqualValues(t, 1, never.Count, "le noeud vient du contact_automation quand l'entree n'en porte pas")
	assert.EqualValues(t, 1, never.NeverExamined)
	assert.EqualValues(t, 1, got[key{"autoA", "j0a", "deferred_legacy"}].Count, "un report sans raison est dit ancien, jamais devine")
	assert.EqualValues(t, 1, got[key{"autoA", "j0a", "automation_paused"}].Count)
	bc := got[key{"", "", "capacity"}]
	assert.EqualValues(t, 1, bc.Count)
	assert.Equal(t, "warmup", bc.ReasonDetail)

	// Orphelins : 5 contacts « sending » dont 1 seul sans aucune entree (les 5 de la file en ont une).
	assert.EqualValues(t, 1, out.Orphans.Count)
	require.Len(t, out.Orphans.ByNode, 1)
	assert.Equal(t, "j0b", out.Orphans.ByNode[0].NodeID)
}

func TestVeridianQueueExplainRepository_GroupByReasonOnlyAndFilters(t *testing.T) {
	db := lot1TestDB(t)
	seedExplainWorld(t, db)
	repo := NewVeridianQueueExplainRepositoryWithDB(db)
	ctx := context.Background()

	out, err := repo.Explain(ctx, "ws", domain.VeridianQueueExplainFilter{GroupBy: []string{"reason"}})
	require.NoError(t, err)
	byReason := map[string]int64{}
	for _, g := range out.Groups {
		byReason[g.Reason] = g.Count
		assert.Empty(t, g.AutomationID)
		assert.Empty(t, g.AutomationName)
	}
	assert.EqualValues(t, 2, byReason["window_closed"])
	assert.EqualValues(t, 1, byReason["capacity"])

	out, err = repo.Explain(ctx, "ws", domain.VeridianQueueExplainFilter{GroupBy: []string{"reason"}, AutomationID: "autoA", Reason: "window_closed"})
	require.NoError(t, err)
	assert.EqualValues(t, 2, out.Total)

	out, err = repo.Explain(ctx, "ws", domain.VeridianQueueExplainFilter{GroupBy: []string{"class"}, Class: "microsoft"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, out.Total)

	out, err = repo.Explain(ctx, "ws", domain.VeridianQueueExplainFilter{GroupBy: []string{"profile"}, ProfileID: "relai"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, out.Total, "le profil du groupe est le profil candidat du report, sinon l'integration de l'entree")
}

func TestVeridianQueueExplainRepository_EntryDetail(t *testing.T) {
	db := lot1TestDB(t)
	seedExplainWorld(t, db)
	repo := NewVeridianQueueExplainRepositoryWithDB(db)
	d, err := repo.EntryDetail(context.Background(), "ws", "q1")
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, "window_closed", d.Reason)
	assert.Equal(t, "Sequence A", d.AutomationName)
	assert.Equal(t, "j0a", d.NodeID)
	assert.Equal(t, "google", d.Class)
	assert.Equal(t, 1, d.DeferCount)
	require.NotNil(t, d.NextRetryAt)
	require.NotNil(t, d.FirstExaminedAt)

	d2, err := repo.EntryDetail(context.Background(), "ws", "q2")
	require.NoError(t, err)
	assert.Equal(t, "not_examined", d2.Reason)
	assert.Equal(t, "j0a", d2.NodeID)

	gone, err := repo.EntryDetail(context.Background(), "ws", "nope")
	require.NoError(t, err)
	assert.Nil(t, gone)
}

func TestVeridianQueueExplainRepository_RecomputeIsBoundedAndSafe(t *testing.T) {
	db := lot1TestDB(t)
	seedExplainWorld(t, db)
	repo := NewVeridianQueueExplainRepositoryWithDB(db)
	ctx := context.Background()

	// Seules les entrees PENDING du filtre sont touchees : la pause (q4) ne l'est jamais.
	ids, err := repo.Recompute(ctx, "ws", domain.VeridianQueueRecomputeRequest{WorkspaceID: "ws", AutomationID: "autoA", Reason: "window_closed", Limit: 1})
	require.NoError(t, err)
	assert.Len(t, ids, 1, "borne par la limite")

	ids, err = repo.Recompute(ctx, "ws", domain.VeridianQueueRecomputeRequest{WorkspaceID: "ws", AutomationID: "autoA", Limit: 100})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"q1", "q2", "q3", "q5"}, ids, "toutes les entrees PENDING de l'automation, jamais la pause ni le broadcast")

	var next sql.NullTime
	var reason sql.NullString
	require.NoError(t, db.QueryRow(`SELECT next_retry_at, defer_reason FROM email_queue WHERE id = 'q5'`).Scan(&next, &reason))
	assert.False(t, next.Valid, "prochaine tentative effacee : le worker la reprend au prochain tick")
	assert.False(t, reason.Valid, "raison perimee effacee")

	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM email_queue WHERE id = 'q4'`).Scan(&status))
	assert.Equal(t, "paused", status, "une entree en pause n'est jamais reveillee")
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM email_queue`).Scan(&n))
	assert.Equal(t, 6, n, "aucune entree supprimee")
	var attempts int
	require.NoError(t, db.QueryRow(`SELECT COALESCE(sum(attempts),0) FROM email_queue`).Scan(&attempts))
	assert.Equal(t, 0, attempts, "aucune tentative consommee")

	// Le broadcast n'est pas dans une automation : un filtre d'automation ne le touche pas.
	require.NoError(t, db.QueryRow(`SELECT defer_reason FROM email_queue WHERE id = 'q6'`).Scan(&reason))
	assert.Equal(t, "capacity", reason.String)

	// Filtre par noeud (repli sur le contact_automation pour une entree sans node_id).
	lot1Seed{db}.queue(t, lot1Queue{id: "q7", sourceID: "autoA", email: "a2@x.fr", reason: "capacity", nextRetry: lot1Future(time.Hour), examined: true})
	ids, err = repo.Recompute(ctx, "ws", domain.VeridianQueueRecomputeRequest{WorkspaceID: "ws", AutomationID: "autoA", NodeID: "j0a", Reason: "capacity", Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"q7"}, ids)
}

func TestVeridianQueueExplainRepository_RecomputeByEntryIDs(t *testing.T) {
	db := lot1TestDB(t)
	seedExplainWorld(t, db)
	repo := NewVeridianQueueExplainRepositoryWithDB(db)
	ids, err := repo.Recompute(context.Background(), "ws", domain.VeridianQueueRecomputeRequest{WorkspaceID: "ws", EntryIDs: []string{"q6", "q3"}, Limit: 10})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"q6", "q3"}, ids)
}

func TestVeridianQueueReasonSQL_ProcessingIsInFlight(t *testing.T) {
	db := lot1TestDB(t)
	lot1Seed{db}.queue(t, lot1Queue{id: "p1", sourceID: "a", email: "p@x.fr", status: "processing"})
	out, err := NewVeridianQueueExplainRepositoryWithDB(db).Explain(context.Background(), "ws", domain.VeridianQueueExplainFilter{GroupBy: []string{"reason"}})
	require.NoError(t, err)
	require.Len(t, out.Groups, 1)
	assert.Equal(t, domain.VeridianReasonInFlight, out.Groups[0].Reason)
}

func TestNewVeridianQueueExplainRepository_UsesWorkspaceConnection(t *testing.T) {
	repo := NewVeridianQueueExplainRepository(nil)
	require.NotNil(t, repo)
}
