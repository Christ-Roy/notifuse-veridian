package service

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- doubles ---------------------------------------------------------------

type fakeForwardRepo struct {
	mu       sync.Mutex
	ref      domain.VeridianSentMessageRef
	found    bool
	resolveE error
	claimE   error
	rows     map[string]domain.VeridianReplyForwardRecord
	dailyCap int
	finished []string // "status:errclass"
}

func newFakeForwardRepo() *fakeForwardRepo {
	return &fakeForwardRepo{
		ref:   domain.VeridianSentMessageRef{ProfileID: "prof1", SenderEmail: "pro@send.example"},
		found: true,
		rows:  map[string]domain.VeridianReplyForwardRecord{},
	}
}

func (r *fakeForwardRepo) ResolveSentMessage(_ context.Context, _, _, _ string) (domain.VeridianSentMessageRef, bool, error) {
	return r.ref, r.found, r.resolveE
}

func (r *fakeForwardRepo) Claim(_ context.Context, _ string, rec domain.VeridianReplyForwardRecord, dailyCap int, _ time.Time) (bool, bool, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimE != nil {
		return false, false, false, r.claimE
	}
	if _, ok := r.rows[rec.ReplyKey]; ok {
		return false, true, false, nil
	}
	n := 0
	for _, x := range r.rows {
		if x.ProfileID == rec.ProfileID {
			n++
		}
	}
	if n >= dailyCap {
		return false, false, true, nil
	}
	r.rows[rec.ReplyKey] = rec
	return true, false, false, nil
}

func (r *fakeForwardRepo) Finish(_ context.Context, _, key, status, errClass string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.rows[key]
	rec.Status = status
	rec.ErrorClass = errClass
	r.rows[key] = rec
	r.finished = append(r.finished, status+":"+errClass)
	return nil
}

func (r *fakeForwardRepo) Stats(context.Context, string, time.Time) ([]domain.VeridianReplyForwardStat, error) {
	return nil, nil
}

type fakeForwardWorkspaces struct {
	ws  *domain.Workspace
	err error
}

func (w *fakeForwardWorkspaces) GetByID(context.Context, string) (*domain.Workspace, error) {
	return w.ws, w.err
}

type fakeForwardMailer struct {
	mu       sync.Mutex
	sent     []domain.SendEmailProviderRequest
	failures int // nombre d'appels a faire echouer avant de reussir (-1 = toujours)
	calls    int
}

func (m *fakeForwardMailer) SendEmail(_ context.Context, req domain.SendEmailProviderRequest, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.failures < 0 || m.calls <= m.failures {
		return errors.New("535 auth failed for secret-user@leak.example")
	}
	m.sent = append(m.sent, req)
	return nil
}

func forwardWorkspace(recipients ...string) *domain.Workspace {
	return &domain.Workspace{
		ID: "ws1",
		Integrations: []domain.Integration{{
			ID:   "prof1",
			Type: domain.IntegrationTypeEmail,
			EmailProvider: domain.EmailProvider{
				Kind:                   domain.EmailProviderKindSMTP,
				Senders:                []domain.EmailSender{{ID: "s1", Email: "pro@send.example", Name: "Pro Sender", IsDefault: true}},
				VeridianReplyForwardTo: recipients,
			},
		}},
	}
}

func newForwarderForTest(t *testing.T, ws *domain.Workspace) (*VeridianReplyForwarder, *fakeForwardRepo, *fakeForwardMailer) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := newFakeForwardRepo()
	mailer := &fakeForwardMailer{}
	f := NewVeridianReplyForwarder(repo, &fakeForwardWorkspaces{ws: ws}, mailer, setupMockLogger(ctrl))
	f.run = func(fn func()) { fn() }
	f.retryDelay = time.Millisecond
	f.now = func() time.Time { return time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC) }
	return f, repo, mailer
}

const humanRaw = "From: Jean Dupont <jean@acme.fr>\r\n" +
	"To: pro@send.example\r\n" +
	"Subject: Re: Votre proposition de site\r\n" +
	"Message-ID: <reply-1@acme.fr>\r\n" +
	"In-Reply-To: <sent-uuid-1@send.example>\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Oui, je suis intéressé, appelez-moi.\r\n\r\nLe lundi, Pro a écrit :\r\n> Bonjour, je vous propose un site.\r\n"

