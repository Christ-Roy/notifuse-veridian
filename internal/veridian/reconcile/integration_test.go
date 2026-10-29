package reconcile

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/golang/mock/gomock"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ces tests jouent les VRAIES requêtes sur un vrai PostgreSQL (schéma réduit aux colonnes
// lues, calqué sur la base d'un workspace). Ils se lancent avec
// VERIDIAN_TEST_POSTGRES_DSN pointant sur une base jetable ; sans elle ils sont ignorés.

const itSchema = `
DROP TABLE IF EXISTS veridian_reconcile_finding, veridian_send_decisions, contact_timeline, email_queue, message_history,
  automation_node_executions, contact_automations, automations, contact_lists, lists, contacts, veridian_contact_reply, broadcasts CASCADE;
CREATE TABLE contacts (email VARCHAR(255) PRIMARY KEY, custom_string_1 VARCHAR(255), custom_string_5 VARCHAR(255), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE lists (id VARCHAR(32) PRIMARY KEY, name VARCHAR(255) NOT NULL, deleted_at TIMESTAMPTZ);
CREATE TABLE contact_lists (email VARCHAR(255) NOT NULL, list_id VARCHAR(32) NOT NULL, status VARCHAR(20) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), deleted_at TIMESTAMPTZ, PRIMARY KEY (email, list_id));
CREATE TABLE automations (id VARCHAR(36) PRIMARY KEY, workspace_id VARCHAR(36) NOT NULL DEFAULT 'ws', name VARCHAR(255) NOT NULL, status VARCHAR(20) DEFAULT 'live',
  list_id VARCHAR(36), root_node_id VARCHAR(36), nodes JSONB DEFAULT '[]'::jsonb, stats JSONB DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ);
CREATE TABLE contact_automations (id VARCHAR(36) PRIMARY KEY, automation_id VARCHAR(36) NOT NULL, contact_email VARCHAR(255) NOT NULL,
  current_node_id VARCHAR(36), status VARCHAR(20) DEFAULT 'active', exit_reason VARCHAR(50), entered_at TIMESTAMPTZ DEFAULT NOW(), scheduled_at TIMESTAMPTZ);
CREATE TABLE automation_node_executions (id VARCHAR(36) PRIMARY KEY, contact_automation_id VARCHAR(36) NOT NULL, automation_id VARCHAR(36),
  node_id VARCHAR(36) NOT NULL, node_type VARCHAR(50) NOT NULL, action VARCHAR(20) NOT NULL, entered_at TIMESTAMPTZ DEFAULT NOW());
CREATE TABLE message_history (id VARCHAR(255) PRIMARY KEY, contact_email VARCHAR(255) NOT NULL, automation_id VARCHAR(36), broadcast_id VARCHAR(255),
  template_id VARCHAR(32) NOT NULL, sent_at TIMESTAMPTZ, failed_at TIMESTAMPTZ, transactional_notification_id VARCHAR(32), veridian_message_type VARCHAR(16));
CREATE TABLE email_queue (id VARCHAR(36) PRIMARY KEY, status VARCHAR(20) NOT NULL DEFAULT 'pending', source_type VARCHAR(20) NOT NULL, source_id VARCHAR(36) NOT NULL,
  contact_email VARCHAR(255) NOT NULL, template_id VARCHAR(36) NOT NULL, node_id VARCHAR(36), payload JSONB NOT NULL DEFAULT '{}'::jsonb, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE veridian_contact_reply (contact_email TEXT PRIMARY KEY, replied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), match_type TEXT NOT NULL DEFAULT 'message_id', reply_type TEXT NOT NULL DEFAULT 'human');
CREATE TABLE veridian_send_decisions (id VARCHAR(36) PRIMARY KEY, at TIMESTAMPTZ NOT NULL DEFAULT NOW(), entry_id VARCHAR(36), message_id VARCHAR(100),
  contact_email VARCHAR(255) NOT NULL, automation_id VARCHAR(36), node_id VARCHAR(36), outcome VARCHAR(16) NOT NULL, reason VARCHAR(32), detail VARCHAR(200));
CREATE TABLE contact_timeline (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), email VARCHAR(255) NOT NULL, operation VARCHAR(20) NOT NULL, entity_type VARCHAR(50) NOT NULL,
  kind VARCHAR(50) NOT NULL DEFAULT '', changes JSONB, entity_id VARCHAR(255), created_at TIMESTAMPTZ NOT NULL, db_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE broadcasts (id VARCHAR(255) PRIMARY KEY, name VARCHAR(255) NOT NULL);
`

