package boxsend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/ratelimiter"
	"github.com/Notifuse/notifuse/pkg/smtp_bridge"
	"github.com/emersion/go-smtp"
)

type fake struct {
	acc      boxaccess.Access
	alive    error
	verify   error
	cap      error
	reserved int
	ws       *domain.Workspace
	supp     map[string]domain.ContactListStatus
	sent     []domain.SendEmailProviderRequest
	sendErr  error
	history  []*domain.MessageHistory
	recorded []mailbox.SentInput
	noBudget bool
}

func (f *fake) Verify(_ context.Context, _, _ string) (*boxaccess.Access, error) {
	if f.verify != nil {
		return nil, f.verify
	}
	return &f.acc, nil
}
func (f *fake) Alive(_ context.Context, id string) (*boxaccess.Access, error) {
	if f.alive != nil {
		return nil, f.alive
	}
	if id != f.acc.ID {
		return nil, boxaccess.ErrAuth
	}
	return &f.acc, nil
}
func (f *fake) Touch(context.Context, string) error { return nil }
func (f *fake) ReserveSend(_ context.Context, _ string, n int) error {
	if f.cap == nil {
		f.reserved += n
	}
	return f.cap
}
func (f *fake) GetByID(context.Context, string) (*domain.Workspace, error) { return f.ws, nil }
func (f *fake) ContactListStatuses(_ context.Context, _, e string) ([]domain.ContactListStatus, error) {
	if s, ok := f.supp[e]; ok {
		return []domain.ContactListStatus{s}, nil
	}
	return nil, nil
}
func (f *fake) SendEmail(_ context.Context, r domain.SendEmailProviderRequest, _ bool) error {
	if f.sendErr == nil {
		f.sent = append(f.sent, r)
	}
	return f.sendErr
}
func (f *fake) Create(_ context.Context, _, _ string, m *domain.MessageHistory) error {
	f.history = append(f.history, m)
	return nil
}
func (f *fake) RecordSent(_ context.Context, in mailbox.SentInput) (bool, error) {
	f.recorded = append(f.recorded, in)
	return true, nil
}
func (f *fake) Allow(string, string) bool { return !f.noBudget }

func bench() (*fake, *Sender) {
	f := &fake{acc: boxaccess.Access{ID: "a1", WorkspaceID: "ws", ProfileID: "p", Address: "jc@boite.fr", CanSend: true},
		ws: &domain.Workspace{ID: "ws", Integrations: domain.Integrations{{ID: "p", Type: domain.IntegrationTypeEmail,
			EmailProvider: domain.EmailProvider{Senders: []domain.EmailSender{{Email: "jc@boite.fr"}, {Email: "autre@boite.fr"}}}}}}}
	SetRecorder(f)
	return f, New(Deps{Accesses: f, WS: f, Suppression: f, Mailer: f, History: f, Limiter: f, Logger: logger.NewLogger()})
}

func msg(from string, headers ...string) []byte {
	return []byte("From: JC <" + from + ">\r\nSubject: Re: Gravure\r\nMessage-Id: <abc@poste>\r\nIn-Reply-To: <p1@client.fr>\r\nReferences: <p1@client.fr>\r\n" + strings.Join(headers, "") + "Content-Type: text/plain; charset=utf-8\r\n\r\nBonjour\r\n")
}

func code(err error) int {
	var e *smtp.SMTPError
	if errors.As(err, &e) {
		for _, r := range e.Message {
			if r > 126 { // une réponse SMTP est en ASCII
				return -1
			}
		}
		return e.Code
	}
	return 0
}