func humanMsg(raw string) (*domain.VeridianIMAPMessage, domain.VeridianReplyDetection) {
	msg := &domain.VeridianIMAPMessage{
		UID: 7, UIDValidity: 1, Folder: "INBOX", WorkspaceID: "ws1", IntegrationID: "imap1",
		MessageID: "<reply-1@acme.fr>", InReplyTo: "<sent-uuid-1@send.example>",
		From: "jean@acme.fr", To: []string{"pro@send.example"}, Subject: "Re: Votre proposition de site",
		RawBody: []byte(raw),
	}
	// Le sujet de l'enveloppe suit celui du message brut (comme le fait le poller IMAP).
	for _, line := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			msg.Subject = strings.TrimPrefix(line, "Subject: ")
		}
		if strings.HasPrefix(line, "From: ") {
			from := strings.TrimPrefix(line, "From: ")
			if i := strings.Index(from, "<"); i >= 0 && strings.HasSuffix(from, ">") {
				from = from[i+1 : len(from)-1]
			}
			msg.From = from
		}
	}
	det := domain.VeridianReplyDetection{
		IsReply: true, MatchType: domain.VeridianReplyMatchMessageID, ContactEmail: "jean@acme.fr",
		MatchedMessageID: "sent-uuid-1", ReplyType: domain.VeridianClassifyReplyType(msg),
	}
	return msg, det
}

// --- copie envoyee ----------------------------------------------------------

func TestReplyForward_HumanReply_SendsOneCopy(t *testing.T) {
	f, repo, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(humanRaw)

	out := f.Forward(context.Background(), "ws1", msg, det)
	require.Equal(t, ReplyForwardSent, out)
	require.Len(t, mailer.sent, 1)

	req := mailer.sent[0]
	assert.Equal(t, "client@client.fr", req.To)
	assert.Equal(t, "pro@send.example", req.FromAddress, "expediteur = identite d'envoi du profil")
	assert.Equal(t, "Pro Sender", req.FromName)
	assert.Equal(t, "jean@acme.fr", req.EmailOptions.ReplyTo, "Reply-To = le prospect")
	assert.Equal(t, "Réponse de Jean Dupont : Votre proposition de site", req.Subject)
	assert.Equal(t, "prof1", req.IntegrationID)
	assert.True(t, req.PlainTextOnly)
	assert.Equal(t, map[string]string{"X-Veridian-Forwarded": "1"}, req.VeridianExtraHeaders)
	// corps = la reponse, citation conservee
	assert.Contains(t, req.TextContent, "Oui, je suis intéressé, appelez-moi.")
	assert.Contains(t, req.TextContent, "> Bonjour, je vous propose un site.")
	// le garde-fou « libelle de gabarit residuel » du transport ne doit pas refuser ce corps
	assert.Empty(t, veridianTemplateLabelLeak(req.TextContent))
	// pas de tiret cadratin entoure d'espaces dans ce que nous composons
	assert.NotContains(t, req.TextContent[:strings.Index(req.TextContent, "------")], " — ")

	// journal : une ligne, statut sent
	require.Len(t, repo.rows, 1)
	assert.Equal(t, []string{"sent:"}, repo.finished)
	for _, rec := range repo.rows {
		assert.Equal(t, "prof1", rec.ProfileID)
		assert.Equal(t, 1, rec.Recipients)
	}
}

func TestReplyForward_MultipleRecipients_OneMessageToPlusCc(t *testing.T) {
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("a@client.fr", "b@client.fr"))
	msg, det := humanMsg(humanRaw)
	require.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg, det))
	require.Len(t, mailer.sent, 1)
	assert.Equal(t, "a@client.fr", mailer.sent[0].To)
	assert.Equal(t, []string{"b@client.fr"}, mailer.sent[0].EmailOptions.CC)
}

func TestReplyForward_ReplyToHeaderOfProspectIsUsed(t *testing.T) {
	raw := strings.Replace(humanRaw, "To: pro@send.example\r\n", "To: pro@send.example\r\nReply-To: Jean Direct <jean.direct@acme.fr>\r\n", 1)
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(raw)
	require.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg, det))
	assert.Equal(t, "jean.direct@acme.fr", mailer.sent[0].EmailOptions.ReplyTo)
}