const itNodes = `[
 {"id":"j0","type":"email","config":{"template_id":"t0"},"next_node_id":"wait"},
 {"id":"wait","type":"delay","config":{"unit":"days","duration":4},"next_node_id":"j4"},
 {"id":"j4","type":"email","config":{"template_id":"t4"}}]`

func itDB(t *testing.T) *sql.DB {
	dsn := os.Getenv("VERIDIAN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("VERIDIAN_TEST_POSTGRES_DSN absente : base jetable requise")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())
	_, err = db.Exec(itSchema)
	require.NoError(t, err)
	for _, st := range SchemaStatements {
		_, err = db.Exec(st)
		require.NoError(t, err, st)
	}
	return db
}

func itExec(t *testing.T, db *sql.DB, q string, args ...any) {
	_, err := db.Exec(q, args...)
	require.NoError(t, err, q)
}

func itEngine(t *testing.T, db *sql.DB) *Engine {
	ctrl := gomock.NewController(t)
	wr := mocks.NewMockWorkspaceRepository(ctrl)
	wr.EXPECT().GetConnection(gomock.Any(), "ws").Return(db, nil).AnyTimes()
	return &Engine{DB: db, Env: Env{WorkspaceID: "ws", DB: db, Stats: repository.NewVeridianProspectionStatsRepository(wr)}}
}

// seed : un workspace où chaque invariant a une donnée corrompue ET une donnée saine.
func itSeed(t *testing.T, db *sql.DB) {
	now := time.Now().UTC()
	old := now.Add(-3 * time.Hour)
	itExec(t, db, `INSERT INTO lists (id, name) VALUES ('prospect', 'Prospection'), ('excl', 'Clients exclus de la prospection')`)
	itExec(t, db, `INSERT INTO automations (id, name, list_id, root_node_id, nodes, stats) VALUES ('seq', 'Séquence test', 'prospect', 'j0', $1::jsonb,
		'{"enrolled": 99, "completed": 0, "exited": 0, "failed": 0}'::jsonb)`, itNodes)
	// Classes : 3 contacts mal classés (invariant 6), 2 sains.
	for _, c := range []struct{ email, tag string }{
		{"a@orange.fr", "freemail_fr"}, {"b@laposte.net", "freemail_fr"}, {"c@free.fr", "freemail_fr"},
		{"d@orange.fr", "orange"}, {"e@gmail.com", "google"},
	} {
		itExec(t, db, `INSERT INTO contacts (email, custom_string_5) VALUES ($1, $2)`, c.email, c.tag)
	}
	enrol := func(id, email, status, node, reason string, execs ...string) {
		var n any
		if node != "" {
			n = node
		}
		var r any
		if reason != "" {
			r = reason
		}
		itExec(t, db, `INSERT INTO contacts (email) VALUES ($1) ON CONFLICT DO NOTHING`, email)
		itExec(t, db, `INSERT INTO contact_lists (email, list_id, status, updated_at) VALUES ($1, 'prospect', 'active', $2) ON CONFLICT (email, list_id) DO NOTHING`, email, old)
		itExec(t, db, `INSERT INTO contact_automations (id, automation_id, contact_email, status, current_node_id, exit_reason) VALUES ($1, 'seq', $2, $3, $4, $5)`, id, email, status, n, r)
		for i, node := range execs {
			itExec(t, db, `INSERT INTO automation_node_executions (id, contact_automation_id, automation_id, node_id, node_type, action) VALUES ($1, $2, 'seq', $3, 'email', 'completed')`, fmt.Sprintf("%s-x%d", id, i), id, node)
		}
	}
	sent := func(id, email, tpl string) {
		itExec(t, db, `INSERT INTO message_history (id, contact_email, automation_id, template_id, sent_at) VALUES ($1, $2, 'seq', $3, $4)`, id, email, tpl, old)
	}
	// Sain : J0 parti, attend J+4.
	enrol("ca-ok", "ok@x.fr", "active", "j4", "", "j0")
	sent("m-ok", "ok@x.fr", "t0")
	// Invariant 3a : a répondu il y a 3 h, reste actif.
	enrol("ca-rep", "rep@x.fr", "active", "j4", "", "j0")
	sent("m-rep", "rep@x.fr", "t0")
	itExec(t, db, `INSERT INTO veridian_contact_reply (contact_email, replied_at, reply_type) VALUES ('rep@x.fr', $1, 'human')`, old)
	// Invariant 3b + file : rebond sur la liste, en cours d'envoi, J+4 en file.
	enrol("ca-bnc", "bnc@x.fr", "sending", "j4", "", "j0", "j4")
	sent("m-bnc", "bnc@x.fr", "t0")
	itExec(t, db, `UPDATE contact_lists SET status = 'bounced', updated_at = $1 WHERE email = 'bnc@x.fr'`, old)
	itExec(t, db, `INSERT INTO email_queue (id, source_type, source_id, contact_email, template_id, node_id) VALUES ('q-bnc', 'automation', 'seq', 'bnc@x.fr', 't4', 'j4')`)
	// Cas du 27/10 : J0 mis en file (terminé) puis désinscrit avant l'envoi : aucun mail parti.
	enrol("ca-uns", "uns@x.fr", "exited", "", "unsubscribed", "j0")
	// Invariant 3d : contact d'une liste d'exclusion, inscrit à la séquence.
	enrol("ca-exc", "exc@x.fr", "active", "j0", "")
	itExec(t, db, `INSERT INTO contact_lists (email, list_id, status, updated_at) VALUES ('exc@x.fr', 'excl', 'active', $1)`, old)
	// Invariant 7a : J0 déjà parti, et de nouveau en file (même modèle).
	enrol("ca-dup", "dup@x.fr", "sending", "j0", "", "j0")
	sent("m-dup", "dup@x.fr", "t0")
	itExec(t, db, `INSERT INTO email_queue (id, source_type, source_id, contact_email, template_id, node_id) VALUES ('q-dup', 'automation', 'seq', 'dup@x.fr', 't0', 'j0')`)
	// Garde 1 : le rebond date d'une minute (délai de grâce) : pas encore jugé.
	enrol("ca-fresh", "fresh@x.fr", "active", "j0", "")
	itExec(t, db, `UPDATE contact_lists SET status = 'bounced', updated_at = $1 WHERE email = 'fresh@x.fr'`, now.Add(-time.Minute))
	// Garde 2 : une séquence qui gère la réponse elle-même (branche réponse) n'est pas jugée sur la réponse.
	itExec(t, db, `INSERT INTO automations (id, name, list_id, root_node_id, nodes, stats) VALUES ('seq-rb', 'Séquence branche réponse', 'prospect', 'j0', $1::jsonb,
		'{"enrolled": 1, "completed": 0, "exited": 0, "failed": 0}'::jsonb)`,
		`[{"id":"j0","type":"email","config":{"template_id":"t0"},"next_node_id":"rb"},{"id":"rb","type":"reply_branch","config":{}}]`)
	itExec(t, db, `INSERT INTO contacts (email) VALUES ('rb@x.fr')`)
	itExec(t, db, `INSERT INTO contact_automations (id, automation_id, contact_email, status, current_node_id) VALUES ('ca-rb', 'seq-rb', 'rb@x.fr', 'active', 'rb')`)
	itExec(t, db, `INSERT INTO veridian_contact_reply (contact_email, replied_at, reply_type) VALUES ('rb@x.fr', $1, 'human')`, old)
	// Stock : 2 personnes jamais contactées dans la liste, hors exclusion.
	for _, e := range []string{"stock1@x.fr", "stock2@x.fr"} {
		itExec(t, db, `INSERT INTO contacts (email) VALUES ($1)`, e)
		itExec(t, db, `INSERT INTO contact_lists (email, list_id, status) VALUES ($1, 'prospect', 'active')`, e)
	}
	// Un client uniquement dans la liste d'exclusion (comme les 771 clients ERP du rapport UX N1).
	itExec(t, db, `INSERT INTO contacts (email) VALUES ('onlyexcl@x.fr')`)
	itExec(t, db, `INSERT INTO contact_lists (email, list_id, status) VALUES ('onlyexcl@x.fr', 'excl', 'active')`)
	// Un client exclu qui est aussi dans la liste de prospection, jamais contacté.
	itExec(t, db, `INSERT INTO contacts (email) VALUES ('client@x.fr')`)
	itExec(t, db, `INSERT INTO contact_lists (email, list_id, status) VALUES ('client@x.fr', 'prospect', 'active'), ('client@x.fr', 'excl', 'active')`)
}

func findingByRef(fs []Finding, ref string) *Finding {
	for i := range fs {
		if fs[i].SubjectRef == ref {
			return &fs[i]
		}
	}
	return nil
}

func TestIntegration_DetectAndRepairEveryInvariant(t *testing.T) {
	db := itDB(t)
	itSeed(t, db)
	ctx := context.Background()
	eng := itEngine(t, db)

	rep, err := eng.Run(ctx, nil)
	require.NoError(t, err)
	status := map[string]CheckStatus{}
	for _, c := range rep.Checks {
		status[c.Check] = c.Status
		assert.NotEqual(t, StatusIncomplet, c.Status, "%s : %s", c.Check, c.Error)
	}
	for _, code := range []string{"statut_coherent", "compteurs", "classes", "stock"} {
		assert.Equal(t, StatusFindings, status[code], code)
	}

	want := []string{
		"classe:freemail_fr->orange", "classe:freemail_fr->laposte", "classe:freemail_fr->free",
		"replied_enrolled:seq", "terminal_enrolled:seq", "queue_suppressed:seq", "excluded_enrolled:seq",
		"stats_stockees:seq", "file_deja_envoye:seq",
		// J0 : 3 comptés partis, 4 contacts ont reçu un mail (le « dup » est reparti en file après un premier envoi).
		"etape_envoyes:seq:1",
	}
	for _, ref := range want {
		assert.NotNil(t, findingByRef(rep.Findings, ref), "écart attendu : %s", ref)
	}
	assert.Nil(t, findingByRef(rep.Findings, "replied_enrolled:seq-rb"), "branche réponse : la séquence traite la réponse elle-même")
	assert.Nil(t, findingByRef(rep.Findings, "terminal_enrolled:seq"+"-fresh"))
	if f := findingByRef(rep.Findings, "terminal_enrolled:seq"); assert.NotNil(t, f) {
		assert.EqualValues(t, 1, f.Evidence["count"], "seul bnc@x.fr : le rebond d'il y a une minute est dans le délai de grâce")
	}
	assert.Nil(t, findingByRef(rep.Findings, "stock:total"), "stock annoncé = stock réel : les listes d'exclusion ne comptent pas")
	for _, f := range rep.Findings {
		if f.ProposedAction != nil {
			assert.Equal(t, map[string]any{"workspace_id": "ws", "finding_id": f.ID, "action": "apply"}, f.ProposedAction.API.Body,
				"l'appel d'API de l'action désigne l'écart : %s", f.SubjectRef)
		}
	}
	etape := findingByRef(rep.Findings, "etape_envoyes:seq:1")
	if assert.NotNil(t, etape) {
		assert.EqualValues(t, -1, etape.Evidence["gap"])
		assert.EqualValues(t, 3, etape.Evidence["served"])
		assert.EqualValues(t, 4, etape.Evidence["base"])
	}

	// Chaque réparation s'applique par le service et l'écart disparaît au rejeu.
	ex := &Executor{DB: db}
	apply := func(ref string) map[string]any {
		f := findingByRef(rep.Findings, ref)
		require.NotNil(t, f, ref)
		require.NotNil(t, f.ProposedAction, ref)
		out, err := ex.Apply(ctx, f)
		require.NoError(t, err, ref)
		return out
	}
	assert.EqualValues(t, 1, apply("classe:freemail_fr->orange")["affected"])
	assert.EqualValues(t, 1, apply("classe:freemail_fr->laposte")["affected"])
	assert.EqualValues(t, 1, apply("classe:freemail_fr->free")["affected"])
	var tag string
	require.NoError(t, db.QueryRow(`SELECT custom_string_5 FROM contacts WHERE email = 'a@orange.fr'`).Scan(&tag))
	assert.Equal(t, "orange", tag)

	assert.EqualValues(t, 1, apply("replied_enrolled:seq")["exited"])
	var st, reason string
	require.NoError(t, db.QueryRow(`SELECT status, exit_reason FROM contact_automations WHERE id = 'ca-rep'`).Scan(&st, &reason))
	assert.Equal(t, "exited", st)
	assert.Equal(t, "replied", reason)

	assert.EqualValues(t, 1, apply("terminal_enrolled:seq")["exited"], "le rebondi sort, son envoi en file part avec lui")
	var inQueue int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM email_queue WHERE id = 'q-bnc'`).Scan(&inQueue))
	assert.Equal(t, 0, inQueue)
	var journal int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_send_decisions WHERE entry_id = 'q-bnc' AND outcome = 'cancelled'`).Scan(&journal))
	assert.Equal(t, 1, journal, "l'annulation est journalisée")

	assert.EqualValues(t, 1, apply("excluded_enrolled:seq")["exited"])
	dup := apply("file_deja_envoye:seq")
	assert.EqualValues(t, 1, dup["cancelled"])
	assert.EqualValues(t, 1, dup["exited"], "le contact parqué sans entrée de file sort : pas d'orphelin")
	apply("stats_stockees:seq")

	// Rejeu : plus aucun de ces écarts (le stock reste : la réparation du stock est dans le code de comptage).
	rep2, err := eng.Run(ctx, nil)
	require.NoError(t, err)
	for _, ref := range want {
		assert.Nil(t, findingByRef(rep2.Findings, ref), "écart disparu après réparation : %s", ref)
	}
	assert.Empty(t, rep2.Findings, "plus aucun écart ouvert après les réparations")
	for _, f := range rep.Findings {
		got, err := GetFinding(ctx, db, f.ID)
		require.NoError(t, err)
		assert.NotNil(t, got.ResolvedAt, "écart clos : %s", f.SubjectRef)
	}
}

