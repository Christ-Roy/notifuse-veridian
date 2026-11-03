package smtprelay

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/emersion/go-smtp"
)

type fakeDirectory struct {
	ids    []string
	idsErr error
	ws     *domain.Workspace
}

func (f *fakeDirectory) UserWorkspaceIDs(ctx context.Context, userID string) ([]string, error) {
	return f.ids, f.idsErr
}

func (f *fakeDirectory) Workspace(ctx context.Context, workspaceID string) (*domain.Workspace, error) {
	return f.ws, nil
}

type fakeSuppression struct {
	statuses map[string][]domain.ContactListStatus
	err      error
}

func (f *fakeSuppression) ContactListStatuses(ctx context.Context, workspaceID, email string) ([]domain.ContactListStatus, error) {
	return f.statuses[email], f.err
}

type fakeMailer struct {
	reqs []domain.SendEmailProviderRequest
	fail bool
}

func (f *fakeMailer) SendEmail(ctx context.Context, req domain.SendEmailProviderRequest, isMarketing bool) error {
	if f.fail {
		return errors.New("smtp profile down")
	}
	f.reqs = append(f.reqs, req)
	return nil
}

type fakeHistory struct {
	rows []*domain.MessageHistory
}

func (f *fakeHistory) Create(ctx context.Context, workspaceID, secretKey string, m *domain.MessageHistory) error {
	f.rows = append(f.rows, m)
	return nil
}

type fakeLimiter struct{ allow bool }

func (f fakeLimiter) Allow(namespace, key string) bool { return f.allow }

func newWorkspace(reserved bool) *domain.Workspace {
	ws := &domain.Workspace{
		ID: "ws1",
		Settings: domain.WorkspaceSettings{
			SecretKey: "secret",
		},
		Integrations: domain.Integrations{{
			ID:   "p1",
			Name: "profil transactionnel",
			Type: domain.IntegrationTypeEmail,
			EmailProvider: domain.EmailProvider{
				Kind:    domain.EmailProviderKindSMTP,
				Senders: []domain.EmailSender{{ID: "s1", Email: "noreply@client.example", Name: "Client", IsDefault: true}},
			},
		}},
	}
	if reserved {
		ws.Settings.TransactionalEmailProviderID = "p1"
	}
	return ws
}

func newRelay(dir *fakeDirectory, sup *fakeSuppression, m *fakeMailer, h *fakeHistory, allow bool) *Relay {
	return New(Deps{
		Directory:   dir,
		Suppression: sup,
		Mailer:      m,
		History:     h,
		Limiter:     fakeLimiter{allow: allow},
		Logger:      logger.NewLogger(),
	})
}