func TestReplyForward_HTMLOnlyReplyIsConvertedToText(t *testing.T) {
	raw := "From: Jean <jean@acme.fr>\r\nSubject: Re: Devis\r\nMessage-ID: <r2@acme.fr>\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<html><body><p>Merci, envoyez le devis.</p></body></html>\r\n"
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(raw)
	require.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg, det))
	assert.Contains(t, mailer.sent[0].TextContent, "Merci, envoyez le devis.")
	assert.NotContains(t, mailer.sent[0].TextContent, "<p>")
}

func TestReplyForward_SubjectAndNameCannotInjectHeaders(t *testing.T) {
	raw := "From: \"Eve\r\n Bcc: x@evil.example\" <jean@acme.fr>\r\nSubject: Re: Hello\r\nMessage-ID: <r3@acme.fr>\r\n\r\ntexte\r\n"
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(raw)
	msg.From = "jean@acme.fr"
	f.Forward(context.Background(), "ws1", msg, det)
	require.Len(t, mailer.sent, 1)
	assert.NotContains(t, mailer.sent[0].Subject, "\n")
	assert.NotContains(t, mailer.sent[0].Subject, "\r")
}

// --- config vide : comportement inchange ----------------------------------

func TestReplyForward_EmptyConfig_DoesNothing(t *testing.T) {
	f, repo, mailer := newForwarderForTest(t, forwardWorkspace())
	msg, det := humanMsg(humanRaw)
	assert.Equal(t, ReplyForwardSkipNoConfig, f.Forward(context.Background(), "ws1", msg, det))
	assert.Empty(t, mailer.sent)
	assert.Empty(t, repo.rows, "aucune ligne de journal quand rien n'est configure")
}

func TestReplyForward_NoSentMessageForReply_DoesNothing(t *testing.T) {
	f, repo, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	repo.found = false
	msg, det := humanMsg(humanRaw)
	assert.Equal(t, ReplyForwardSkipNoProfile, f.Forward(context.Background(), "ws1", msg, det))
	assert.Empty(t, mailer.sent)
}

func TestReplyForward_OtherProfileWithoutConfig_DoesNothing(t *testing.T) {
	// L'envoi vient de prof2, qui n'a rien configure ; prof1 a une redirection : pas de copie.
	ws := forwardWorkspace("client@client.fr")
	ws.Integrations = append(ws.Integrations, domain.Integration{ID: "prof2", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}})
	f, repo, mailer := newForwarderForTest(t, ws)
	repo.ref.ProfileID = "prof2"
	msg, det := humanMsg(humanRaw)
	assert.Equal(t, ReplyForwardSkipNoConfig, f.Forward(context.Background(), "ws1", msg, det))
	assert.Empty(t, mailer.sent)
}

// --- garde-fous : jamais pour rebond / auto-reponse / boucle -----------------

func TestReplyForward_NeverForBouncesOrAutoReplies(t *testing.T) {
	cases := map[string]string{
		"auto-submitted":  "From: Jean <jean@acme.fr>\r\nSubject: Re: Hello\r\nAuto-Submitted: auto-replied\r\nMessage-ID: <a1@acme.fr>\r\n\r\nJe suis absent.\r\n",
		"precedence":      "From: Jean <jean@acme.fr>\r\nSubject: Re: Hello\r\nPrecedence: auto_reply\r\nMessage-ID: <a2@acme.fr>\r\n\r\nAbsent.\r\n",
		"subject-absence": "From: Jean <jean@acme.fr>\r\nSubject: Réponse automatique : Hello\r\nMessage-ID: <a3@acme.fr>\r\n\r\nAbsent jusqu'au 10.\r\n",
		"mailer-daemon":   "From: MAILER-DAEMON@acme.fr\r\nSubject: Undelivered Mail Returned to Sender\r\nMessage-ID: <a4@acme.fr>\r\n\r\nDelivery failed.\r\n",
		"dsn":             "From: postmaster@acme.fr\r\nSubject: Delivery Status Notification (Failure)\r\nContent-Type: multipart/report; report-type=delivery-status; boundary=B\r\nMessage-ID: <a5@acme.fr>\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\nFailed\r\n--B\r\nContent-Type: message/delivery-status\r\n\r\nFinal-Recipient: rfc822; x@acme.fr\r\nAction: failed\r\nStatus: 5.1.1\r\nDiagnostic-Code: smtp; 550 5.1.1 user unknown\r\n\r\n--B--\r\n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			f, repo, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
			msg, _ := humanMsg(raw)
			// Meme si l'appelant se trompait et annoncait « humain », le forwarder re-verifie.
			det := domain.VeridianReplyDetection{IsReply: true, ContactEmail: "jean@acme.fr", MatchedMessageID: "sent-uuid-1", ReplyType: domain.VeridianReplyTypeHuman}
			out := f.Forward(context.Background(), "ws1", msg, det)
			assert.Equal(t, ReplyForwardSkipNotHuman, out)
			assert.Empty(t, mailer.sent)
			assert.Empty(t, repo.rows)
		})
	}
}