func TestIntegration_CleanWorkspaceHasNoFinding(t *testing.T) {
	db := itDB(t)
	itExec(t, db, `INSERT INTO lists (id, name) VALUES ('prospect', 'Prospection')`)
	itExec(t, db, `INSERT INTO automations (id, name, list_id, root_node_id, nodes) VALUES ('seq', 'S', 'prospect', 'j0', $1::jsonb)`, itNodes)
	rep, err := itEngine(t, db).Run(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, rep.Findings)
	for _, c := range rep.Checks {
		assert.Equal(t, StatusOK, c.Status, "%s %s", c.Check, c.Error)
	}
}

func TestIntegration_NoMailSentIsNotCounted_ServedVsBase(t *testing.T) {
	db := itDB(t)
	itSeed(t, db)
	eng := itEngine(t, db)
	served, err := servedStats(context.Background(), eng.Env)
	require.NoError(t, err)
	var seq = served.Sequences[0]
	// Parti à J0 : ok, rep, bnc = 3 ; dup est en file ; uns (désinscrit avant l'envoi, J0 « terminé » mais jamais parti) n'y est pas.
	assert.Equal(t, 3, seq.Stages[0].Sent)
	assert.Equal(t, 1, seq.Stages[0].Queued)
	assert.Equal(t, 1, seq.Exits.BeforeFirstMail, "le désinscrit avant envoi est sorti sans avoir reçu de mail")
	assert.Equal(t, 7, seq.Enrolled)
}