func TestSubmit_SendsOnceThroughTheBoxProfile(t *testing.T) {
	f, s := bench()
	data := msg("jc@boite.fr", "To: p@client.fr, p2@client.fr\r\n", "Cc: c@client.fr\r\n", "Bcc: cache@client.fr,\r\n autre@client.fr\r\n")
	rcpts := []string{"P@client.fr", "p2@client.fr", "c@client.fr", "cache@client.fr", "p@client.fr"}
	if err := s.Submit(context.Background(), "box:a1", "jc@boite.fr", rcpts, data); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 {
		t.Fatalf("%d envois", len(f.sent))
	}
	r := f.sent[0]
	if r.IntegrationID != "p" || r.FromAddress != "jc@boite.fr" || r.To != "p@client.fr" || fmt.Sprint(r.EmailOptions.CC) != "[p2@client.fr c@client.fr]" || fmt.Sprint(r.EmailOptions.BCC) != "[cache@client.fr]" || r.EmailOptions.InReplyTo != "<p1@client.fr>" {
		t.Fatalf("%+v", r)
	}
	if f.reserved != 4 || len(f.history) != 1 || f.history[0].VeridianMessageType != "transactional" {
		t.Fatalf("réservés %d, historique %d", f.reserved, len(f.history))
	}
	in := f.recorded[0]
	if in.ProfileID != "p" || in.MessageHistoryID != "abc" || fmt.Sprint(in.To) != "[p@client.fr p2@client.fr]" || fmt.Sprint(in.Cc) != "[c@client.fr]" || strings.Contains(fmt.Sprint(in), "cache@") {
		t.Fatalf("rangement : %+v", in)
	}
	if raw := string(in.Raw); !strings.Contains(raw, "Bonjour") || !strings.Contains(raw, "Cc: c@client.fr") || strings.Contains(raw, "Bcc") || strings.Contains(raw, "autre@client.fr") {
		t.Fatalf("copie gardée : %q", raw)
	}
	// Reprise du même message par le client : 250 sans second envoi.
	if err := s.Submit(context.Background(), "box:a1", "jc@boite.fr", rcpts, data); err != nil || len(f.sent) != 1 {
		t.Fatalf("%v, %d envois", err, len(f.sent))
	}
}

func TestSubmit_Refusals(t *testing.T) {
	ok := msg("jc@boite.fr", "To: p@client.fr\r\n")
	for name, tc := range map[string]struct {
		prep func(*fake)
		data []byte
		env  string
		rcpt []string
		code int
	}{
		"autre adresse du même profil": {data: msg("autre@boite.fr", "To: p@client.fr\r\n"), env: "autre@boite.fr", code: 550},
		"usurpation":                   {data: msg("pdg@banque.fr", "To: p@client.fr\r\n"), env: "jc@boite.fr", code: 550},
		"enveloppe étrangère":          {data: ok, env: "x@y.fr", code: 550},
		"accès révoqué":                {prep: func(f *fake) { f.alive = boxaccess.ErrAuth }, data: ok, code: 535},
		"lecture seule":                {prep: func(f *fake) { f.acc.CanSend = false }, data: ok, code: 550},
		"profil en pause":              {prep: func(f *fake) { f.ws.Integrations[0].EmailProvider.VeridianPaused = true }, data: ok, code: 550},
		"adresse retirée du profil":    {prep: func(f *fake) { f.ws.Integrations[0].EmailProvider.Senders = nil }, data: ok, code: 535},
		"plafond du jour":              {prep: func(f *fake) { f.cap = boxaccess.ErrCap }, data: ok, code: 452},
		"panne des accès":              {prep: func(f *fake) { f.alive = errors.New("base") }, data: ok, code: 451},
		"aucun destinataire":           {data: ok, rcpt: []string{" "}, code: 554},
		"relais en erreur":             {prep: func(f *fake) { f.sendErr = errors.New("relais") }, data: ok, code: 451},
	} {
		f, s := bench()
		if tc.prep != nil {
			tc.prep(f)
		}
		if tc.env == "" {
			tc.env = "jc@boite.fr"
		}
		if tc.rcpt == nil {
			tc.rcpt = []string{"p@client.fr"}
		}
		if got := code(s.Submit(context.Background(), "box:a1", tc.env, tc.rcpt, tc.data)); got != tc.code || len(f.sent) != 0 || len(f.recorded) != 0 {
			t.Errorf("%s : code %d (attendu %d), %d envois", name, got, tc.code, len(f.sent))
		}
	}
}

func TestSubmit_FailedSendIsRetriable(t *testing.T) {
	f, s := bench()
	f.sendErr = errors.New("relais")
	data := msg("jc@boite.fr", "To: p@client.fr\r\n")
	if err := s.Submit(context.Background(), "box:a1", "jc@boite.fr", []string{"p@client.fr"}, data); code(err) != 451 {
		t.Fatal(err)
	}
	f.sendErr = nil
	if err := s.Submit(context.Background(), "box:a1", "jc@boite.fr", []string{"p@client.fr"}, data); err != nil || len(f.sent) != 1 {
		t.Fatalf("un envoi raté doit pouvoir repartir : %v, %d", err, len(f.sent))
	}
}

func TestCheckRecipient(t *testing.T) {
	f, s := bench()
	f.supp = map[string]domain.ContactListStatus{"mort@client.fr": domain.ContactListStatusBounced, "desinscrit@client.fr": domain.ContactListStatusUnsubscribed}
	for addr, want := range map[string]int{"mort@client.fr": 550, "desinscrit@client.fr": 0, "ok@client.fr": 0} {
		if got := code(s.CheckRecipient(context.Background(), "box:a1", addr)); got != want {
			t.Errorf("%s : %d", addr, got)
		}
	}
}