func plainMail(from, msgID, subject, body string) []byte {
	return []byte("From: " + from + "\r\n" +
		"To: bob@example.org\r\n" +
		"Subject: " + subject + "\r\n" +
		"Message-ID: <" + msgID + ">\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		body + "\r\n")
}

func asSMTP(t *testing.T, err error) *smtp.SMTPError {
	t.Helper()
	var se *smtp.SMTPError
	if !errors.As(err, &se) {
		t.Fatalf("erreur SMTP attendue, got %T %v", err, err)
	}
	return se
}

func TestNew(t *testing.T) {
	r := New(Deps{Logger: logger.NewLogger()})
	if r == nil || r.dedupe == nil || r.deps.Now == nil {
		t.Fatalf("New doit poser dedupe et l'horloge par defaut: %+v", r)
	}
}

func TestRelay_Submit_Accepts(t *testing.T) {
	m := &fakeMailer{}
	h := &fakeHistory{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, &fakeSuppression{}, m, h, true)

	err := r.Submit(context.Background(), "key-user", "noreply@client.example", []string{"bob@example.org"},
		plainMail("noreply@client.example", "m1@client.example", "Bonjour", "Corps"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(m.reqs) != 1 {
		t.Fatalf("un envoi attendu, got %d", len(m.reqs))
	}
	req := m.reqs[0]
	if req.IntegrationID != "p1" || req.WorkspaceID != "ws1" || req.Provider == nil {
		t.Fatalf("mauvais profil ou workspace: %+v", req)
	}
	if req.MessageID != "m1" {
		t.Fatalf("le premier destinataire doit garder la partie locale du Message-ID client (le domaine vient de l'expéditeur), got %q", req.MessageID)
	}
	if req.Subject != "Bonjour" || req.To != "bob@example.org" || req.FromAddress != "noreply@client.example" {
		t.Fatalf("champs d'envoi faux: %+v", req)
	}
	if len(h.rows) != 1 || h.rows[0].TemplateID != RelayTemplateID || h.rows[0].VeridianMessageType != "transactional" {
		t.Fatalf("historique attendu (template relay, type transactionnel), got %+v", h.rows)
	}
}

func TestRelay_Submit_WrongHeaderFrom(t *testing.T) {
	m := &fakeMailer{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, &fakeSuppression{}, m, &fakeHistory{}, true)
	err := r.Submit(context.Background(), "u", "", []string{"bob@example.org"},
		plainMail("ceo@autre.example", "m2", "x", "y"))
	if se := asSMTP(t, err); se.Code != 550 {
		t.Fatalf("From non autorise doit rendre 550, got %d", se.Code)
	}
	if len(m.reqs) != 0 {
		t.Fatal("aucun envoi attendu pour un From non autorise")
	}
}

func TestRelay_Submit_WrongEnvelopeFrom(t *testing.T) {
	m := &fakeMailer{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, &fakeSuppression{}, m, &fakeHistory{}, true)
	err := r.Submit(context.Background(), "u", "evil@autre.example", []string{"bob@example.org"},
		plainMail("noreply@client.example", "m3", "x", "y"))
	if se := asSMTP(t, err); se.Code != 550 {
		t.Fatalf("MAIL FROM non autorise doit rendre 550, got %d", se.Code)
	}
	if len(m.reqs) != 0 {
		t.Fatal("aucun envoi attendu pour une enveloppe non autorisee")
	}
}

func TestRelay_Submit_NoReservedProfile(t *testing.T) {
	m := &fakeMailer{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(false)}, &fakeSuppression{}, m, &fakeHistory{}, true)
	err := r.Submit(context.Background(), "u", "", []string{"bob@example.org"},
		plainMail("noreply@client.example", "m4", "x", "y"))
	if se := asSMTP(t, err); se.Code != 554 {
		t.Fatalf("sans profil reserve doit rendre 554, got %d", se.Code)
	}
	if len(m.reqs) != 0 {
		t.Fatal("aucun repli silencieux sur un autre profil")
	}
}

func TestRelay_Submit_MultiWorkspaceKey(t *testing.T) {
	m := &fakeMailer{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1", "ws2"}, ws: newWorkspace(true)}, &fakeSuppression{}, m, &fakeHistory{}, true)
	err := r.Submit(context.Background(), "u", "", []string{"bob@example.org"},
		plainMail("noreply@client.example", "m5", "x", "y"))
	if se := asSMTP(t, err); se.Code != 550 {
		t.Fatalf("cle multi-workspace doit rendre 550, got %d", se.Code)
	}
	if len(m.reqs) != 0 {
		t.Fatal("une cle multi-workspace ne doit rien envoyer")
	}
}

func TestRelay_Submit_TooManyRecipients(t *testing.T) {
	m := &fakeMailer{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, &fakeSuppression{}, m, &fakeHistory{}, true)
	to := make([]string, MaxRecipients+1)
	for i := range to {
		to[i] = "bob@example.org"
	}
	err := r.Submit(context.Background(), "u", "", to, plainMail("noreply@client.example", "m6", "x", "y"))
	if se := asSMTP(t, err); se.Code != 552 {
		t.Fatalf("trop de RCPT doit rendre 552, got %d", se.Code)
	}
	if len(m.reqs) != 0 {
		t.Fatal("aucun envoi au-dela de la limite de destinataires")
	}
}

func TestRelay_Submit_TooBig(t *testing.T) {
	m := &fakeMailer{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, &fakeSuppression{}, m, &fakeHistory{}, true)
	big := []byte(strings.Repeat("a", MaxMessageBytes+1))
	err := r.Submit(context.Background(), "u", "", []string{"bob@example.org"}, big)
	if se := asSMTP(t, err); se.Code != 552 {
		t.Fatalf("message trop gros doit rendre 552, got %d", se.Code)
	}
	if len(m.reqs) != 0 {
		t.Fatal("aucun envoi pour un message trop gros")
	}
}

func TestRelay_Submit_RateLimited(t *testing.T) {
	m := &fakeMailer{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, &fakeSuppression{}, m, &fakeHistory{}, false)
	err := r.Submit(context.Background(), "u", "", []string{"bob@example.org"},
		plainMail("noreply@client.example", "m7", "x", "y"))
	if se := asSMTP(t, err); se.Code != 451 {
		t.Fatalf("debit depasse doit rendre 451, got %d", se.Code)
	}
	if len(m.reqs) != 0 {
		t.Fatal("aucun envoi au-dela du debit")
	}
}

func TestRelay_Submit_SendFailureIsTemporary(t *testing.T) {
	m := &fakeMailer{fail: true}
	h := &fakeHistory{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, &fakeSuppression{}, m, h, true)
	err := r.Submit(context.Background(), "u", "", []string{"bob@example.org"},
		plainMail("noreply@client.example", "m8", "x", "y"))
	if se := asSMTP(t, err); se.Code != 451 {
		t.Fatalf("echec d'envoi doit rendre 451 (le client reessaie), got %d", se.Code)
	}
	if len(h.rows) != 0 {
		t.Fatal("aucune ligne d'historique pour un envoi refuse")
	}
}

func TestRelay_Submit_IdempotentRetry(t *testing.T) {
	m := &fakeMailer{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, &fakeSuppression{}, m, &fakeHistory{}, true)
	msg := plainMail("noreply@client.example", "m9@client.example", "x", "y")
	if err := r.Submit(context.Background(), "u", "", []string{"bob@example.org"}, msg); err != nil {
		t.Fatalf("premier Submit: %v", err)
	}
	if err := r.Submit(context.Background(), "u", "", []string{"bob@example.org"}, msg); err != nil {
		t.Fatalf("retry doit rendre 250, got %v", err)
	}
	if len(m.reqs) != 1 {
		t.Fatalf("un retry ne doit pas renvoyer le mail, got %d envois", len(m.reqs))
	}
}

// TestRelay_Submit_TwoRecipientsOneBounced : deux RCPT dont un bounced. Le garde RCPT
// refuse le premier en 550 5.7.1, le second passe, et seul le second est envoye.
func TestRelay_Submit_TwoRecipientsOneBounced(t *testing.T) {
	sup := &fakeSuppression{statuses: map[string][]domain.ContactListStatus{
		"bounced@example.org": {domain.ContactListStatusBounced},
	}}
	m := &fakeMailer{}
	r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, sup, m, &fakeHistory{}, true)

	var accepted []string
	var refusedCode int
	for _, rcpt := range []string{"bounced@example.org", "ok@example.org"} {
		if err := r.CheckRecipient(context.Background(), "u", rcpt); err != nil {
			refusedCode = asSMTP(t, err).Code
			continue
		}
		accepted = append(accepted, rcpt)
	}
	if refusedCode != 550 {
		t.Fatalf("le destinataire bounced doit etre refuse en 550, got %d", refusedCode)
	}
	if len(accepted) != 1 || accepted[0] != "ok@example.org" {
		t.Fatalf("seul ok@example.org doit passer, got %v", accepted)
	}
	if err := r.Submit(context.Background(), "u", "", accepted, plainMail("noreply@client.example", "m10", "x", "y")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(m.reqs) != 1 || m.reqs[0].To != "ok@example.org" {
		t.Fatalf("un seul envoi, vers ok@example.org, attendu: %+v", m.reqs)
	}
}

func TestRelay_CheckRecipient(t *testing.T) {
	sup := &fakeSuppression{statuses: map[string][]domain.ContactListStatus{
		"complained@example.org":   {domain.ContactListStatusComplained},
		"unsubscribed@example.org": {domain.ContactListStatusUnsubscribed},
	}}
	t.Run("complained refuse", func(t *testing.T) {
		r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, sup, &fakeMailer{}, &fakeHistory{}, true)
		if se := asSMTP(t, r.CheckRecipient(context.Background(), "u", "complained@example.org")); se.Code != 550 {
			t.Fatalf("complained doit rendre 550, got %d", se.Code)
		}
	})
	t.Run("unsubscribed ne bloque pas le transactionnel", func(t *testing.T) {
		r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, sup, &fakeMailer{}, &fakeHistory{}, true)
		if err := r.CheckRecipient(context.Background(), "u", "unsubscribed@example.org"); err != nil {
			t.Fatalf("unsubscribed ne doit pas bloquer: %v", err)
		}
	})
	t.Run("cle non scopee : le garde laisse passer, Submit tranche", func(t *testing.T) {
		r := newRelay(&fakeDirectory{ids: []string{"ws1", "ws2"}, ws: newWorkspace(true)}, sup, &fakeMailer{}, &fakeHistory{}, true)
		if err := r.CheckRecipient(context.Background(), "u", "complained@example.org"); err != nil {
			t.Fatalf("cle multi-workspace: le garde ne refuse pas : %v", err)
		}
	})
	t.Run("lecture de directory en erreur : 451", func(t *testing.T) {
		r := newRelay(&fakeDirectory{idsErr: errors.New("db down")}, sup, &fakeMailer{}, &fakeHistory{}, true)
		if se := asSMTP(t, r.CheckRecipient(context.Background(), "u", "x@example.org")); se.Code != 451 {
			t.Fatalf("erreur de lecture doit rendre 451, got %d", se.Code)
		}
	})
	t.Run("lecture de suppression en erreur : 451", func(t *testing.T) {
		r := newRelay(&fakeDirectory{ids: []string{"ws1"}, ws: newWorkspace(true)}, &fakeSuppression{err: errors.New("db down")}, &fakeMailer{}, &fakeHistory{}, true)
		if se := asSMTP(t, r.CheckRecipient(context.Background(), "u", "x@example.org")); se.Code != 451 {
			t.Fatalf("erreur de suppression doit rendre 451, got %d", se.Code)
		}
	})
}

func TestIsSuppressed(t *testing.T) {
	cases := []struct {
		name     string
		statuses []domain.ContactListStatus
		want     bool
	}{
		{"aucun statut", nil, false},
		{"actif", []domain.ContactListStatus{domain.ContactListStatusActive}, false},
		{"desinscrit marketing", []domain.ContactListStatus{domain.ContactListStatusUnsubscribed}, false},
		{"bounced", []domain.ContactListStatus{domain.ContactListStatusBounced}, true},
		{"complained", []domain.ContactListStatus{domain.ContactListStatusComplained}, true},
		{"actif puis bounced", []domain.ContactListStatus{domain.ContactListStatusActive, domain.ContactListStatusBounced}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsSuppressed(c.statuses); got != c.want {
				t.Fatalf("IsSuppressed = %v, attendu %v", got, c.want)
			}
		})
	}
}

// TestWireLocalPart : la partie locale est gardée, le domaine client est retiré.
func TestWireLocalPart(t *testing.T) {
	cases := map[string]string{
		"3fdf-uuid@agences-veridian.fr": "3fdf-uuid",
		"plain-id":                      "plain-id",
		"@seul":                         "@seul",
	}
	for in, want := range cases {
		if got := wireLocalPart(in); got != want {
			t.Fatalf("wireLocalPart(%q) = %q, attendu %q", in, got, want)
		}
	}
}
