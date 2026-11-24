package reservoir

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/domain"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Base d'espace complète (schéma et déclencheurs réels), jetée à la fin.
func workspaceDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := testpg.DSN(t, "VERIDIAN_RESERVOIR_TEST_DSN")
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	name := fmt.Sprintf("reservoir_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + pq.QuoteIdentifier(name))
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP DATABASE " + pq.QuoteIdentifier(name) + " WITH (FORCE)")
		_ = admin.Close()
	})
	require.NoError(t, database.InitializeWorkspaceDatabase(db))
	// Toutes les migrations Veridian par le VRAI moteur (marque d'application comprise), deux fois : le 24/11/2026, V76 passait les
	// tests instruction par instruction et empêchait l'application de démarrer.
	for i := 0; i < 2; i++ {
		_, err := veridianmig.Default.ApplyWorkspace(context.Background(), db, "ws")
		require.NoError(t, err)
	}
	return db
}

type wsRepo struct {
	domain.WorkspaceRepository
	db *sql.DB
	w  *domain.Workspace
}

func (w wsRepo) GetByID(context.Context, string) (*domain.Workspace, error) { return w.w, nil }
func (w wsRepo) GetConnection(context.Context, string) (*sql.DB, error)     { return w.db, nil }

type fakeAuth struct {
	role  string
	perms domain.UserPermissions
	err   error
}

func (f *fakeAuth) AuthenticateUserForWorkspace(ctx context.Context, ws string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
	if f.err != nil {
		return ctx, nil, nil, f.err
	}
	return ctx, &domain.User{ID: "u"}, &domain.UserWorkspace{UserID: "u", WorkspaceID: ws, Role: f.role, Permissions: f.perms}, nil
}

type bench struct {
	t    *testing.T
	db   *sql.DB
	ws   *domain.Workspace
	svc  *Service
	auth *fakeAuth
	mux  *http.ServeMux
}

func newBench(t *testing.T) *bench {
	db := workspaceDB(t)
	auth := &fakeAuth{role: "owner"}
	ws := &domain.Workspace{ID: "ws"}
	repo := wsRepo{db: db, w: ws}
	b := &bench{t: t, db: db, ws: ws, auth: auth, mux: http.NewServeMux(), svc: &Service{Auth: auth, Store: &Store{WS: repo}, Contacts: repository.NewContactRepository(repo), Workspaces: repo, Log: logger.NewLoggerWithLevel("disabled")}}
	(&Handler{Svc: b.svc, RequireAuth: func(h http.Handler) http.Handler { return h }, Log: b.svc.Log}).RegisterRoutes(b.mux)
	for _, l := range []string{"reserve", "sequence", "autre"} {
		b.exec(`INSERT INTO lists (id, name, is_double_optin, is_public, created_at, updated_at) VALUES ($1, $1, false, false, NOW(), NOW())`, l)
	}
	return b
}

func (b *bench) exec(q string, a ...any) {
	b.t.Helper()
	_, err := b.db.Exec(q, a...)
	require.NoError(b.t, err, q)
}

func (b *bench) count(q string, a ...any) int {
	b.t.Helper()
	var n int
	require.NoError(b.t, b.db.QueryRow(q, a...).Scan(&n), q)
	return n
}

func (b *bench) call(method, path string, body any, out any) int {
	b.t.Helper()
	var rd *bytes.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	w := httptest.NewRecorder()
	b.mux.ServeHTTP(w, httptest.NewRequest(method, path, rd))
	if out != nil {
		require.NoError(b.t, json.Unmarshal(w.Body.Bytes(), out), w.Body.String())
	}
	return w.Code
}

