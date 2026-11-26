package activite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/activite/activitesql"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	"github.com/Notifuse/notifuse/pkg/logger"
	_ "github.com/lib/pq"
)

const testSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"

// Le schéma de l'upstream utile ici (message_history, abonnements), puis la boîte unifiée et V86.
var upstreamSchema = []string{
	`CREATE TABLE contacts (email VARCHAR(255) PRIMARY KEY)`,
	`CREATE TABLE message_history (
		id VARCHAR(255) PRIMARY KEY, contact_email VARCHAR(255) NOT NULL, broadcast_id VARCHAR(255), automation_id VARCHAR(36),
		transactional_notification_id VARCHAR(32), list_id VARCHAR(32), channel VARCHAR(20) NOT NULL DEFAULT 'email',
		message_data JSONB NOT NULL DEFAULT '{}', channel_options JSONB, sent_at TIMESTAMPTZ, delivered_at TIMESTAMPTZ, opened_at TIMESTAMPTZ,
		clicked_at TIMESTAMPTZ, bounced_at TIMESTAMPTZ, unsubscribed_at TIMESTAMPTZ, bounce_type VARCHAR(100),
		veridian_sender_email VARCHAR(255), veridian_message_type VARCHAR(16), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	`CREATE TABLE webhook_subscriptions (id VARCHAR(32) PRIMARY KEY, name VARCHAR(255) NOT NULL DEFAULT '', url TEXT NOT NULL DEFAULT '',
		secret VARCHAR(64) NOT NULL DEFAULT '', settings JSONB NOT NULL DEFAULT '{}', enabled BOOLEAN DEFAULT true)`,
	`CREATE TABLE webhook_deliveries (id VARCHAR(36) PRIMARY KEY, subscription_id VARCHAR(32), event_type VARCHAR(50), payload JSONB,
		status VARCHAR(20), attempts INT, max_attempts INT, next_attempt_at TIMESTAMPTZ)`,
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	return testpg.System(t, func(db *sql.DB) error {
		all := append(append(append([]string{}, upstreamSchema...), mailbox.AllSchemaStatements()...), activitesql.Statements...)
		for _, s := range all {
			if _, err := db.Exec(s); err != nil {
				return fmt.Errorf("%v : %.80s", err, s)
			}
		}
		return nil
	})
}

func subscribe(t *testing.T, db *sql.DB, url, secret string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO webhook_subscriptions (id, url, secret, settings, enabled) VALUES ($1, $2, $3, '{"event_types":["message.received"]}', true)
		ON CONFLICT (id) DO UPDATE SET url = EXCLUDED.url, secret = EXCLUDED.secret, enabled = true`, activitesql.SubscriptionID, url, secret); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, db *sql.DB, where string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM veridian_activite_outbox WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%v : %s", err, q)
	}
}

func sendMH(t *testing.T, db *sql.DB, id, email string) {
	exec(t, db, `INSERT INTO message_history (id, contact_email, broadcast_id, list_id, sent_at, veridian_sender_email, channel_options, message_data)
		VALUES ($1, $2, 'bc1', 'l1', NOW(), 'hugo@agence.fr', '{"from_name":"Hugo"}', '{"data":{"corps":"CORPS SECRET"}}')`, id, email)
}