func TestReplyForward_NeverWhenDetectionSaysAutoOrChallenge(t *testing.T) {
	for _, rt := range []domain.VeridianReplyType{domain.VeridianReplyTypeAuto, domain.VeridianReplyTypeChallenge} {
		f, _, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
		msg, det := humanMsg(humanRaw)
		det.ReplyType = rt
		assert.Equal(t, ReplyForwardSkipNotHuman, f.Forward(context.Background(), "ws1", msg, det))
		assert.Empty(t, mailer.sent)
	}
}

func TestReplyForward_NeverToSenderAddress_Loop(t *testing.T) {
	// Le message entrant vient de l'adresse d'envoi elle-meme (le fil tourne sur lui-meme).
	raw := strings.Replace(humanRaw, "From: Jean Dupont <jean@acme.fr>", "From: Pro <pro@send.example>", 1)
	f, repo, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(raw)
	msg.From = "pro@send.example"
	assert.Equal(t, ReplyForwardSkipLoop, f.Forward(context.Background(), "ws1", msg, det))
	assert.Empty(t, mailer.sent)
	assert.Empty(t, repo.rows)
}

func TestReplyForward_NeverWhenAlreadyForwardedHeader(t *testing.T) {
	raw := strings.Replace(humanRaw, "To: pro@send.example\r\n", "To: pro@send.example\r\nX-Veridian-Forwarded: 1\r\n", 1)
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(raw)
	assert.Equal(t, ReplyForwardSkipLoop, f.Forward(context.Background(), "ws1", msg, det))
	assert.Empty(t, mailer.sent)
}

func TestReplyForward_NeverWhenSenderIsOneOfTheRecipients(t *testing.T) {
	// Le client repond depuis son adresse : ce n'est pas une reponse de prospect a recopier.
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(strings.Replace(humanRaw, "jean@acme.fr", "client@client.fr", 1))
	msg.From = "client@client.fr"
	assert.Equal(t, ReplyForwardSkipLoop, f.Forward(context.Background(), "ws1", msg, det))
	assert.Empty(t, mailer.sent)
}

func TestReplyForward_NeverToTheProspect(t *testing.T) {
	// A. Le prospect figure dans la liste (ecrite a la main) : aucune copie ne part, personne
	//    ne recoit un mail qui le citerait lui-meme (boucle).
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("jean@acme.fr", "client@client.fr"))
	msg, det := humanMsg(humanRaw)
	assert.Equal(t, ReplyForwardSkipLoop, f.Forward(context.Background(), "ws1", msg, det))
	assert.Empty(t, mailer.sent)

	// B. Un collegue du prospect repond depuis une autre adresse : le contact d'origine est
	//    retire de la liste, le client recoit la copie.
	raw := strings.Replace(humanRaw, "From: Jean Dupont <jean@acme.fr>", "From: Marie <marie@acme.fr>", 1)
	f2, _, mailer2 := newForwarderForTest(t, forwardWorkspace("jean@acme.fr", "client@client.fr"))
	msg2, det2 := humanMsg(raw)
	msg2.From = "marie@acme.fr"
	require.Equal(t, ReplyForwardSent, f2.Forward(context.Background(), "ws1", msg2, det2))
	assert.Equal(t, "client@client.fr", mailer2.sent[0].To)
	assert.Empty(t, mailer2.sent[0].EmailOptions.CC)
	assert.Equal(t, "marie@acme.fr", mailer2.sent[0].EmailOptions.ReplyTo)

	// C. La liste se reduit au seul contact d'origine : rien ne part.
	f3, repo3, mailer3 := newForwarderForTest(t, forwardWorkspace("jean@acme.fr"))
	assert.Equal(t, ReplyForwardSkipNoRecipent, f3.Forward(context.Background(), "ws1", msg2, det2))
	assert.Empty(t, mailer3.sent)
	assert.Empty(t, repo3.rows)
}