// automation pose une séquence active AVEC son vrai déclencheur PostgreSQL.
func (b *bench) automation(id, list string, trigger *domain.TimelineTriggerConfig) {
	b.t.Helper()
	trig, _ := json.Marshal(trigger)
	b.exec(`INSERT INTO automations (id, workspace_id, name, status, list_id, trigger_config, root_node_id) VALUES ($1, 'ws', $1, 'live', NULLIF($2, ''), $3, 'n1')`, id, list, trig)
	repo := repository.NewAutomationRepositoryWithDB(b.db, service.NewAutomationTriggerGenerator(service.NewQueryBuilder()))
	require.NoError(b.t, repo.CreateAutomationTrigger(context.Background(), "ws", &domain.Automation{ID: id, WorkspaceID: "ws", Name: id, Status: domain.AutomationStatusLive, ListID: list, Trigger: trigger, RootNodeID: "n1"}))
}

func onList(l string) *domain.TimelineTriggerConfig {
	return &domain.TimelineTriggerConfig{EventKind: "list.subscribed", ListID: &l, Frequency: domain.TriggerFrequencyOnce}
}

func contacts(n int, f func(i int) string) []json.RawMessage {
	out := make([]json.RawMessage, n)
	for i := range out {
		out[i] = raw(f(i))
	}
	return out
}

func TestIntake_ImportDeMilleRejouableEtDedoublonne(t *testing.T) {
	b := newBench(t)
	body := map[string]any{"workspace_id": "ws", "list_ids": []string{"reserve"}, "contacts": contacts(1000, func(i int) string {
		return fmt.Sprintf(`{"email":"contact@societe%d.fr","siren":"%09d","raison_sociale":"Société %d"}`, i, 100000000+i, i)
	})}
	var res IntakeResult
	body["dry_run"] = true
	require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", body, &res))
	assert.Equal(t, 1000, res.Created)
	assert.Equal(t, 0, b.count(`SELECT count(*) FROM contacts`), "une simulation n'écrit rien")

	body["dry_run"] = false
	res = IntakeResult{}
	require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", body, &res))
	assert.Equal(t, 1000, res.Created)
	assert.Equal(t, 1000, res.Subscribed)
	assert.Equal(t, 1000, b.count(`SELECT count(*) FROM contact_lists WHERE list_id='reserve' AND status='active'`))
	assert.Equal(t, 1, b.count(`SELECT count(*) FROM contacts WHERE custom_json_1->>'siren'='100000007' AND custom_json_1->>'raison_sociale'='Société 7'`))

	res = IntakeResult{}
	require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", body, &res))
	assert.Equal(t, [4]int{0, 0, 1000, 0}, [4]int{res.Created, res.Updated, res.Existing, res.Subscribed}, "rejouer ne double rien")
	assert.Equal(t, 1000, b.count(`SELECT count(*) FROM contacts`))

	// Autre adresse, même SIREN ou même domaine : écartée, avec l'adresse qui l'occupe.
	res = IntakeResult{}
	require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", map[string]any{"workspace_id": "ws", "contacts": []json.RawMessage{
		raw(`{"email":"gerant@gmail.com","siret":"10000000700018"}`), raw(`{"email":"direction@societe9.fr"}`), raw(`{"email":"neuf@gmail.com","website":"https://www.societe10.fr/x"}`), raw(`{"email":"vrai@neuf.fr"}`)}}, &res))
	assert.Equal(t, 1, res.Created)
	assert.Equal(t, map[string]int{ReasonSiren: 1, ReasonDomain: 2}, res.Skipped)
	assert.Equal(t, "contact@societe7.fr", res.Details[0].DuplicateOf)
}