// Sans abonnement du Hub, rien n'entre dans la file ; avec, un événement par transition et un seul par message et par événement.
func TestDeclencheurs_UnEvenementParTransitionEtDedoublonnage(t *testing.T) {
	db := testDB(t)
	// Un abonnement du client et celui du Hub éteint ne comptent pas.
	exec(t, db, `INSERT INTO webhook_subscriptions (id, url, secret, enabled) VALUES ('client1', 'https://client.fr/h', 's', true), ($1, 'https://hub.test', 's', false)`,
		activitesql.SubscriptionID)
	sendMH(t, db, "ws1_a", "marie@x.fr")
	exec(t, db, `UPDATE message_history SET opened_at = NOW() WHERE id = 'ws1_a'`)
	if n := count(t, db, `TRUE`); n != 0 {
		t.Fatalf("espace non relié au Hub : %d lignes", n)
	}
	subscribe(t, db, "https://hub.test/api/evenements/notifuse", testSecret)
	sendMH(t, db, "ws1_b", "marie@x.fr")
	exec(t, db, `INSERT INTO message_history (id, contact_email, channel, sent_at) VALUES ('ws1_sms', 'm@x.fr', 'sms', NOW())`)
	exec(t, db, `INSERT INTO message_history (id, contact_email) VALUES ('ws1_pas_parti', 'm@x.fr')`) // rejeté par une porte : jamais « envoyé »
	exec(t, db, `UPDATE message_history SET sent_at = NOW() WHERE id = 'ws1_pas_parti'`)              // parti plus tard : un envoyé
	// Deux ouvertures, la seconde forcée par une remise à zéro (que l'upstream ne fait jamais) : la file n'en garde qu'une.
	exec(t, db, `UPDATE message_history SET opened_at = NOW() WHERE id = 'ws1_b'`)
	exec(t, db, `UPDATE message_history SET opened_at = NULL WHERE id = 'ws1_b'`)
	exec(t, db, `UPDATE message_history SET opened_at = NOW() WHERE id = 'ws1_b'`)
	exec(t, db, `UPDATE message_history SET clicked_at = NOW(), delivered_at = NOW() WHERE id = 'ws1_b'`) // une mise à jour, deux événements
	exec(t, db, `UPDATE message_history SET clicked_at = NULL WHERE id = 'ws1_b'`)
	exec(t, db, `UPDATE message_history SET clicked_at = NOW() WHERE id = 'ws1_b'`)
	exec(t, db, `UPDATE message_history SET bounced_at = NOW(), bounce_type = 'HardBounce' WHERE id = 'ws1_b'`)
	exec(t, db, `UPDATE message_history SET unsubscribed_at = NOW() WHERE id = 'ws1_b'`)
	got := map[string]int{}
	rows, _ := db.Query(`SELECT ref_id || ' ' || event FROM veridian_activite_outbox`)
	for rows.Next() {
		var k string
		rows.Scan(&k)
		got[k]++
	}
	rows.Close()
	want := []string{"ws1_b message.sent", "ws1_b message.opened", "ws1_b message.clicked", "ws1_b message.delivered", "ws1_b message.bounced",
		"ws1_b message.unsubscribed", "ws1_pas_parti message.sent"}
	for _, k := range want {
		if got[k] != 1 {
			t.Errorf("%s : %d ligne(s), attendu 1", k, got[k])
		}
	}
	if len(got) != len(want) {
		t.Fatalf("lignes inattendues : %v", got)
	}
}

func inMail(t *testing.T, db *sql.DB, id, kind, mid string, headers string) {
	exec(t, db, `INSERT INTO veridian_mail_message (id, direction, kind, message_id, dedup_key, thread_id, contact_email, from_addr, from_name, subject,
		occurred_at, received_at, headers, body_text) VALUES ($1, 'in', $2, $3, 'mid:' || lower($3), 't_1', 'marie@x.fr', 'marie@x.fr', 'Marie Dupont',
		'Re: Présentation', NOW(), NOW(), $4::jsonb, 'Oui, appelez-moi CORPS SECRET')`, id, kind, mid, headers)
}

func TestDeclencheurReçus_UneLigneParMailQuiCompte(t *testing.T) {
	db := testDB(t)
	subscribe(t, db, "https://hub.test/api/evenements/notifuse", testSecret)
	inMail(t, db, "r1", "reply_human", "<rep1@x.fr>", `{}`)
	inMail(t, db, "r2", "reply_human", "<rep1@x.fr>", `{}`)                                    // même mail rangé par une seconde boîte
	inMail(t, db, "r3", "reply_auto", "<auto@x.fr>", `{"X-Sieve-Redirected-From":["a@b.fr"]}`) // copie Sieve
	inMail(t, db, "r4", "other", "<news@x.fr>", `{}`)                                          // sans rapport avec un envoi
	inMail(t, db, "r5", "bounce_hard", "<ndr@x.fr>", `{}`)                                     // rejet
	if n, n1, n5 := count(t, db, `TRUE`), count(t, db, `ref_id = 'r1'`), count(t, db, `ref_id = 'r5'`); n != 2 || n1 != 1 || n5 != 1 {
		t.Fatalf("reçus retenus : %d (r1 %d, r5 %d)", n, n1, n5)
	}
}