func TestReplyForward_StoredSenderAddressInListIsIgnored(t *testing.T) {
	// Blob ecrit hors API avec l'adresse d'envoi : elle est ecartee a l'execution.
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("pro@send.example", "client@client.fr"))
	msg, det := humanMsg(humanRaw)
	require.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg, det))
	assert.Equal(t, "client@client.fr", mailer.sent[0].To)
	assert.Empty(t, mailer.sent[0].EmailOptions.CC)
}

// --- idempotence et plafond ------------------------------------------------

func TestReplyForward_Idempotent_SameReplyReadTwice(t *testing.T) {
	f, repo, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(humanRaw)
	assert.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg, det))
	assert.Equal(t, ReplyForwardSkipDuplicate, f.Forward(context.Background(), "ws1", msg, det))
	assert.Equal(t, ReplyForwardSkipDuplicate, f.Forward(context.Background(), "ws1", msg, det))
	assert.Len(t, mailer.sent, 1, "une reponse = une copie")
	assert.Len(t, repo.rows, 1)
}

func TestReplyForward_TwoDifferentRepliesFromSameProspect_TwoCopies(t *testing.T) {
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(humanRaw)
	msg2, det2 := humanMsg(strings.Replace(humanRaw, "reply-1@acme.fr", "reply-2@acme.fr", 1))
	msg2.MessageID = "<reply-2@acme.fr>"
	msg2.UID = 8
	assert.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg, det))
	assert.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg2, det2))
	assert.Len(t, mailer.sent, 2)
}

func TestReplyForward_KeyFallsBackToIMAPCoordinatesWithoutMessageID(t *testing.T) {
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(humanRaw)
	msg.MessageID = ""
	assert.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg, det))
	assert.Equal(t, ReplyForwardSkipDuplicate, f.Forward(context.Background(), "ws1", msg, det))
	msg.UID = 99
	assert.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg, det))
	assert.Len(t, mailer.sent, 2)
}

func TestReplyForward_DailyCapPerProfile(t *testing.T) {
	f, repo, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	f.dailyCap = 3
	for i := 0; i < 5; i++ {
		msg, det := humanMsg(humanRaw)
		msg.MessageID = "<reply-" + string(rune('a'+i)) + "@acme.fr>"
		out := f.Forward(context.Background(), "ws1", msg, det)
		if i < 3 {
			assert.Equal(t, ReplyForwardSent, out, "i=%d", i)
		} else {
			assert.Equal(t, ReplyForwardSkipCapped, out, "i=%d", i)
		}
	}
	assert.Len(t, mailer.sent, 3, "boucle d'envoi bornee par le plafond")
	assert.Len(t, repo.rows, 3)
}

// --- echec d'envoi : journalise, jamais bloquant ------------------------------

func TestReplyForward_SendFailure_RetriedOnceThenRecordedWithoutPersonalData(t *testing.T) {
	f, repo, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	mailer.failures = -1
	msg, det := humanMsg(humanRaw)
	out := f.Forward(context.Background(), "ws1", msg, det)
	assert.Equal(t, ReplyForwardFailed, out)
	assert.Equal(t, 2, mailer.calls, "un essai + une relance")
	assert.Equal(t, []string{"failed:auth"}, repo.finished, "classe grossiere, jamais le texte brut de l'erreur SMTP")
	for _, rec := range repo.rows {
		assert.NotContains(t, rec.ErrorClass, "@")
	}
}

func TestReplyForward_SendFailureThenSuccessOnRetry(t *testing.T) {
	f, _, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	mailer.failures = 1
	msg, det := humanMsg(humanRaw)
	assert.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg, det))
	assert.Len(t, mailer.sent, 1)
}

func TestReplyForward_InfrastructureErrorsNeverPanicNorPropagate(t *testing.T) {
	f, repo, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	msg, det := humanMsg(humanRaw)

	repo.resolveE = errors.New("db down")
	assert.Equal(t, ReplyForwardError, f.Forward(context.Background(), "ws1", msg, det))
	repo.resolveE = nil

	repo.claimE = errors.New("db down")
	assert.Equal(t, ReplyForwardError, f.Forward(context.Background(), "ws1", msg, det))
	repo.claimE = nil

	f.workspaces = &fakeForwardWorkspaces{err: errors.New("db down")}
	assert.Equal(t, ReplyForwardError, f.Forward(context.Background(), "ws1", msg, det))
	assert.Empty(t, mailer.sent)
}