// Rejouer le même lot, entier ou en partie, ne crée personne : le constat d'Atlas en prod (25/11/2026), « 1 créé » au second envoi.
func TestIntake_RejeuSansDoublonDeDomaine(t *testing.T) {
	b := newBench(t)
	lot := []json.RawMessage{raw(`{"email":"accueil@orange.fr","domaine":"www.hotel-exemple.fr"}`), raw(`{"email":"resa@gmail.com","domaine":"resa.hotel-exemple.fr"}`),
		raw(`{"email":"a@orange.fr","domaine":"hotel-a.site-solocal.com"}`), raw(`{"email":"b@orange.fr","domaine":"hotel-b.site-solocal.com"}`),
		raw(`{"email":"c@orange.fr","domaine":"c.fr","website":"https://second.fr/accueil"}`), raw(`{"email":"d@mail.sous.fr"}`)}
	post := func(l []json.RawMessage) IntakeResult {
		var r IntakeResult
		require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", map[string]any{"workspace_id": "ws", "list_ids": []string{"reserve"}, "contacts": l}, &r))
		return r
	}
	r := post(lot)
	assert.Equal(t, 5, r.Created)
	assert.Equal(t, map[string]int{ReasonBatchDomain: 1}, r.Skipped)
	for _, l := range [][]json.RawMessage{lot, {lot[1], lot[0]}, {lot[1]}, {raw(`{"email":"z@gmail.com","site":"second.fr"}`)}, {raw(`{"email":"z@gmail.com","site":"https://www.sous.fr"}`)},
		{raw(`{"email":"z@gmail.com","url":"hotel-b.site-solocal.com/menu"}`)}} {
		r = post(l)
		assert.Equal(t, 0, r.Created, "%s", l)
		assert.Equal(t, 1, r.Skipped[ReasonDomain], "%s", l)
	}
	assert.Equal(t, 5, b.count(`SELECT count(*) FROM contacts`))
}

func TestIntake_ExclutDesinscritsRebondsPlaintes(t *testing.T) {
	b := newBench(t)
	for _, e := range []string{"stop@a.fr", "mort@b.fr", "spam@c.fr", "histo@d.fr", "doux@e.fr"} {
		b.exec(`INSERT INTO contacts (email, created_at, updated_at) VALUES ($1, NOW(), NOW())`, e)
	}
	b.exec(`INSERT INTO contact_lists (email, list_id, status) VALUES ('stop@a.fr','autre','unsubscribed'), ('mort@b.fr','autre','bounced'), ('spam@c.fr','autre','complained')`)
	b.exec(`INSERT INTO message_history (id, contact_email, template_id, template_version, channel, message_data, bounced_at, bounce_type) VALUES ('m1','histo@d.fr','t',1,'email','{}',NOW(),'HardBounce'), ('m2','doux@e.fr','t',1,'email','{}',NOW(),'SoftBounce')`)
	var res IntakeResult
	require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", map[string]any{"workspace_id": "ws", "list_ids": []string{"reserve"}, "existing": "update", "contacts": []json.RawMessage{
		raw(`{"email":"stop@a.fr"}`), raw(`{"email":"mort@b.fr"}`), raw(`{"email":"spam@c.fr"}`), raw(`{"email":"histo@d.fr"}`), raw(`{"email":"doux@e.fr","first_name":"Ok"}`)}}, &res))
	assert.Equal(t, map[string]int{ReasonUnsubscribed: 1, ReasonBounced: 2, ReasonComplained: 1}, res.Skipped)
	assert.Equal(t, 1, res.Updated, "un rebond temporaire ne bloque pas")
	assert.Equal(t, 1, b.count(`SELECT count(*) FROM contact_lists WHERE list_id='reserve'`))
}

func TestIntake_MiseAJourPreserveLaFiche(t *testing.T) {
	b := newBench(t)
	b.exec(`INSERT INTO contacts (email, first_name, custom_json_1, created_at, updated_at) VALUES ('x@y.fr','Ancien','{"note":"gardee"}','2020-01-02T00:00:00Z',NOW())`)
	post := func(mode string) IntakeResult {
		var r IntakeResult
		require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", map[string]any{"workspace_id": "ws", "existing": mode, "list_ids": []string{"reserve"}, "contacts": []json.RawMessage{raw(`{"email":"x@y.fr","last_name":"Neuf","siren":"352522304"}`)}}, &r))
		return r
	}
	assert.Equal(t, 0, post("skip").Subscribed)
	assert.Equal(t, 1, post("").Subscribed, "par défaut : ajoutée à la liste")
	assert.Equal(t, 0, b.count(`SELECT count(*) FROM contacts WHERE last_name IS NOT NULL`), "par défaut : fiche intacte")
	assert.Equal(t, 1, post("update").Updated)
	assert.Equal(t, 1, b.count(`SELECT count(*) FROM contacts WHERE first_name='Ancien' AND last_name='Neuf' AND custom_json_1->>'note'='gardee' AND custom_json_1->>'siren'='352522304' AND created_at='2020-01-02T00:00:00Z'`))
}