func TestIntegration_SyncLifecycleDismissAndAutoClose(t *testing.T) {
	db := itDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mk := func(ref string) Finding {
		return Finding{SubjectRef: ref, Severity: SeverityWarning, Title: "t", Evidence: map[string]any{"n": 1}, ProposedAction: &Action{Kind: "k", Params: map[string]any{}}}
	}
	out, err := SyncFindings(ctx, db, "classes", []Finding{mk("a"), mk("b")}, now)
	require.NoError(t, err)
	assert.Equal(t, 2, out.Opened)
	// 2e passage : a persiste, b a disparu, c arrive.
	out, err = SyncFindings(ctx, db, "classes", []Finding{mk("a"), mk("c")}, now.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, SyncOutcome{Opened: 1, Refreshed: 1, AutoClosed: 1}, out)
	open, err := ListFindings(ctx, db, ListOptions{})
	require.NoError(t, err)
	assert.Len(t, open, 2)
	// Un autre contrôle n'est jamais touché.
	_, err = SyncFindings(ctx, db, "stock", []Finding{mk("a")}, now)
	require.NoError(t, err)
	open, _ = ListFindings(ctx, db, ListOptions{Check: "classes"})
	assert.Len(t, open, 2)
	// Écarter c : il ne revient pas avant 7 jours, il revient ensuite.
	var cID string
	for _, f := range open {
		if f.SubjectRef == "c" {
			cID = f.ID
		}
	}
	require.NoError(t, CloseFinding(ctx, db, cID, "robert", map[string]any{"how": "dismissed"}, now.Add(2*time.Minute)))
	out, err = SyncFindings(ctx, db, "classes", []Finding{mk("a"), mk("c")}, now.Add(3*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, out.Muted)
	out, err = SyncFindings(ctx, db, "classes", []Finding{mk("a"), mk("c")}, now.Add(DismissQuietPeriod+time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, out.Opened, "après le délai, l'écart toujours constaté revient")
	// Double résolution refusée.
	assert.Error(t, CloseFinding(ctx, db, cID, "robert", nil, now))
	got, err := GetFinding(ctx, db, cID)
	require.NoError(t, err)
	assert.Equal(t, "robert", got.ResolvedBy)
	_, err = GetFinding(ctx, db, "inconnu")
	assert.ErrorIs(t, err, ErrFindingNotFound)
}

// Stock : le chiffre servi compte à tort les personnes d'une liste d'exclusion (N1 du rapport UX).
type inflatedStats struct {
	inner StatsSource
	extra int
}

func (i inflatedStats) GetProspectionRaw(ctx context.Context, ws string, since, until time.Time) (*domain.VeridianProspectionRaw, error) {
	raw, err := i.inner.GetProspectionRaw(ctx, ws, since, until)
	if err != nil {
		return nil, err
	}
	n := *raw.StockDistinct + i.extra
	raw.StockDistinct = &n
	return raw, nil
}

func TestIntegration_StockServedInflatedByExcludedClients(t *testing.T) {
	db := itDB(t)
	itSeed(t, db)
	eng := itEngine(t, db)
	eng.Env.Stats = inflatedStats{inner: eng.Env.Stats, extra: 2}
	rep, err := eng.Run(context.Background(), []string{"stock"})
	require.NoError(t, err)
	f := findingByRef(rep.Findings, "stock:total")
	require.NotNil(t, f)
	assert.EqualValues(t, 3, f.Evidence["base"], "stock1, stock2 et le désinscrit avant envoi (jamais contacté)")
	assert.EqualValues(t, 5, f.Evidence["served"])
	assert.EqualValues(t, 3, f.Evidence["excluded_in_stock"], "exc@x.fr, client@x.fr et onlyexcl@x.fr sont dans la liste d'exclusion")
	assert.Contains(t, f.Explanation, "2 personnes de la différence sont des personnes d'une liste d'exclusion")
}