func TestReplyForward_Dispatch_ReturnsImmediatelyEvenIfSendBlocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := newFakeForwardRepo()
	release := make(chan struct{})
	done := make(chan struct{})
	mailer := &blockingForwardMailer{release: release, done: done}
	f := NewVeridianReplyForwarder(repo, &fakeForwardWorkspaces{ws: forwardWorkspace("client@client.fr")}, mailer, setupMockLogger(ctrl))
	msg, det := humanMsg(humanRaw)

	start := time.Now()
	f.Dispatch("ws1", msg, det) // run par defaut : goroutine
	assert.Less(t, time.Since(start), 500*time.Millisecond, "Dispatch ne doit pas attendre l'envoi")
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("la copie n'est jamais partie")
	}
}

type blockingForwardMailer struct {
	release chan struct{}
	done    chan struct{}
}

func (m *blockingForwardMailer) SendEmail(context.Context, domain.SendEmailProviderRequest, bool) error {
	<-m.release
	close(m.done)
	return nil
}

func TestReplyForward_Dispatch_NilForwarderAndPanicAreSafe(t *testing.T) {
	var nilF *VeridianReplyForwarder
	msg, det := humanMsg(humanRaw)
	assert.NotPanics(t, func() { nilF.Dispatch("ws1", msg, det) })

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	f := NewVeridianReplyForwarder(newFakeForwardRepo(), &fakeForwardWorkspaces{ws: forwardWorkspace("client@client.fr")}, panicMailer{}, setupMockLogger(ctrl))
	f.run = func(fn func()) { fn() }
	assert.NotPanics(t, func() { f.Dispatch("ws1", msg, det) })
}

type panicMailer struct{}

func (panicMailer) SendEmail(context.Context, domain.SendEmailProviderRequest, bool) error {
	panic("boom")
}

// --- branchement dans le service de detection --------------------------------

type recordingDispatcher struct {
	calls []domain.VeridianReplyDetection
}

func (r *recordingDispatcher) Dispatch(_ string, _ *domain.VeridianIMAPMessage, det domain.VeridianReplyDetection) {
	r.calls = append(r.calls, det)
}

func TestReplyService_HumanReply_DispatchesAfterExit_AutoReplyDoesNot(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()
	disp := &recordingDispatcher{}
	m.svc.SetReplyForwarder(disp)

	m.messageRepo.EXPECT().FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-1").Return("prospect@acme.fr", true, nil).AnyTimes()
	m.replyRepo.EXPECT().HasReplied(gomock.Any(), replyWS, "prospect@acme.fr").Return(false, nil).AnyTimes()
	m.replyRepo.EXPECT().MarkReplied(gomock.Any(), replyWS, gomock.Any()).Return(nil).AnyTimes()
	m.timelineRepo.EXPECT().Create(gomock.Any(), replyWS, gomock.Any()).Return(nil).AnyTimes()
	m.autoRepo.EXPECT().ListContactAutomations(gomock.Any(), replyWS, gomock.Any()).Return(nil, 0, nil).AnyTimes()

	human := &domain.VeridianIMAPMessage{WorkspaceID: replyWS, From: "prospect@acme.fr", Subject: "Re: x", InReplyTo: "<sent-uuid-1@send.example>",
		RawBody: []byte("From: prospect@acme.fr\r\nSubject: Re: x\r\n\r\nOui.\r\n")}
	require.NoError(t, m.svc.ProcessInboundMessage(context.Background(), human))
	require.Len(t, disp.calls, 1)
	assert.Equal(t, "prospect@acme.fr", disp.calls[0].ContactEmail)

	auto := &domain.VeridianIMAPMessage{WorkspaceID: replyWS, From: "prospect@acme.fr", Subject: "Re: x", InReplyTo: "<sent-uuid-1@send.example>",
		RawBody: []byte("From: prospect@acme.fr\r\nSubject: Re: x\r\nAuto-Submitted: auto-replied\r\n\r\nAbsent.\r\n")}
	require.NoError(t, m.svc.ProcessInboundMessage(context.Background(), auto))
	assert.Len(t, disp.calls, 1, "une reponse automatique n'est jamais confiee au forwarder")
}