func TestIntake_RefuseDeDemarrerUneSequence(t *testing.T) {
	b := newBench(t)
	b.automation("seq", "sequence", onList("sequence"))
	body := map[string]any{"workspace_id": "ws", "list_ids": []string{"sequence"}, "contacts": []json.RawMessage{raw(`{"email":"x@y.fr"}`)}}
	var e map[string]any
	require.Equal(t, 409, b.call("POST", "/api/veridian/contacts.intake", body, &e))
	assert.Equal(t, "would_trigger", e["code"])
	assert.Equal(t, 0, b.count(`SELECT count(*) FROM contacts`), "rien n'est écrit")

	var res IntakeResult
	body["dry_run"] = true
	require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", body, &res))
	require.Len(t, res.Triggers, 1)
	body["dry_run"], body["allow_triggers"] = false, true
	require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", body, &res))
	assert.Equal(t, 1, b.count(`SELECT count(*) FROM contact_automations`))
	body["list_ids"] = []string{"reserve"}
	delete(body, "allow_triggers")
	require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", body, &res), "une liste de réserve n'est pas bloquée")
}

// Un segment se recalcule après coup : un contact ou une liste neuve peut y faire entrer quelqu'un, donc démarrer sa séquence. Une fiche
// complétée démarre « contact mis à jour ». Sans accord, rien n'est écrit (24/11/2026 : seules la liste et la création étaient vues).
func TestIntake_RefuseAussiSegmentEtMiseAJour(t *testing.T) {
	b := newBench(t)
	seq := func(id, kind string) {
		b.exec(`INSERT INTO automations (id, workspace_id, name, status, trigger_config, root_node_id) VALUES ($1,'ws',$1,'live',$2,'n1')`, id, `{"event_kind":"`+kind+`","segment_id":"s"}`)
	}
	post := func(mode, email string) int {
		return b.call("POST", "/api/veridian/contacts.intake", map[string]any{"workspace_id": "ws", "existing": mode, "list_ids": []string{"reserve"}, "contacts": []json.RawMessage{raw(`{"email":"` + email + `"}`)}}, nil)
	}
	b.exec(`INSERT INTO contacts (email, created_at, updated_at) VALUES ('deja@y.fr',NOW(),NOW())`)
	seq("maj", "contact.updated")
	require.Equal(t, 200, post("", "neuf@societe-a.fr"), "ni création ni rattachement ne mettent une fiche à jour")
	b.exec(`INSERT INTO contacts (email, created_at, updated_at) VALUES ('present@societe-c.fr',NOW(),NOW())`)
	require.Equal(t, 200, post("", "present@societe-c.fr"), "rattacher ne met pas à jour")
	require.Equal(t, 409, post("update", "deja@y.fr"))
	b.exec(`DELETE FROM automations`)
	seq("segment", "segment.joined")
	assert.Equal(t, 409, post("", "autre@societe-b.fr"), "création")
	assert.Equal(t, 409, post("", "deja@y.fr"), "rattachement à une liste")
	assert.Equal(t, 0, b.count(`SELECT count(*) FROM contacts WHERE email='autre@societe-b.fr'`))
	assert.Equal(t, 0, b.count(`SELECT count(*) FROM contact_lists WHERE email='deja@y.fr'`))
	b.exec(`UPDATE automations SET status='paused'`)
	assert.Equal(t, 200, post("", "autre@societe-b.fr"))
}