// L'événement porte objet, adresses, nom, fil et Message-ID ; jamais le corps.
func TestReadReady_EvenementCompletSansCorps(t *testing.T) {
	db := testDB(t)
	subscribe(t, db, "https://hub.test/api/evenements/notifuse", testSecret)
	sendMH(t, db, "ws1_6f1c2d3e-0000-4000-8000-000000000001", "Marie@X.fr")
	exec(t, db, `INSERT INTO veridian_mail_message (id, direction, kind, message_id, thread_id, contact_email, from_addr, from_name, subject, occurred_at,
		sent_ref, body_text, body_html_sanitized) VALUES ('o1', 'out', 'sent', '<6f1c2d3e-0000-4000-8000-000000000001@agence.fr>', 't_9', 'marie@x.fr',
		'hugo@agence.fr', 'Hugo Bourdat', 'Présentation', NOW(), 'ws1_6f1c2d3e-0000-4000-8000-000000000001', 'CORPS SECRET', '<p>CORPS SECRET</p>')`)
	inMail(t, db, "r1", "reply_human", "<rep1@x.fr>", `{}`)
	rows, orphans, err := ReadReady(context.Background(), db, 0, 10)
	if err != nil || len(orphans) != 0 || len(rows) != 2 {
		t.Fatalf("%d lignes, %d orphelines, %v", len(rows), len(orphans), err)
	}
	s, r := rows[0].Event, rows[1].Event
	if s.Event != "message.sent" || s.RFCMessageID != "6f1c2d3e-0000-4000-8000-000000000001@agence.fr" || s.ThreadID != "t_9" ||
		s.RecipientEmail != "marie@x.fr" || s.FromEmail != "hugo@agence.fr" || s.FromName != "Hugo Bourdat" || s.Subject != "Présentation" ||
		s.Nature != NatureCampaign || s.BroadcastID != "bc1" || s.ListID != "l1" {
		t.Fatalf("envoyé : %+v", s)
	}
	if r.Event != "message.received" || r.RFCMessageID != "rep1@x.fr" || r.FromName != "Marie Dupont" || r.Nature != NatureReplyHuman ||
		r.RecipientEmail != "marie@x.fr" || r.ThreadID != "t_1" {
		t.Fatalf("reçu : %+v", r)
	}
	for _, b := range Split("ws1", rows, MaxEvents, MaxBytes) {
		if strings.Contains(string(b.Body), "CORPS SECRET") || strings.Contains(string(b.Body), "appelez") {
			t.Fatalf("le lot ne porte jamais le corps : %s", b.Body)
		}
	}
	// Sans le rendu rangé, le Message-ID se recalcule par la règle de l'envoi, l'objet vient de l'historique.
	exec(t, db, `DELETE FROM veridian_mail_message WHERE id = 'o1'`)
	exec(t, db, `UPDATE message_history SET channel_options = '{"from_name":"Hugo","subject":"Objet historique"}'`)
	rows, _, _ = ReadReady(context.Background(), db, 0, 10)
	if s := rows[0].Event; s.RFCMessageID != "6f1c2d3e-0000-4000-8000-000000000001@agence.fr" || s.Subject != "Objet historique" || s.FromName != "Hugo" {
		t.Fatalf("repli : %+v", s)
	}
}

// fakeHub : le Hub de l'épreuve. Il vérifie chaque signature par la bibliothèque de référence et la routine du Hub, et répond ce qu'on lui dit.
type fakeHub struct {
	t      *testing.T
	mu     sync.Mutex
	secret string
	status []int // une réponse par appel ; la dernière se répète
	header http.Header
	reason string // un 200 « rien relayé »
	lots   []Lot
	calls  atomic.Int32
}