func TestReplyService_SecondReplyOfAlreadyRepliedContactIsStillDispatched(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()
	disp := &recordingDispatcher{}
	m.svc.SetReplyForwarder(disp)
	m.messageRepo.EXPECT().FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-1").Return("prospect@acme.fr", true, nil)
	m.replyRepo.EXPECT().HasReplied(gomock.Any(), replyWS, "prospect@acme.fr").Return(true, nil)

	msg := &domain.VeridianIMAPMessage{WorkspaceID: replyWS, From: "prospect@acme.fr", Subject: "Re: x", InReplyTo: "<sent-uuid-1@send.example>"}
	require.NoError(t, m.svc.ProcessInboundMessage(context.Background(), msg))
	assert.Len(t, disp.calls, 1, "dedoublonnage par message dans le forwarder, pas par contact")
}

func TestReplyService_WithoutForwarder_BehaviourUnchanged(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()
	// Aucun SetReplyForwarder : le service doit se comporter exactement comme avant.
	m.messageRepo.EXPECT().FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-1").Return("prospect@acme.fr", true, nil)
	m.replyRepo.EXPECT().HasReplied(gomock.Any(), replyWS, "prospect@acme.fr").Return(true, nil)
	msg := &domain.VeridianIMAPMessage{WorkspaceID: replyWS, From: "prospect@acme.fr", InReplyTo: "<sent-uuid-1@send.example>"}
	assert.NoError(t, m.svc.ProcessInboundMessage(context.Background(), msg))
}

var _ = mocks.NewMockMessageHistoryRepository

// smtpMailer envoie pour de bon par le transport SMTP du fork vers un faux serveur local.
type smtpMailer struct{ svc *SMTPService }

func (m smtpMailer) SendEmail(ctx context.Context, req domain.SendEmailProviderRequest, _ bool) error {
	return m.svc.SendEmail(ctx, req)
}

// Bout en bout local (sink SMTP, aucune adresse reelle) : de la reponse IMAP brute au mail recu
// par le serveur : en-tetes, Reply-To, objet decode, corps avec citation.
func TestReplyForward_EndToEnd_RealSMTPTransportToLocalSink(t *testing.T) {
	server := newMockSMTPServer(t, true)
	defer server.Close()

	ws := forwardWorkspace("client@client.test")
	ws.Integrations[0].EmailProvider.SMTP = &domain.SMTPSettings{Host: "127.0.0.1", Port: server.Port(), UseTLS: false}
	f, _, _ := newForwarderForTest(t, ws)
	f.mailer = smtpMailer{svc: NewSMTPService(&noopLogger{})}

	msg, det := humanMsg(humanRaw)
	require.Equal(t, ReplyForwardSent, f.Forward(context.Background(), "ws1", msg, det))

	messages := server.GetMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, "pro@send.example", messages[0].from)
	parsed, err := mail.ReadMessage(strings.NewReader(strings.ReplaceAll(string(messages[0].data), "Subject:\r\n", "Subject: ")))
	require.NoError(t, err)

	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	require.NoError(t, err)
	assert.Equal(t, "Réponse de Jean Dupont : Votre proposition de site", subject)
	assert.Equal(t, "1", parsed.Header.Get("X-Veridian-Forwarded"))
	rt, err := mail.ParseAddress(parsed.Header.Get("Reply-To"))
	require.NoError(t, err)
	assert.Equal(t, "jean@acme.fr", rt.Address)
	to, err := mail.ParseAddress(parsed.Header.Get("To"))
	require.NoError(t, err)
	assert.Equal(t, "client@client.test", to.Address)
	from, err := mail.ParseAddress(parsed.Header.Get("From"))
	require.NoError(t, err)
	assert.Equal(t, "pro@send.example", from.Address)

	// corps : texte brut (quoted-printable ou 8bit selon le transport), citation conservee
	var body []byte
	if strings.EqualFold(parsed.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
		body, err = io.ReadAll(quotedprintable.NewReader(parsed.Body))
	} else {
		body, err = io.ReadAll(parsed.Body)
	}
	require.NoError(t, err)
	assert.Contains(t, string(body), "Oui, je suis int")
	assert.Contains(t, string(body), "> Bonjour, je vous propose un site.")
	assert.Equal(t, 1, strings.Count(string(messages[0].data), "X-Veridian-Forwarded"))
}