func TestListChoices(t *testing.T) {
	b := newBench(t)
	get := func() map[string]bool {
		var r struct {
			Lists []ListChoice `json:"lists"`
		}
		require.Equal(t, 200, b.call("GET", "/api/veridian/reservoir.lists?workspace_id=ws", "", &r))
		out := map[string]bool{}
		for _, l := range r.Lists {
			assert.NotEmpty(t, l.Name)
			out[l.ID] = l.StartsSequence
		}
		return out
	}
	b.automation("seq", "sequence", onList("sequence"))
	b.exec(`UPDATE lists SET deleted_at=NOW() WHERE id='autre'`)
	assert.Equal(t, map[string]bool{"reserve": false, "sequence": true}, get())
	b.exec(`INSERT INTO automations (id, workspace_id, name, status, trigger_config) VALUES ('cree','ws','cree','live','{"event_kind":"contact.created"}')`)
	assert.Equal(t, map[string]bool{"reserve": true, "sequence": true}, get(), "toute création la démarrerait")
	b.auth.role, b.auth.perms = "member", domain.UserPermissions{domain.PermissionResourceContacts: {Read: true, Write: true}}
	assert.Equal(t, 403, b.call("GET", "/api/veridian/reservoir.lists?workspace_id=ws", "", nil), "lists:read requis")
}

func TestIntake_Refus(t *testing.T) {
	b := newBench(t)
	one := []json.RawMessage{raw(`{"email":"x@y.fr"}`)}
	for name, body := range map[string]any{
		"sans espace":    map[string]any{"contacts": one},
		"vide":           map[string]any{"workspace_id": "ws"},
		"trop":           map[string]any{"workspace_id": "ws", "contacts": contacts(1001, func(i int) string { return `{}` })},
		"liste inconnue": map[string]any{"workspace_id": "ws", "contacts": one, "list_ids": []string{"nulle"}},
		"mode":           map[string]any{"workspace_id": "ws", "contacts": one, "existing": "ecrase"},
		"dedupe":         map[string]any{"workspace_id": "ws", "contacts": one, "dedupe": []string{"nom"}},
		"json":           "pas du json",
	} {
		assert.Equal(t, 400, b.call("POST", "/api/veridian/contacts.intake", body, nil), name)
	}
	assert.Equal(t, 413, b.call("POST", "/api/veridian/contacts.intake", strings.Repeat("x", maxBody+1), nil))
	b.auth.role, b.auth.perms = "member", domain.UserPermissions{domain.PermissionResourceContacts: {Read: true, Write: true}}
	assert.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", map[string]any{"workspace_id": "ws", "contacts": one}, nil))
	assert.Equal(t, 403, b.call("POST", "/api/veridian/contacts.intake", map[string]any{"workspace_id": "ws", "contacts": one, "list_ids": []string{"reserve"}}, nil), "les listes demandent leur droit")
	assert.Equal(t, 403, b.call("POST", "/api/veridian/reservoir.activate", map[string]any{"workspace_id": "ws", "automation_id": "a", "list_id": "reserve"}, nil))
	b.auth.err = errors.New("non")
	assert.NotEqual(t, 200, b.call("GET", "/api/veridian/reservoir.summary?workspace_id=ws", "", nil))
}

func (b *bench) reserve(emails ...string) {
	b.t.Helper()
	for i, e := range emails {
		b.exec(`INSERT INTO contacts (email, custom_number_1, created_at, updated_at) VALUES ($1, $2, NOW(), NOW()) ON CONFLICT DO NOTHING`, e, 100-i)
		b.exec(`INSERT INTO contact_lists (email, list_id, status) VALUES ($1, 'reserve', 'active')`, e)
	}
}