func TestLogin(t *testing.T) {
	f, s := bench()
	if id, err := s.Login(context.Background(), "jc@boite.fr", "x"); err != nil || id != "box:a1" || !IsBox(id) || IsBox("u-123") {
		t.Fatalf("%q %v", id, err)
	}
	f.verify = boxaccess.ErrAuth
	if _, err := s.Login(context.Background(), "jc@boite.fr", "x"); !errors.Is(err, boxaccess.ErrAuth) {
		t.Fatal(err)
	}
	f.verify = errors.New("base")
	if _, err := s.Login(context.Background(), "jc@boite.fr", "x"); !errors.Is(err, smtp_bridge.ErrTemporaryAuth) {
		t.Fatalf("une panne n'est pas un mauvais mot de passe : %v", err)
	}
	f.verify, f.noBudget = nil, true
	if _, err := s.Login(context.Background(), "jc@boite.fr", "x"); err != nil {
		t.Fatalf("un bon mot de passe n'est jamais refusé par la borne globale : %v", err)
	}
	if !LooksLikeAPIKey("aa.bb.cc") || LooksLikeAPIKey("abcdef234567") {
		t.Fatal("clé d'API ou mot de passe de boîte")
	}
}

func TestSplit_AllBlindGoesToFirst(t *testing.T) {
	to, cc, bcc := split([]byte("Subject: x\r\n\r\n"), []string{"a@x.fr", "b@x.fr"})
	if fmt.Sprint(to, cc, bcc) != "[a@x.fr] [] [b@x.fr]" {
		t.Fatal(to, cc, bcc)
	}
}

func TestWithoutBcc_LeavesTheRestUntouched(t *testing.T) {
	for in, want := range map[string]string{
		"A: b\r\nBcc: x@y\r\n\tz@y\r\nC: d\r\n\r\nbcc: corps": "A: b\r\nC: d\r\n\r\nbcc: corps",
		"A: b\nBCC: x@y\n\ncorps":                             "A: b\n\ncorps",
		"sans corps":                                          "sans corps",
	} {
		if got := string(withoutBcc([]byte(in))); got != want {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

func TestSubmit_SuspendedWorkspaceDoesNotSend(t *testing.T) {
	f, s := bench()
	f.acc.ReadOnly = true
	var se *smtp.SMTPError
	if err := s.CheckRecipient(context.Background(), Prefix+"a1", "x@y.fr"); !errors.As(err, &se) || se.Code != 550 {
		t.Fatalf("RCPT : %v", err)
	}
	if err := s.Submit(context.Background(), Prefix+"a1", "jc@boite.fr", []string{"x@y.fr"}, []byte("From: jc@boite.fr\r\nTo: x@y.fr\r\nSubject: a\r\n\r\nb")); !errors.As(err, &se) || se.Code != 550 {
		t.Fatalf("DATA : %v", err)
	}
	if len(f.sent) != 0 || f.reserved != 0 {
		t.Fatal("rien ne part d'un espace suspendu")
	}
}

// La borne globale ne compte que les échecs : des milliers de logins valides venus d'autres adresses ne ferment pas la porte,
// et un déluge d'échecs ne fait pas refuser un bon mot de passe (la borne protège la base, pas le secret).
func TestLogin_GlobalLimitNotExhaustedByForeignBuckets(t *testing.T) {
	f, s := bench()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy(LimitLogin, LimitLoginPerMinute, time.Minute)
	t.Cleanup(rl.Stop)
	s.d.Limiter = rl
	slept := 0
	s.sleep = func(time.Duration) { slept++ }
	ctx := context.Background()
	for i := 0; i < LimitLoginPerMinute+50; i++ {
		if _, err := s.Login(ctx, "jc@boite.fr", "x"); err != nil {
			t.Fatalf("login valide %d refusé : %v", i, err)
		}
	}
	if slept != 0 {
		t.Fatal("des succès ne doivent rien ralentir")
	}
	f.verify = boxaccess.ErrAuth
	for i := 0; i < LimitLoginPerMinute+50; i++ {
		if _, err := s.Login(ctx, "jc@boite.fr", "faux"); !errors.Is(err, boxaccess.ErrAuth) {
			t.Fatalf("mauvais mot de passe %d : %v", i, err)
		}
	}
	if slept == 0 {
		t.Fatal("au-delà du seuil global, les échecs doivent être ralentis")
	}
	f.verify = nil
	if _, err := s.Login(ctx, "jc@boite.fr", "x"); err != nil {
		t.Fatalf("bon mot de passe refusé après un déluge d'échecs : %v", err)
	}
}
