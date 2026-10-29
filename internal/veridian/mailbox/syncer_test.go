package mailbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// fakeBox simule une boîte IMAP (Dovecot) : des messages par UID, un UIDVALIDITY.
type fakeBox struct {
	mu       sync.Mutex
	validity uint32
	msgs     map[uint32][]byte
	failNext int // nombre de FetchRaw à faire échouer
	fetched  []uint32
	dialErr  error
}

func newFakeBox() *fakeBox { return &fakeBox{validity: 7, msgs: map[uint32][]byte{}} }

func (b *fakeBox) Dial(context.Context, *domain.IMAPSettings) (Session, error) {
	if b.dialErr != nil {
		return nil, b.dialErr
	}
	return &fakeSession{b: b}, nil
}

type fakeSession struct{ b *fakeBox }

func (s *fakeSession) UIDValidity() uint32 { return s.b.validity }
func (s *fakeSession) Close() error        { return nil }
func (s *fakeSession) ListUIDs(_ context.Context, after uint32, _ time.Time) ([]UIDInfo, error) {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	var out []UIDInfo
	for u, raw := range s.b.msgs {
		if u > after {
			out = append(out, UIDInfo{UID: u, Size: int64(len(raw))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}
func (s *fakeSession) FetchRaw(_ context.Context, uids []uint32) ([]RawMessage, error) {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	if s.b.failNext > 0 {
		s.b.failNext--
		return nil, errors.New("connexion coupée")
	}
	var out []RawMessage
	for _, u := range uids {
		s.b.fetched = append(s.b.fetched, u)
		out = append(out, RawMessage{UID: u, Raw: s.b.msgs[u], InternalDate: time.Now()})
	}
	return out, nil
}

func testWorkspace() (*domain.Workspace, *domain.Integration) {
	imap := domain.Integration{ID: "imap1", Name: "relais", Type: domain.IntegrationTypeIMAP,
		IMAPSettings: &domain.IMAPSettings{Host: "h", Port: 993, Username: "hugo.bourdat@animalservicedistribution.fr", Password: "x", UseTLS: true}}
	prof := domain.Integration{ID: "prof1", Name: "prospection-asd-2", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{VeridianReturnIMAPIntegrationID: "imap1",
			Senders: []domain.EmailSender{{Email: "hugo.bourdat@animalservicedistribution.fr", IsDefault: true}}}}
	ws := &domain.Workspace{ID: "ws1", Integrations: []domain.Integration{prof, imap}}
	return ws, &ws.Integrations[1]
}

func replyN(i int) []byte {
	return []byte(strings.Replace(string(replyHuman("<"+j0uuid+"@x.fr>", "<"+j0uuid+"@x.fr>")), "rep1@", fmt.Sprintf("rep%d@", i), 1))
}

func countRows(t *testing.T, s *Store) int {
	var n int
	if err := s.mustDB(t).QueryRow(`SELECT COUNT(*) FROM veridian_mail_message`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSyncer_CatchUpByUIDAndRestart(t *testing.T) {
	s, db := testStore(t)
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")
	box := newFakeBox()
	for i := uint32(10); i < 15; i++ {
		box.msgs[i] = replyN(int(i))
	}
	ws, integ := testWorkspace()
	log := logger.NewLoggerWithLevel("disabled")
	sy := NewSyncer(s, NewIngestor(s, log, nil), box, nil, log)
	ctx := context.Background()

	sy.SyncBox(ctx, ws, integ)
	if n := countRows(t, s); n != 5 {
		t.Fatalf("5 messages attendus après le premier passage, %d", n)
	}
	st, _ := s.GetSync(ctx, "ws1", "imap1", "INBOX")
	if st == nil || st.LastUID != 14 || st.UIDValidity != 7 || st.State.TotalIngested != 5 || st.State.LastOKAt == nil {
		t.Fatalf("repère: %+v", st)
	}

	// Notifuse « arrêté » : le courrier continue d'arriver dans la boîte du relais.
	for i := uint32(15); i < 19; i++ {
		box.msgs[i] = replyN(int(i))
	}
	// « redémarrage » : nouveau Syncer, même base. Rien relu, rien perdu.
	box.fetched = nil
	sy2 := NewSyncer(s, NewIngestor(s, log, nil), box, nil, log)
	sy2.SyncBox(ctx, ws, integ)
	if n := countRows(t, s); n != 9 {
		t.Fatalf("9 messages attendus après rattrapage, %d", n)
	}
	for _, u := range box.fetched {
		if u < 15 {
			t.Fatalf("UID %d relu alors qu'il était déjà traité", u)
		}
	}
	// passage à vide
	box.fetched = nil
	sy2.SyncBox(ctx, ws, integ)
	if len(box.fetched) != 0 || countRows(t, s) != 9 {
		t.Fatalf("passage à vide: lu %v", box.fetched)
	}
}

func TestSyncer_FetchFailureKeepsWatermark(t *testing.T) {
	s, db := testStore(t)
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")
	box := newFakeBox()
	for i := uint32(1); i <= 60; i++ { // 60 messages : 3 lots de 25 au plus
		box.msgs[i] = replyN(int(i))
	}
	ws, integ := testWorkspace()
	log := logger.NewLoggerWithLevel("disabled")
	sy := NewSyncer(s, NewIngestor(s, log, nil), box, nil, log)
	ctx := context.Background()

	// le 2e lot échoue : on garde le 1er, le repère s'arrête au bon UID, l'erreur est visible
	var calls int
	box.failNext = 0
	orig := box.msgs
	_ = orig
	box.failNext = 0
	// échoue au 2e FetchRaw : on arme après le premier
	failing := &failAfter{fakeBox: box, ok: 1}
	sy.dialer = failing
	sy.SyncBox(ctx, ws, integ)
	calls = countRows(t, s)
	if calls != 25 {
		t.Fatalf("seul le premier lot (25) doit être rangé: %d", calls)
	}
	st, _ := s.GetSync(ctx, "ws1", "imap1", "INBOX")
	if st.LastUID != 25 || st.State.LastError == "" {
		t.Fatalf("repère après échec: %+v", st)
	}
	// reprise : la connexion revient, le reste est rattrapé sans doublon
	sy.dialer = box
	sy.SyncBox(ctx, ws, integ)
	if n := countRows(t, s); n != 60 {
		t.Fatalf("60 attendus après reprise, %d", n)
	}
	st, _ = s.GetSync(ctx, "ws1", "imap1", "INBOX")
	if st.LastUID != 60 || st.State.LastError != "" {
		t.Fatalf("%+v", st)
	}
}

type failAfter struct {
	*fakeBox
	ok int
}

func (f *failAfter) Dial(ctx context.Context, s *domain.IMAPSettings) (Session, error) {
	sess, err := f.fakeBox.Dial(ctx, s)
	if err != nil {
		return nil, err
	}
	return &failingSession{Session: sess, ok: f.ok}, nil
}

type failingSession struct {
	Session
	ok int
}

func (f *failingSession) FetchRaw(ctx context.Context, uids []uint32) ([]RawMessage, error) {
	if f.ok <= 0 {
		return nil, errors.New("connexion coupée")
	}
	f.ok--
	return f.Session.FetchRaw(ctx, uids)
}

func TestSyncer_UIDValidityChangeDoesNotDuplicate(t *testing.T) {
	s, db := testStore(t)
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")
	box := newFakeBox()
	for i := uint32(1); i <= 3; i++ {
		box.msgs[i] = replyN(int(i))
	}
	ws, integ := testWorkspace()
	log := logger.NewLoggerWithLevel("disabled")
	sy := NewSyncer(s, NewIngestor(s, log, nil), box, nil, log)
	ctx := context.Background()
	sy.SyncBox(ctx, ws, integ)
	// le serveur renumérote : nouvel UIDVALIDITY, mêmes messages sous d'autres UID
	box.validity = 99
	renum := map[uint32][]byte{}
	for u, raw := range box.msgs {
		renum[u+100] = raw
	}
	box.msgs = renum
	sy.SyncBox(ctx, ws, integ)
	if n := countRows(t, s); n != 3 {
		t.Fatalf("une renumérotation ne doit pas dupliquer: %d lignes", n)
	}
	st, _ := s.GetSync(ctx, "ws1", "imap1", "INBOX")
	if st.UIDValidity != 99 || st.LastUID != 103 {
		t.Fatalf("%+v", st)
	}
}

func TestSyncer_DialErrorIsRecordedNotFatal(t *testing.T) {
	s, _ := testStore(t)
	box := newFakeBox()
	box.dialErr = errors.New("authentification IMAP: refusée")
	ws, integ := testWorkspace()
	log := logger.NewLoggerWithLevel("disabled")
	sy := NewSyncer(s, NewIngestor(s, log, nil), box, nil, log)
	sy.SyncBox(context.Background(), ws, integ)
	m, _ := s.ListSync(context.Background(), "ws1")
	if m["imap1"].LastError == "" || m["imap1"].LastOKAt != nil {
		t.Fatalf("%+v", m)
	}
}

type fakeRenderer struct{ calls int }

func (f *fakeRenderer) Render(_ context.Context, _ string, tpl string, _ int64, data map[string]interface{}) (string, string, string, error) {
	f.calls++
	if tpl == "casse" {
		return "", "", "", errors.New("modèle supprimé")
	}
	return "Objet rendu", fmt.Sprintf("<p>Bonjour %v</p>", data["first_name"]), "", nil
}

func TestSyncer_HistoryImportCursorAndRender(t *testing.T) {
	s, db := testStore(t)
	ctx := context.Background()
	key := "cle-de-test"
	enc, _ := cryptoEnc(`{"first_name":"Marie"}`, key)
	ins := func(id, contact, tpl string, ago time.Duration, md string) {
		if _, err := db.Exec(`INSERT INTO message_history (id, contact_email, template_id, message_data, sent_at, veridian_profile_id, veridian_sender_email)
			VALUES ($1,$2,$3,$4::jsonb, NOW() - $5::interval, 'prof1', 'hugo.bourdat@animalservicedistribution.fr')`, id, contact, tpl, md, fmt.Sprintf("%d seconds", int(ago.Seconds()))); err != nil {
			t.Fatal(err)
		}
	}
	md := `{"data":{"_encrypted":"` + enc + `"}}`
	ins("ws1_a1111111-1111-4111-8111-111111111111", "marie@exemple-animaux.fr", "tpl", 3*time.Hour, md)
	ins("ws1_a2222222-2222-4222-8222-222222222222", "marie@exemple-animaux.fr", "tpl", 2*time.Hour, `{"data":{},"metadata":{"veridian_subject":"Mon objet exact"}}`)
	ins("ws1_a3333333-3333-4333-8333-333333333333", "paul@x.fr", "casse", time.Hour, `{"data":{}}`)
	ws, _ := testWorkspace()
	rend := &fakeRenderer{}
	log := logger.NewLoggerWithLevel("disabled")
	sy := NewSyncer(s, NewIngestor(s, log, nil), newFakeBox(), rend, log)

	sy.SyncWorkspace(ctx, ws)
	if n := countRows(t, s); n != 3 {
		t.Fatalf("3 envois repris: %d", n)
	}
	var id string
	db.QueryRow(`SELECT id FROM veridian_mail_message WHERE sent_ref='ws1_a1111111-1111-4111-8111-111111111111'`).Scan(&id)
	m, err := s.Get(ctx, "ws1", id)
	if err != nil {
		t.Fatal(err)
	}
	if m.BodySource != BodyRerendered || !strings.Contains(m.BodyHTMLSanitized, "Bonjour Marie") || m.Subject != "Objet rendu" {
		t.Fatalf("reconstitution depuis les données chiffrées: %+v", m)
	}
	if m.Kind != KindSent || m.Direction != DirectionOut || m.From != "hugo.bourdat@animalservicedistribution.fr" || m.ProfileID != "prof1" || m.Link.AutomationID != "" {
		t.Fatalf("%+v", m)
	}
	// objet exact consigné par le worker : prioritaire sur le rendu
	db.QueryRow(`SELECT id FROM veridian_mail_message WHERE sent_ref='ws1_a2222222-2222-4222-8222-222222222222'`).Scan(&id)
	m2, _ := s.Get(ctx, "ws1", id)
	if m2.Subject != "Mon objet exact" {
		t.Fatalf("%q", m2.Subject)
	}
	// modèle cassé : le mail est tout de même repris, corps indisponible, dit honnêtement
	db.QueryRow(`SELECT id FROM veridian_mail_message WHERE sent_ref='ws1_a3333333-3333-4333-8333-333333333333'`).Scan(&id)
	m3, _ := s.Get(ctx, "ws1", id)
	if m3.BodySource != BodyUnavailable || m3.Subject != "(objet non conservé)" {
		t.Fatalf("%+v", m3)
	}
	// le curseur avance : un second passage ne refait rien, un nouvel envoi est repris seul
	before := rend.calls
	sy.SyncWorkspace(ctx, ws)
	if rend.calls != before || countRows(t, s) != 3 {
		t.Fatalf("second passage: %d appels, %d lignes", rend.calls-before, countRows(t, s))
	}
	ins("ws1_a4444444-4444-4444-8444-444444444444", "luc@x.fr", "tpl", time.Minute, md)
	sy.SyncWorkspace(ctx, ws)
	if countRows(t, s) != 4 || rend.calls != before+1 {
		t.Fatalf("nouvel envoi: %d lignes, %d appels", countRows(t, s), rend.calls-before)
	}
}