func TestActivate_ParLotsSansLesBloques(t *testing.T) {
	b := newBench(t)
	b.reserve("un@a.fr", "deux@b.fr", "trois@c.fr", "stop@d.fr", "reponse@e.fr", "vu@f.fr", "ailleurs@g.fr", "dedans@h.fr", "fin@i.fr")
	b.automation("seq", "sequence", onList("sequence"))
	b.exec(`INSERT INTO automations (id, workspace_id, name, status, trigger_config) VALUES ('vieille','ws','vieille','live','{"event_kind":"email.opened"}')`)
	b.exec(`INSERT INTO contact_lists (email, list_id, status) VALUES ('stop@d.fr','autre','unsubscribed')`)
	for e, st := range map[string]string{"mort@j.fr": "bounced", "attente@k.fr": "pending"} { // rebond DE CETTE liste : décompté ; à confirmer : hors réservoir
		b.exec(`INSERT INTO contacts (email, created_at, updated_at) VALUES ($1, NOW(), NOW())`, e)
		b.exec(`INSERT INTO contact_lists (email, list_id, status) VALUES ($1, 'reserve', $2)`, e, st)
	}
	b.exec(`INSERT INTO veridian_contact_reply (contact_email, match_type) VALUES ('reponse@e.fr','message_id')`)
	b.exec(`INSERT INTO message_history (id, contact_email, template_id, template_version, channel, message_data, sent_at) VALUES ('m1','vu@f.fr','t',1,'email','{}',NOW())`)
	b.exec(`INSERT INTO contact_automations (id, automation_id, contact_email, status) VALUES ('c1','vieille','ailleurs@g.fr','active'), ('c2','seq','dedans@h.fr','exited')`)

	var sum struct{ Reservoirs []ListState }
	require.Equal(t, 200, b.call("GET", "/api/veridian/reservoir.summary?workspace_id=ws&list_ids=reserve&automation_id=seq", "", &sum))
	require.Len(t, sum.Reservoirs, 1)
	want := Pool{Total: 10, Available: 4, Unsubscribed: 1, Bounced: 1, Replied: 1, Contacted: 1, InSequence: 1, Already: 1}
	assert.Equal(t, want, sum.Reservoirs[0].Pool)

	body := map[string]any{"workspace_id": "ws", "automation_id": "seq", "list_id": "reserve", "limit": 2, "dry_run": true}
	var res ActivateResult
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", body, &res))
	assert.Equal(t, []string{"un@a.fr", "deux@b.fr"}, res.Emails, "meilleur score d'abord")
	assert.Equal(t, 1, b.count(`SELECT count(*) FROM contact_automations WHERE automation_id='seq'`), "simulation")

	body["dry_run"] = false
	res = ActivateResult{}
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", body, &res))
	assert.Equal(t, [3]int{2, 2, 2}, [3]int{res.Selected, res.Enrolled, res.Remaining})
	assert.Equal(t, 2, b.count(`SELECT count(*) FROM contact_automations WHERE automation_id='seq' AND status='active'`), "le déclencheur ET l'inscription directe ne doublent pas")
	assert.Equal(t, 2, b.count(`SELECT count(*) FROM contact_lists WHERE list_id='sequence' AND status='active'`), "la garde d'envoi exige la liste de la séquence")

	body["limit"] = 1000
	res = ActivateResult{}
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", body, &res))
	assert.Equal(t, 2, res.Enrolled)
	assert.Equal(t, 0, res.Remaining)
	res = ActivateResult{}
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", body, &res))
	assert.Equal(t, 0, res.Enrolled, "rejouer n'inscrit plus personne")
	assert.Equal(t, 0, b.count(`SELECT count(*) FROM contact_automations WHERE contact_email IN ('stop@d.fr','reponse@e.fr','vu@f.fr')`))

	// Par adresses, déjà contactés compris ; un désinscrit reste dehors.
	res = ActivateResult{}
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", map[string]any{"workspace_id": "ws", "automation_id": "seq", "emails": []string{"VU@f.fr", "stop@d.fr", "inconnu@z.fr"}, "include_contacted": true}, &res))
	assert.Equal(t, []string{"vu@f.fr"}, res.Emails)
}

func TestActivate_SequenceSansListeEtParSegment(t *testing.T) {
	b := newBench(t)
	b.reserve("un@a.fr", "deux@b.fr")
	b.automation("libre", "", &domain.TimelineTriggerConfig{EventKind: "email.opened", Frequency: domain.TriggerFrequencyEveryTime})
	b.exec(`INSERT INTO segments (id, name, color, tree, timezone, version, status) VALUES ('sg','sg','blue','{}','UTC',2,'active')`)
	b.exec(`INSERT INTO contact_segments (email, segment_id, version, matched_at) VALUES ('un@a.fr','sg',2,NOW()), ('deux@b.fr','sg',1,NOW())`)
	var res ActivateResult
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", map[string]any{"workspace_id": "ws", "automation_id": "libre", "segment_id": "sg"}, &res))
	assert.Equal(t, []string{"un@a.fr"}, res.Emails, "seule la version courante du segment compte")
	assert.Equal(t, 1, b.count(`SELECT count(*) FROM contact_automations`))
}