func (f *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	n := int(f.calls.Add(1))
	if r.URL.Path != "/api/evenements/notifuse/lot" {
		f.t.Errorf("chemin %s", r.URL.Path)
	}
	verifyBoth(f.t, f.secret, r.Header, body)
	var l Lot
	if err := json.Unmarshal(body, &l); err != nil || r.Header.Get("webhook-id") != l.LotID {
		f.t.Errorf("webhook-id = lot_id : %v", err)
	}
	f.lots = append(f.lots, l)
	code := 200
	if f.reason != "" {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"acceptes":0,"doublons":0,"ignores":[],"raison":%q}`, f.reason)
		return
	}
	if len(f.status) > 0 {
		code = f.status[min(n-1, len(f.status)-1)]
	}
	for k, v := range f.header {
		w.Header()[k] = v
	}
	w.WriteHeader(code)
	if code == 200 {
		fmt.Fprintf(w, `{"acceptes":%d,"doublons":0,"ignores":[]}`, len(l.Events))
	}
}

type oneWS struct{ db *sql.DB }

func (o oneWS) IDs(context.Context) ([]string, error)       { return []string{"ws1"}, nil }
func (o oneWS) DB(context.Context, string) (*sql.DB, error) { return o.db, nil }

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newWorker(db *sql.DB, hubURL string, c *clock) *Worker {
	return &Worker{WS: oneWS{db}, Sender: &Sender{}, Log: logger.NewLoggerWithLevel("error"), Enabled: true,
		EventsURL: hubURL + "/api/evenements/notifuse", Settle: -1, Now: c.now}
}

func fill(t *testing.T, db *sql.DB, n int) {
	exec(t, db, `INSERT INTO message_history (id, contact_email, sent_at, veridian_sender_email) SELECT 'ws1_' || g, 'c' || g || '@x.fr', NOW(), 'h@a.fr'
		FROM generate_series(1, $1) g`, n)
}

func TestWorker_LotsDe200SignesEtClos(t *testing.T) {
	db := testDB(t)
	hub := &fakeHub{t: t, secret: testSecret}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	subscribe(t, db, srv.URL+"/api/evenements/notifuse", testSecret)
	fill(t, db, 201)
	w := newWorker(db, srv.URL, &clock{time.Now()})
	w.Pass(context.Background())
	if len(hub.lots) != 2 || len(hub.lots[0].Events) != 200 || len(hub.lots[1].Events) != 1 || hub.lots[0].WorkspaceID != "ws1" {
		t.Fatalf("lots reçus : %d", len(hub.lots))
	}
	if n := count(t, db, `done_at IS NOT NULL AND outcome = 'accepte' AND lot_id IS NOT NULL`); n != 201 {
		t.Fatalf("lignes closes : %d", n)
	}
	var out strings.Builder
	w.Metrics.Write(&out)
	if !strings.Contains(out.String(), `veridian_activite_events_total{resultat="accepte"} 201`) || !strings.Contains(out.String(), `veridian_activite_lots_total{resultat="accepte"} 2`) {
		t.Fatalf("métriques : %s", out.String())
	}
}

// 400 (comme 409 et 413) : le lot est abandonné, jamais rejoué.
func TestWorker_400NonRejoue(t *testing.T) {
	db := testDB(t)
	hub := &fakeHub{t: t, secret: testSecret, status: []int{400}}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	subscribe(t, db, srv.URL+"/api/evenements/notifuse", testSecret)
	fill(t, db, 3)
	c := &clock{time.Now()}
	w := newWorker(db, srv.URL, c)
	w.Pass(context.Background())
	c.t = c.t.Add(2 * time.Hour)
	w.Pass(context.Background())
	if hub.calls.Load() != 1 || count(t, db, `outcome = 'refuse'`) != 3 || count(t, db, `done_at IS NULL`) != 0 {
		t.Fatalf("appels %d, refusés %d", hub.calls.Load(), count(t, db, `outcome = 'refuse'`))
	}
}

// 503 avec Retry-After : rien avant l'attente, rejeu après, les lignes restent en file entre-temps.
func TestWorker_5xxRejoueAvecAttente(t *testing.T) {
	db := testDB(t)
	hub := &fakeHub{t: t, secret: testSecret, status: []int{503, 503, 200}, header: http.Header{"Retry-After": {"120"}}}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	subscribe(t, db, srv.URL+"/api/evenements/notifuse", testSecret)
	fill(t, db, 3)
	c := &clock{time.Now()}
	w := newWorker(db, srv.URL, c)
	w.Pass(context.Background())
	if hub.calls.Load() != 1 || count(t, db, `done_at IS NULL AND attempts = 1`) != 3 {
		t.Fatalf("premier essai : %d appels", hub.calls.Load())
	}
	c.t = c.t.Add(60 * time.Second)
	w.Pass(context.Background())
	if hub.calls.Load() != 1 {
		t.Fatal("Retry-After de 120 s respecté : aucun rejeu à 60 s")
	}
	c.t = c.t.Add(61 * time.Second)
	w.Pass(context.Background())
	if hub.calls.Load() != 2 {
		t.Fatalf("rejeu après l'attente : %d appels", hub.calls.Load())
	}
	c.t = c.t.Add(30 * time.Second) // second échec : l'attente croît (au moins 1 min, et Retry-After)
	w.Pass(context.Background())
	if hub.calls.Load() != 2 {
		t.Fatal("attente croissante après le second échec")
	}
	c.t = c.t.Add(2 * time.Minute)
	w.Pass(context.Background())
	if hub.calls.Load() != 3 || count(t, db, `outcome = 'accepte'`) != 3 {
		t.Fatalf("remis au troisième essai : %d appels", hub.calls.Load())
	}
}

// 401 : rien n'est perdu, pause ; un nouveau secret (abonnement resynchronisé) relance aussitôt.
func TestWorker_401RejoueApresResynchronisation(t *testing.T) {
	db := testDB(t)
	const newSecret = "whsec_c2Vjb25kLXNlY3JldC1kZS10ZXN0LTMyLW9jdGV0cyE="
	hub := &fakeHub{t: t, secret: testSecret, status: []int{401, 200}}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	subscribe(t, db, srv.URL+"/api/evenements/notifuse", testSecret)
	fill(t, db, 2)
	c := &clock{time.Now()}
	w := newWorker(db, srv.URL, c)
	w.Pass(context.Background())
	c.t = c.t.Add(5 * time.Second)
	w.Pass(context.Background())
	if hub.calls.Load() != 1 || count(t, db, `done_at IS NULL`) != 2 {
		t.Fatalf("même secret : pas de rejeu immédiat (%d appels)", hub.calls.Load())
	}
	hub.mu.Lock()
	hub.secret = newSecret
	hub.mu.Unlock()
	subscribe(t, db, srv.URL+"/api/evenements/notifuse", newSecret)
	w.Pass(context.Background())
	if hub.calls.Load() != 2 || count(t, db, `outcome = 'accepte'`) != 2 {
		t.Fatalf("nouveau secret : rejeu (%d appels)", hub.calls.Load())
	}
}

// Un abonnement modifié vers une autre adresse n'est jamais suivi.
func TestWorker_AdresseDeConfigurationSeulement(t *testing.T) {
	db := testDB(t)
	hub := &fakeHub{t: t, secret: testSecret}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	subscribe(t, db, srv.URL+"/ailleurs", testSecret)
	fill(t, db, 1)
	w := newWorker(db, srv.URL, &clock{time.Now()})
	w.Pass(context.Background())
	if hub.calls.Load() != 0 || count(t, db, `done_at IS NULL`) != 1 {
		t.Fatal("abonnement détourné : rien n'est envoyé")
	}
}

// Interrupteur éteint : la file se remplit, rien ne part ; les bornes tiennent (7 jours, taille).
func TestWorker_EteintEtBornes(t *testing.T) {
	db := testDB(t)
	hub := &fakeHub{t: t, secret: testSecret}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	subscribe(t, db, srv.URL+"/api/evenements/notifuse", testSecret)
	fill(t, db, 8)
	exec(t, db, `UPDATE veridian_activite_outbox SET occurred_at = NOW() - INTERVAL '8 days' WHERE ref_id = 'ws1_1'`)
	w := newWorker(db, srv.URL, &clock{time.Now()})
	w.Enabled = false
	w.Bounds = Bounds{MaxAge: 7 * 24 * time.Hour, KeepDone: time.Hour, MaxPending: 5, MaxDone: 100}
	w.Pass(context.Background())
	if hub.calls.Load() != 0 {
		t.Fatal("éteint : aucun envoi")
	}
	if e, o, p := count(t, db, `outcome = 'perime'`), count(t, db, `outcome = 'debordement'`), count(t, db, `done_at IS NULL`); e != 1 || o != 2 || p != 5 {
		t.Fatalf("périmés %d, débordement %d, en attente %d", e, o, p)
	}
	if count(t, db, `done_at IS NULL AND ref_id IN ('ws1_2', 'ws1_3')`) != 0 {
		t.Fatal("le débordement retire les plus vieux")
	}
	var out strings.Builder
	w.Metrics.Write(&out)
	if !strings.Contains(out.String(), "veridian_activite_pending_events 5") || !strings.Contains(out.String(), "veridian_activite_enabled 0") {
		t.Fatalf("métriques : %s", out.String())
	}
}

// L'hôte posé par le filtre des clics part avec message.clicked.
func TestWorker_HoteDuClic(t *testing.T) {
	db := testDB(t)
	hub := &fakeHub{t: t, secret: testSecret}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	subscribe(t, db, srv.URL+"/api/evenements/notifuse", testSecret)
	fill(t, db, 1)
	exec(t, db, `UPDATE message_history SET clicked_at = NOW(), opened_at = NOW()`)
	if err := SetClickHost(context.Background(), db, "ws1_1", HostOf("https://boutique.exemple.fr/p?jeton=abc")); err != nil {
		t.Fatal(err)
	}
	newWorker(db, srv.URL, &clock{time.Now()}).Pass(context.Background())
	var clicked *Event
	for _, l := range hub.lots {
		for i, e := range l.Events {
			if e.Event == "message.clicked" {
				clicked = &l.Events[i]
			}
		}
	}
	if clicked == nil || clicked.Host != "boutique.exemple.fr" {
		t.Fatalf("clic : %+v", clicked)
	}
	b, _ := json.Marshal(hub.lots)
	if strings.Contains(string(b), "jeton") || strings.Contains(string(b), "/p") {
		t.Fatalf("jamais l'URL : %s", b)
	}
}

// Liste d'espaces : seul un espace nommé envoie, les autres gardent leur file.
func TestWorker_SeulsLesEspacesNommes(t *testing.T) {
	db := testDB(t)
	hub := &fakeHub{t: t, secret: testSecret}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	subscribe(t, db, srv.URL+"/api/evenements/notifuse", testSecret)
	fill(t, db, 1)
	w := newWorker(db, srv.URL, &clock{time.Now()})
	w.Only = map[string]bool{"autre": true}
	w.Pass(context.Background())
	if hub.calls.Load() != 0 || count(t, db, `done_at IS NULL`) != 1 {
		t.Fatal("espace hors liste : rien n'est envoyé")
	}
	w.Only = map[string]bool{"ws1": true}
	w.Pass(context.Background())
	if hub.calls.Load() != 1 {
		t.Fatal("espace nommé : envoyé")
	}
}

// 200 avec raison (phase de test du Hub, liaison éteinte, organisation suspendue) : traité, jamais rejoué, et jamais compté en succès.
func TestWorker_200AvecRaisonCompteAPart(t *testing.T) {
	db := testDB(t)
	hub := &fakeHub{t: t, secret: testSecret, reason: "evenements_fermes"}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	subscribe(t, db, srv.URL+"/api/evenements/notifuse", testSecret)
	fill(t, db, 3)
	c := &clock{time.Now()}
	w := newWorker(db, srv.URL, c)
	w.Pass(context.Background())
	c.t = c.t.Add(time.Hour)
	w.Pass(context.Background())
	if hub.calls.Load() != 1 || count(t, db, `outcome = 'ferme'`) != 3 || count(t, db, `done_at IS NULL`) != 0 {
		t.Fatalf("appels %d, fermés %d", hub.calls.Load(), count(t, db, `outcome = 'ferme'`))
	}
	var out strings.Builder
	w.Metrics.Write(&out)
	for _, want := range []string{`veridian_activite_lots_fermes_total{raison="evenements_fermes"} 1`, `veridian_activite_events_total{resultat="ferme"} 3`,
		`veridian_activite_events_total{resultat="accepte"} 0`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("%s absent : %s", want, out.String())
		}
	}
	if ClosedReason("nouvelle_raison") != "autre" || ClosedReason("paire_eteinte") != "paire_eteinte" || ClosedReason("") != "" {
		t.Fatal("étiquette bornée aux raisons connues")
	}
}

// Un espace qui n'a plus l'abonnement du Hub : rien ne part, ses lignes sont closes « desabonne » passé une heure, comptées.
func TestWorker_EspaceDesabonne(t *testing.T) {
	db := testDB(t)
	hub := &fakeHub{t: t, secret: testSecret}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	subscribe(t, db, srv.URL+"/api/evenements/notifuse", testSecret)
	fill(t, db, 3)
	exec(t, db, `UPDATE webhook_subscriptions SET enabled = false`) // abonnement éteint par le Hub
	exec(t, db, `UPDATE veridian_activite_outbox SET created_at = NOW() - INTERVAL '2 hours' WHERE ref_id <> 'ws1_3'`)
	w := newWorker(db, srv.URL, &clock{time.Now()})
	w.Pass(context.Background())
	if hub.calls.Load() != 0 {
		t.Fatal("espace désabonné : rien ne part vers le Hub")
	}
	if c, p := count(t, db, `outcome = 'desabonne'`), count(t, db, `done_at IS NULL`); c != 2 || p != 1 {
		t.Fatalf("closes %d, en attente %d (la ligne récente attend un réabonnement)", c, p)
	}
	var out strings.Builder
	w.Metrics.Write(&out)
	if !strings.Contains(out.String(), `veridian_activite_events_total{resultat="desabonne"} 2`) {
		t.Fatalf("métriques : %s", out.String())
	}
	exec(t, db, `UPDATE webhook_subscriptions SET enabled = true`) // réabonné : la ligne récente part
	w.Pass(context.Background())
	if hub.calls.Load() != 1 || len(hub.lots[0].Events) != 1 {
		t.Fatalf("réabonné : %d appels", hub.calls.Load())
	}
}
