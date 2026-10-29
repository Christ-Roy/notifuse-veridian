package policy

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/domain"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/repository"
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
	// Schéma à part : d'autres paquets peuvent utiliser la même base de test en même temps (go test lance les paquets en parallèle).
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	_, err = admin.Exec(`CREATE SCHEMA IF NOT EXISTS l3policy`)
	require.NoError(t, err)
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("postgres", dsn+sep+"search_path=l3policy")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())
	ctx := context.Background()
	for _, s := range []string{
		`DROP TABLE IF EXISTS veridian_policy_override, veridian_send_priority, veridian_policy_settings, email_queue, broadcasts, automations, contacts, contact_lists, message_history, contact_automations CASCADE`,
		`CREATE TABLE contacts (email VARCHAR(255) PRIMARY KEY, custom_string_1 VARCHAR(255), custom_string_2 VARCHAR(255), custom_string_3 VARCHAR(255), custom_string_4 VARCHAR(255), custom_string_5 VARCHAR(255), custom_number_1 NUMERIC)`,
		`CREATE TABLE contact_automations (id VARCHAR(36) PRIMARY KEY, automation_id VARCHAR(36) NOT NULL, contact_email VARCHAR(255) NOT NULL, status VARCHAR(20) DEFAULT 'active', scheduled_at TIMESTAMPTZ)`,
		`CREATE TABLE contact_lists (email VARCHAR(255) NOT NULL, list_id VARCHAR(32) NOT NULL, status VARCHAR(20) NOT NULL, deleted_at TIMESTAMPTZ, PRIMARY KEY (email, list_id))`,
		`CREATE TABLE message_history (id VARCHAR(255) PRIMARY KEY, contact_email VARCHAR(255) NOT NULL, sent_at TIMESTAMPTZ, veridian_message_type VARCHAR(16))`,
		`CREATE TABLE email_queue (id VARCHAR(36) PRIMARY KEY, status VARCHAR(20) NOT NULL DEFAULT 'pending', priority INTEGER NOT NULL DEFAULT 5,
			source_type VARCHAR(20) NOT NULL, source_id VARCHAR(36) NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE broadcasts (id VARCHAR(255) PRIMARY KEY, name VARCHAR(255) NOT NULL, status VARCHAR(20) NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE automations (id VARCHAR(36) PRIMARY KEY, name VARCHAR(255) NOT NULL, status VARCHAR(20) DEFAULT 'draft', list_id VARCHAR(36), created_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`,
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

// Parité avec le script stock-par-classe.py : on rejoue SES règles sur un jeu de contacts aléatoire (graine fixe)
// et on exige les mêmes comptes que la requête SQL, filtres compris (adresse interne, custom_string_1 vide, absent ou
// cart_, étiquette absente = « ? », appartenance supprimée, statut quelconque).
func TestPG_ComptesDeStockIdentiquesALaRegleDuScript(t *testing.T) {
	db := pgDB(t)
	ctx := context.Background()
	st := NewStoreWithDB(db)
	rng := rand.New(rand.NewSource(29102026))
	classes := []string{"google", "ovh", "orange", "free", "", "godaddy", "microsoft"}
	bad := regexp.MustCompile(`(?i)brunon5robert|veridian\.site|animalsd\.fr|zz-test|animal-services-distribution`)
	type ref struct{ total, unsent int }
	want := map[string]map[string]*ref{"prospectionasd": {}, "stockodhmasse": {}}
	ns := func(s string, null bool) sql.NullString { return sql.NullString{String: s, Valid: !null} }
	for i := 0; i < 600; i++ {
		email := fmt.Sprintf("c%d@societe%d.fr", i, i%37)
		switch rng.Intn(14) {
		case 0:
			email = fmt.Sprintf("test%d@brunon5robert.gmail.com", i)
		case 1:
			email = fmt.Sprintf("x%d@VERIDIAN.SITE", i)
		case 2:
			email = fmt.Sprintf("y%d@zz-test.example", i)
		}
		var cs1 sql.NullString
		switch rng.Intn(6) {
		case 0:
			cs1 = ns("", false) // chaîne vide
		case 1:
			cs1 = ns("", true) // NULL
		case 2:
			cs1 = ns(fmt.Sprintf("cart_%d", i), false)
		case 3:
			cs1 = ns(" ", false) // espace : renseigné pour le script (valeur « truthy »)
		default:
			cs1 = ns(fmt.Sprintf("SIRET%d", i), false)
		}
		cls := classes[rng.Intn(len(classes))]
		cs5 := ns(cls, cls == "" && rng.Intn(2) == 0) // classe vide : tantôt '' tantôt NULL
		sent := rng.Intn(3) == 0
		transactional := sent && rng.Intn(4) == 0
		_, err := db.ExecContext(ctx, `INSERT INTO contacts (email, custom_string_1, custom_string_5) VALUES ($1, $2, $3)`, email, cs1, cs5)
		require.NoError(t, err)
		if sent {
			mt := sql.NullString{}
			if transactional {
				mt = ns("transactional", false)
			}
			_, err := db.ExecContext(ctx, `INSERT INTO message_history (id, contact_email, sent_at, veridian_message_type) VALUES ($1, $2, NOW(), $3)`, fmt.Sprintf("m%d", i), email, mt)
			require.NoError(t, err)
		}
		for _, list := range []string{"prospectionasd", "stockodhmasse"} {
			if rng.Intn(2) == 0 {
				continue
			}
			deleted := rng.Intn(6) == 0
			status := []string{"active", "unsubscribed", "pending", "bounced"}[rng.Intn(4)]
			_, err := db.ExecContext(ctx, `INSERT INTO contact_lists (email, list_id, status, deleted_at) VALUES ($1, $2, $3, CASE WHEN $4 THEN NOW() END)`, email, list, status, deleted)
			require.NoError(t, err)
			// La règle du script : adresse interne, custom_string_1 faux (vide ou absent) ou « cart_ », appartenance supprimée.
			if deleted || bad.MatchString(email) || !cs1.Valid || cs1.String == "" || strings.HasPrefix(cs1.String, "cart_") {
				continue
			}
			key := cls
			if key == "" {
				key = "?"
			}
			r := want[list][key]
			if r == nil {
				r = &ref{}
				want[list][key] = r
			}
			r.total++
			if !sent || transactional {
				r.unsent++
			}
		}
	}

	cfg := StockConfig{ExcludeEmailRegex: `brunon5robert|veridian\.site|animalsd\.fr|zz-test|animal-services-distribution`,
		RequireNonEmpty: []string{"custom_string_1"}, ExcludePrefixes: map[string][]string{"custom_string_1": {"cart_"}}}
	got, err := st.CountStock(ctx, "ws", cfg, []string{"prospectionasd", "stockodhmasse"})
	require.NoError(t, err)
	grand := 0
	for list, classesWant := range want {
		for class, w := range classesWant {
			assert.Equal(t, ListCounts{Total: w.total, Unsent: w.unsent}, got[list][class], "%s / %s", list, class)
			grand += w.total
		}
		assert.Len(t, got[list], len(classesWant), "mêmes classes pour %s", list)
	}
	assert.Greater(t, grand, 100, "le jeu d'essai n'est pas vide")
	assert.Contains(t, got["prospectionasd"], "?", "étiquette absente : classe « ? » comme le script")

	// Contrôle négatif : sans les filtres les comptes changent, le test voit la différence.
	loose, err := st.CountStock(ctx, "ws", StockConfig{}, []string{"prospectionasd"})
	require.NoError(t, err)
	sum := func(m map[string]ListCounts) (n int) {
		for _, c := range m {
			n += c.Total
		}
		return
	}
	assert.Greater(t, sum(loose["prospectionasd"]), sum(got["prospectionasd"]), "sans filtre il y a plus de contacts")

	// Listes par défaut : celles des automations actives et seulement elles.
	_, err = db.ExecContext(ctx, `INSERT INTO automations (id, name, status, list_id) VALUES ('a1','seq','active','prospectionasd'), ('a2','vieille','draft','autre'), ('a3','sans liste','active','')`)
	require.NoError(t, err)
	ids, err := st.ActiveAutomationLists(ctx, "ws")
	require.NoError(t, err)
	assert.Equal(t, []string{"prospectionasd"}, ids)
}

func TestPG_SuggestionRelancesEtCandidatsDeLaReserve(t *testing.T) {
	db := pgDB(t)
	ctx := context.Background()
	st := NewStoreWithDB(db)
	mustExec := func(q string, a ...any) {
		_, err := db.ExecContext(ctx, q, a...)
		require.NoError(t, err, q)
	}
	mustExec(`INSERT INTO automations (id, name, status, list_id) VALUES ('seq','Prospection','active','insc'), ('autre','Autre','active','ailleurs'), ('vieille','Vieille','draft','insc')`)
	contact := func(email, cs1, cls, prio string, score any) {
		mustExec(`INSERT INTO contacts (email, custom_string_1, custom_string_2, custom_string_5, custom_number_1) VALUES ($1,$2,NULLIF($3,''),$4,$5)`, email, cs1, prio, cls, score)
	}
	list := func(email, id, status string) {
		mustExec(`INSERT INTO contact_lists (email, list_id, status) VALUES ($1,$2,$3)`, email, id, status)
	}
	sent := func(email string, tx bool) {
		mt := sql.NullString{String: "transactional", Valid: tx}
		mustExec(`INSERT INTO message_history (id, contact_email, sent_at, veridian_message_type) VALUES ($1,$2,NOW(),$3)`, "m-"+email, email, mt)
	}

	// Réserve : 7 contacts google/ovh. Ordre attendu pour google : P1 score 90, P1 score 50, P2 score 99, sans priorité.
	contact("g-p2-99@x.fr", "S1", "google", "P2", 99)
	contact("g-p1-50@x.fr", "S2", "google", "P1", 50)
	contact("g-p1-90@x.fr", "S3", "google", "P1", 90)
	contact("g-none@x.fr", "S4", "google", "", nil)
	contact("g-unsub@x.fr", "S5", "google", "P1", 100)    // désabonné : jamais proposé
	contact("g-contacte@x.fr", "S6", "google", "P1", 100) // déjà contacté : jamais proposé
	contact("g-insc@x.fr", "S7", "google", "P1", 100)     // déjà dans la liste des inscrits : jamais proposé
	contact("g-seq@x.fr", "S8", "google", "P1", 100)      // déjà dans l'automation d'inscription : jamais proposé
	contact("g-cart@x.fr", "cart_9", "google", "P1", 100) // écarté par le filtre
	contact("o-1@x.fr", "S9", "ovh", "P3", 10)
	for _, e := range []string{"g-p2-99@x.fr", "g-p1-50@x.fr", "g-p1-90@x.fr", "g-none@x.fr", "g-unsub@x.fr", "g-contacte@x.fr", "g-insc@x.fr", "g-seq@x.fr", "g-cart@x.fr", "o-1@x.fr"} {
		list(e, "res", "active")
	}
	mustExec(`UPDATE contact_lists SET status='unsubscribed' WHERE email='g-unsub@x.fr'`)
	list("g-insc@x.fr", "insc", "active")
	sent("g-contacte@x.fr", false)
	mustExec(`INSERT INTO contact_automations (id, automation_id, contact_email, status, scheduled_at) VALUES ('ca-x','seq','g-seq@x.fr','active',NOW() + interval '1 day')`)

	cfg := StockConfig{ReserveListID: "res", EnrollAutomationID: "seq", RequireNonEmpty: []string{"custom_string_1"}, ExcludePrefixes: map[string][]string{"custom_string_1": {"cart_"}}}
	got, err := st.ReserveCandidates(ctx, "ws", cfg, []string{"insc"}, 10)
	require.NoError(t, err)
	assert.Equal(t, 4, got["google"].Total)
	assert.Equal(t, []string{"g-p1-90@x.fr", "g-p1-50@x.fr", "g-p2-99@x.fr", "g-none@x.fr"}, got["google"].Emails, "priorité P1 d'abord, puis score décroissant, sans priorité en dernier")
	assert.Equal(t, 1, got["ovh"].Total)
	limited, err := st.ReserveCandidates(ctx, "ws", cfg, []string{"insc"}, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"g-p1-90@x.fr", "g-p1-50@x.fr"}, limited["google"].Emails)
	assert.Equal(t, 4, limited["google"].Total, "le total ne dépend pas de la limite")
	counts, err := st.ReserveCandidates(ctx, "ws", cfg, []string{"insc"}, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, counts["google"].Total)
	assert.Empty(t, counts["google"].Emails, "perClass 0 : comptes seulement")

	// Relances : contacts déjà contactés dont la prochaine étape échoit avant l'horizon, dans une automation active de la définition.
	for _, c := range []struct {
		email, auto, status string
		in                  string
		sentOK              bool
	}{
		{"r1@x.fr", "seq", "active", "1 day", true},     // compte
		{"r2@x.fr", "seq", "active", "20 days", true},   // hors horizon
		{"r3@x.fr", "seq", "paused", "1 day", true},     // pas active
		{"r4@x.fr", "seq", "active", "1 day", false},    // jamais contacté : c'est un premier contact, pas une relance
		{"r5@x.fr", "autre", "active", "1 day", true},   // autre automation, autre liste
		{"r6@x.fr", "vieille", "active", "1 day", true}, // automation en brouillon
	} {
		contact(c.email, "S", "ovh", "", nil)
		mustExec(`INSERT INTO contact_automations (id, automation_id, contact_email, status, scheduled_at) VALUES ($1,$2,$3,$4, NOW() + $5::interval)`, "ca-"+c.email, c.auto, c.email, c.status, c.in)
		if c.sentOK {
			sent(c.email, false)
		}
	}
	f, err := st.FollowupsDue(ctx, "ws", cfg, []string{"insc"}, time.Now().Add(5*24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"ovh": 1}, f, "seul r1 est une relance à venir dans l'horizon")
	f, err = st.FollowupsDue(ctx, "ws", StockConfig{}, nil, time.Now().Add(5*24*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, f, "sans automation ni liste : rien à compter")
}

// Bout en bout contre le VRAI schéma de workspace et la VRAIE requête de dépilage : une campagne de priorité 2 sort avant une
// campagne de priorité 5 même si elle est arrivée après ; changer la priorité d'une campagne dont les mails sont déjà en file
// change l'ordre de sortie ; le transactionnel de séquence (1) passe toujours avant.
func TestPG_PrioriteBoutEnBoutAvecLaVraieFile(t *testing.T) {
	dsn := os.Getenv("VERIDIAN_POLICY_TEST_DSN")
	if dsn == "" {
		t.Skip("VERIDIAN_POLICY_TEST_DSN non posé")
	}
	ctx := context.Background()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = admin.Close() }()
	_, err = admin.ExecContext(ctx, `DROP SCHEMA IF EXISTS l3full CASCADE`)
	require.NoError(t, err)
	_, err = admin.ExecContext(ctx, `CREATE SCHEMA l3full`)
	require.NoError(t, err)
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("postgres", dsn+sep+"search_path=l3full")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, database.InitializeWorkspaceDatabase(db), "schéma de workspace réel")

	var stmts []string
	for _, sp := range veridianmig.Default.List() {
		if sp.Number == 66 {
			stmts = sp.Workspace
		}
	}
	for _, s := range stmts {
		_, err := db.ExecContext(ctx, s)
		require.NoError(t, err)
	}

	st := NewStoreWithDB(db)
	r := NewResolver(st, noopLog{}, time.Minute)
	domain.SetVeridianSendPriorityProvider(r)
	defer domain.SetVeridianSendPriorityProvider(nil)
	queue := repository.NewEmailQueueRepositoryWithDB(db)

	enqueue := func(source, email string, tx bool) {
		prio := domain.VeridianSendPriority("ws", domain.EmailQueueSourceBroadcast, source)
		if tx {
			prio = domain.EmailQueuePriorityTransactional
		}
		require.NoError(t, queue.Enqueue(ctx, "ws", []*domain.EmailQueueEntry{{
			Priority: prio, SourceType: domain.EmailQueueSourceBroadcast, SourceID: source, IntegrationID: "int", ProviderKind: domain.EmailProviderKindSMTP,
			ContactEmail: email, MessageID: "m-" + email, TemplateID: "tpl", Payload: domain.EmailQueuePayload{FromAddress: "a@b.fr"}}}))
		time.Sleep(5 * time.Millisecond) // created_at distincts : B (5) arrive AVANT A
	}
	sources := func() string {
		out, err := queue.FetchPending(ctx, "ws", 100)
		require.NoError(t, err)
		var s []string
		for _, e := range out {
			s = append(s, fmt.Sprintf("%s/%d", e.SourceID, e.Priority))
		}
		return strings.Join(s, " ")
	}

	// B (défaut 5) est en file la première ; A reçoit la priorité 2 AVANT d'enqueue ; un transactionnel arrive en dernier.
	enqueue("campB", "b1@x.fr", false)
	enqueue("campB", "b2@x.fr", false)
	_, err = st.SetPriority(ctx, "ws", "broadcast", "campA", 2, "agent")
	require.NoError(t, err)
	r.Invalidate("ws")
	enqueue("campA", "a1@x.fr", false)
	enqueue("campA", "a2@x.fr", false)
	enqueue("campB", "tx@x.fr", true)
	assert.Equal(t, "campB/1 campA/2 campA/2 campB/5 campB/5", sources(),
		"transactionnel d'abord, puis la campagne de priorité 2 (arrivée après), puis la campagne à 5")

	// Les mails de B sont DÉJÀ en file : la priorité 2 posée sur B les réécrit, A (2) et B (2) passent à tour de rôle.
	n, err := st.SetPriority(ctx, "ws", "broadcast", "campB", 2, "agent")
	require.NoError(t, err)
	assert.EqualValues(t, 2, n, "les deux mails marketing de B, pas le transactionnel")
	got := sources()
	assert.Equal(t, 5, len(strings.Fields(got)), "5 mails")
	assert.True(t, strings.HasPrefix(got, "campB/1 "), "le transactionnel garde la première place : %s", got)
	assert.NotContains(t, got, "/5", "plus aucun mail à 5 : %s", got)

	// A descend à 9 : B (2) repasse devant. Même requête de dépilage, même file.
	_, err = st.SetPriority(ctx, "ws", "broadcast", "campA", 9, "agent")
	require.NoError(t, err)
	assert.Equal(t, "campB/1 campB/2 campB/2 campA/9 campA/9", sources())

	// Retrait : A retrouve 5 et passe encore après B (2), avant rien d'autre.
	_, err = st.ClearPriority(ctx, "ws", "broadcast", "campA")
	require.NoError(t, err)
	assert.Equal(t, "campB/1 campB/2 campB/2 campA/5 campA/5", sources())
}