func TestActivate_Refus(t *testing.T) {
	b := newBench(t)
	b.reserve("un@a.fr")
	b.automation("seq", "sequence", onList("sequence"))
	b.automation("intrus", "", onList("sequence"))
	b.exec(`INSERT INTO automations (id, workspace_id, name, status, trigger_config, root_node_id) VALUES ('pause','ws','pause','paused','{"event_kind":"email.opened"}','n1')`)
	for name, body := range map[string]map[string]any{
		"sans séquence":   {"list_id": "reserve"},
		"sans source":     {"automation_id": "seq"},
		"deux sources":    {"automation_id": "seq", "list_id": "reserve", "segment_id": "x"},
		"inconnue":        {"automation_id": "nulle", "list_id": "reserve"},
		"en pause":        {"automation_id": "pause", "list_id": "reserve"},
		"limite":          {"automation_id": "seq", "list_id": "reserve", "limit": 5000},
		"démarre l'autre": {"automation_id": "seq", "list_id": "reserve"},
	} {
		body["workspace_id"] = "ws"
		assert.Equal(t, 400, b.call("POST", "/api/veridian/reservoir.activate", body, nil), name)
	}
	assert.Equal(t, 0, b.count(`SELECT count(*) FROM contact_automations`))
}

func TestActivate_DeclencheurAChaqueFoisNInscritPasDeuxFois(t *testing.T) {
	b := newBench(t)
	b.reserve("un@a.fr")
	trig := onList("sequence")
	trig.Frequency = domain.TriggerFrequencyEveryTime
	b.automation("seq", "sequence", trig)
	var res ActivateResult
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", map[string]any{"workspace_id": "ws", "automation_id": "seq", "list_id": "reserve"}, &res))
	assert.Equal(t, 1, res.Enrolled)
	assert.Equal(t, 1, b.count(`SELECT count(*) FROM contact_automations`), "la liste démarre la séquence : pas de seconde inscription directe")
}

// Le Hub coupe un programme à 45 s : il doit pouvoir rejouer le même lot sans faire entrer le suivant.
func TestActivate_LotNommeRejouable(t *testing.T) {
	b := newBench(t)
	b.reserve("un@a.fr", "deux@b.fr", "trois@c.fr")
	b.automation("seq", "sequence", onList("sequence"))
	body := map[string]any{"workspace_id": "ws", "automation_id": "seq", "list_id": "reserve", "limit": 2, "batch_id": "lot-1"}
	var first, again, next ActivateResult
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", body, &first))
	assert.Equal(t, [3]any{2, 2, false}, [3]any{first.Selected, first.Enrolled, first.Replayed})
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", body, &again))
	assert.Equal(t, [3]any{2, 2, true}, [3]any{again.Selected, again.Enrolled, again.Replayed})
	assert.ElementsMatch(t, first.Emails, again.Emails)
	assert.Equal(t, 1, again.Remaining)
	assert.Equal(t, 2, b.count(`SELECT count(*) FROM contact_automations`), "rejouer le lot n'inscrit personne de plus")
	body["batch_id"] = "lot-2"
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", body, &next))
	assert.Equal(t, []string{"trois@c.fr"}, next.Emails)
	body["batch_id"] = strings.Repeat("x", 101)
	assert.Equal(t, 400, b.call("POST", "/api/veridian/reservoir.activate", body, nil))
}

// Vingt activations simultanées du même lot puis de lots différents : personne n'entre deux fois.
func TestActivate_SimultanesSansDoublon(t *testing.T) {
	b := newBench(t)
	var emails []string
	for i := 0; i < 30; i++ {
		emails = append(emails, fmt.Sprintf("c%d@s%d.fr", i, i))
	}
	b.reserve(emails...)
	b.automation("seq", "sequence", onList("sequence"))
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		for _, id := range []string{"meme", fmt.Sprintf("lot-%d", i)} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := b.svc.Activate(context.Background(), ActivateRequest{WorkspaceID: "ws", AutomationID: "seq", ListID: "reserve", Limit: 2, BatchID: id})
				assert.NoError(t, err)
			}()
		}
	}
	wg.Wait()
	assert.Equal(t, 22, b.count(`SELECT count(*) FROM contact_automations`), "un lot « meme » et dix lots de deux")
	assert.Equal(t, 22, b.count(`SELECT count(DISTINCT contact_email) FROM contact_automations`))
}

// Rejeu d'un import, y compris sur une liste qui démarre une séquence « à chaque fois » : personne n'entre deux fois, un
// désinscrit de CETTE liste reste désinscrit.
func TestIntake_RejeuNInscritPasDeuxFois(t *testing.T) {
	b := newBench(t)
	trig := onList("sequence")
	trig.Frequency = domain.TriggerFrequencyEveryTime
	b.automation("seq", "sequence", trig)
	body := map[string]any{"workspace_id": "ws", "list_ids": []string{"sequence"}, "allow_triggers": true, "existing": "update", "contacts": []json.RawMessage{raw(`{"email":"a@un.fr"}`), raw(`{"email":"b@deux.fr"}`)}}
	for i := 0; i < 3; i++ {
		require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", body, nil))
	}
	assert.Equal(t, 2, b.count(`SELECT count(*) FROM contact_automations`))
	b.exec(`UPDATE contact_lists SET status='unsubscribed' WHERE email='a@un.fr'`)
	var res IntakeResult
	require.Equal(t, 200, b.call("POST", "/api/veridian/contacts.intake", body, &res))
	assert.Equal(t, 1, res.Skipped[ReasonUnsubscribed])
	assert.Equal(t, 1, b.count(`SELECT count(*) FROM contact_lists WHERE email='a@un.fr' AND status='unsubscribed'`))
	assert.Equal(t, 2, b.count(`SELECT count(*) FROM contact_automations`))
}

// Un modèle transactionnel part par le profil réservé sans aucune porte : une séquence qui en contient ne s'alimente pas en masse.
func TestActivate_RefuseUneSequenceHorsPlafond(t *testing.T) {
	b := newBench(t)
	b.reserve("un@a.fr")
	b.automation("seq", "sequence", onList("sequence"))
	b.exec(`INSERT INTO templates (id, name, version, channel, category, created_at, updated_at) VALUES ('t1','t1',1,'email','marketing',NOW(),NOW()), ('t1','t1',2,'email','transactional',NOW(),NOW())`)
	b.exec(`UPDATE automations SET nodes = '[{"id":"n1","type":"trigger","config":{}},{"id":"e","type":"email","config":{"template_id":"t1"}}]' WHERE id='seq'`)
	act := map[string]any{"workspace_id": "ws", "automation_id": "seq", "list_id": "reserve", "dry_run": true}
	imp := map[string]any{"workspace_id": "ws", "list_ids": []string{"sequence"}, "allow_triggers": true, "contacts": []json.RawMessage{raw(`{"email":"x@y.fr"}`)}}
	require.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", act, nil), "sans profil réservé, le mail suit le chemin commercial")

	b.ws.Integrations = domain.Integrations{{ID: "tx", Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}}}
	b.ws.Settings.TransactionalEmailProviderID = "tx"
	assert.Equal(t, 400, b.call("POST", "/api/veridian/reservoir.activate", act, nil))
	assert.Equal(t, 400, b.call("POST", "/api/veridian/contacts.intake", imp, nil))
	assert.Equal(t, 0, b.count(`SELECT count(*) FROM contacts WHERE email='x@y.fr'`))

	b.exec(`INSERT INTO templates (id, name, version, channel, category, created_at, updated_at) VALUES ('t1','t1',3,'email','marketing',NOW(),NOW())`)
	assert.Equal(t, 200, b.call("POST", "/api/veridian/reservoir.activate", act, nil), "repassé en marketing")
}
