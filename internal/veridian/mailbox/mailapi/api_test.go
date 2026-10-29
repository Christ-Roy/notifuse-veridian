package mailapi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox/mailboxtest"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const (
	ws    = "ws1"
	j0id  = "11111111-1111-4111-8111-111111111111"
	j0ref = "<" + j0id + "@animalservicedistribution.fr>"
)

// ---- doubles ----

type grant struct{ Resource, Perm string }

type fakeAuth struct {
	mu    sync.Mutex
	calls []grant
	deny  map[grant]error // refus par (ressource, permission)
	err   error           // refus global
}

func (f *fakeAuth) Authorize(ctx context.Context, _ string, res domain.PermissionResource, perm domain.PermissionType) (context.Context, Actor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := grant{string(res), string(perm)}
	f.calls = append(f.calls, g)
	if f.err != nil {
		return ctx, Actor{}, f.err
	}
	if e, ok := f.deny[g]; ok {
		return ctx, Actor{}, e
	}
	return ctx, Actor{ID: "key-1", Kind: "agent"}, nil
}

func (f *fakeAuth) granted() []grant {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]grant(nil), f.calls...)
}

type fakeWS struct{ w *domain.Workspace }

func (f fakeWS) GetByID(context.Context, string) (*domain.Workspace, error) { return f.w, nil }

type fakeQueue struct {
	mu      sync.Mutex
	entries []*domain.EmailQueueEntry
	err     error
}

func (q *fakeQueue) Enqueue(_ context.Context, _ string, e []*domain.EmailQueueEntry) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	q.entries = append(q.entries, e...)
	return nil
}

func testWorkspace() *domain.Workspace {
	return &domain.Workspace{ID: ws, Integrations: []domain.Integration{
		{ID: "prof1", Name: "prospection-asd-2", Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{
			Kind: domain.EmailProviderKindSMTP, RateLimitPerMinute: 60, VeridianReturnIMAPIntegrationID: "imap1",
			Senders: []domain.EmailSender{{ID: "s1", Email: "hugo.bourdat@animalservicedistribution.fr", Name: "Hugo Bourdat", IsDefault: true}}}},
		{ID: "paused", Name: "en pause", Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{
			Kind: domain.EmailProviderKindSMTP, VeridianPaused: true, Senders: []domain.EmailSender{{Email: "x@d.fr", IsDefault: true}}}},
		{ID: "imap1", Name: "relais", Type: domain.IntegrationTypeIMAP, IMAPSettings: &domain.IMAPSettings{Host: "h", Port: 993, Username: "hugo.bourdat@animalservicedistribution.fr", Folder: "INBOX"}},
	}}
}

type env struct {
	srv   *httptest.Server
	svc   *Service
	store *mailbox.Store
	ing   *mailbox.Ingestor
	auth  *fakeAuth
	queue *fakeQueue
	db    *sql.DB
}

func newEnv(t *testing.T) (*env, *Handler) {
	t.Helper()
	store, db := mailboxtest.NewStore(t)
	auth := &fakeAuth{}
	q := &fakeQueue{}
	w := testWorkspace()
	svc := &Service{Store: store, Auth: auth, WS: fakeWS{w}}
	svc.Sender = &QueueSender{Queue: q, WS: fakeWS{w}, Store: store}
	h := &Handler{Svc: svc, Log: logger.NewLoggerWithLevel("disabled"), RequireAuth: func(n http.Handler) http.Handler { return n }, PollEvery: 20 * time.Millisecond}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	e := &env{srv: srv, svc: svc, store: store, ing: mailbox.NewIngestor(store, logger.NewLoggerWithLevel("disabled"), nil), auth: auth, queue: q, db: db}
	_, _ = db.Exec(`INSERT INTO message_history (id, contact_email, automation_id, veridian_profile_id, veridian_sender_email, sent_at)
		VALUES ('ws1_` + j0id + `', 'marie@exemple-animaux.fr', 'auto1', 'prof1', 'hugo.bourdat@animalservicedistribution.fr', NOW() - interval '2 days')`)
	return e, h
}

func crlf(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }

func replyEML(n int) []byte {
	return crlf(`From: Marie Dupont <marie@exemple-animaux.fr>
To: hugo.bourdat@animalservicedistribution.fr
Subject: Re: Présentation ASD
Date: Thu, 29 Oct 2026 10:15:00 +0100
Message-ID: <rep` + string(rune('0'+n)) + `@exemple-animaux.fr>
In-Reply-To: ` + j0ref + `
References: ` + j0ref + `
Reply-To: marie.reply@exemple-animaux.fr
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="m"

--m
Content-Type: text/html; charset=utf-8

<p>Bonjour, oui cela m'intéresse.</p><script>alert(1)</script>
--m
Content-Type: application/pdf; name="catalogue.pdf"
Content-Disposition: attachment; filename="catalogue.pdf"
Content-Transfer-Encoding: base64

` + base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 contenu")) + `
--m--
`)
}

func bounceEML() []byte {
	return crlf(`From: MAILER-DAEMON@relais.animalservicedistribution.fr
To: hugo.bourdat@animalservicedistribution.fr
Subject: Undelivered Mail Returned to Sender
Date: Thu, 29 Oct 2026 10:20:00 +0100
Message-ID: <bnc1@relais>
MIME-Version: 1.0
Content-Type: multipart/report; report-type=delivery-status; boundary="bb"

--bb
Content-Type: text/plain

Delivery failed

--bb
Content-Type: message/delivery-status

Reporting-MTA: dns; relais

Final-Recipient: rfc822; marie@exemple-animaux.fr
Action: failed
Status: 5.1.1
Remote-MTA: dns; gmail-smtp-in.l.google.com
Diagnostic-Code: smtp; 550-5.1.1 The email account that you tried to reach does not exist.

--bb
Content-Type: message/rfc822

Message-ID: <` + j0id + `@animalservicedistribution.fr>
Subject: x

x
--bb--
`)
}

var box = mailbox.BoxInfo{IntegrationID: "imap1", Address: "hugo.bourdat@animalservicedistribution.fr", ProfileID: "prof1", Folder: "INBOX"}

func (e *env) ingest(t *testing.T, uid uint32, raw []byte, at time.Time) *mailbox.Message {
	t.Helper()
	m, ok, err := e.ing.IngestRaw(context.Background(), ws, box, mailbox.MailboxRef{IntegrationID: "imap1", Folder: "INBOX", UIDValidity: 7, UID: uid}, raw, at, false)
	if err != nil || !ok {
		t.Fatalf("ingestion: %v %v", err, ok)
	}
	return m
}

func (e *env) do(t *testing.T, method, route string, body any) (int, map[string]any, http.Header) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, e.srv.URL+route, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{"_raw": string(b)}
	}
	return resp.StatusCode, out, resp.Header
}

func seed(t *testing.T, e *env) (reply, bounce *mailbox.Message) {
	t.Helper()
	reply = e.ingest(t, 1, replyEML(1), time.Now().Add(-2*time.Hour))
	bounce = e.ingest(t, 2, bounceEML(), time.Now().Add(-time.Hour))
	return
}

// ---- tests ----

func TestMessagesGet_FullContent(t *testing.T) {
	e, _ := newEnv(t)
	reply, _ := seed(t, e)
	code, out, _ := e.do(t, "GET", "/api/veridian/mail.messages.get?workspace_id=ws1&id="+reply.ID, nil)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	m := out["message"].(map[string]any)
	if m["kind"] != "reply_human" || m["direction"] != "in" || m["contact_email"] != "marie@exemple-animaux.fr" {
		t.Fatalf("%v", m)
	}
	if !strings.Contains(m["body_text"].(string), "m'intéresse") {
		t.Fatalf("texte: %v", m["body_text"])
	}
	html := m["body_html_sanitized"].(string)
	if strings.Contains(html, "script") || !strings.Contains(html, "Bonjour") {
		t.Fatalf("html: %q", html)
	}
	if _, has := m["raw_eml"]; has {
		t.Fatal("le message brut ne sort jamais dans messages.get")
	}
	atts := m["attachments"].([]any)
	if len(atts) != 1 || atts[0].(map[string]any)["name"] != "catalogue.pdf" {
		t.Fatalf("pièces: %v", atts)
	}
	if link := m["link"].(map[string]any); link["sent_message_id"] != "ws1_"+j0id || link["automation_id"] != "auto1" {
		t.Fatalf("lien: %v", link)
	}
	hdrs := m["headers"].(map[string]any)
	if hdrs["Subject"] == nil || hdrs["Message-Id"] == nil {
		t.Fatalf("en-têtes: %v", hdrs)
	}
	if got := e.auth.granted(); len(got) == 0 || got[0] != (grant{"message_history", "read"}) {
		t.Fatalf("droit demandé: %v", got)
	}
}

func TestMessagesGet_Errors(t *testing.T) {
	e, _ := newEnv(t)
	if code, out, _ := e.do(t, "GET", "/api/veridian/mail.messages.get?workspace_id=ws1&id=inconnu", nil); code != 404 || out["code"] != "not_found" {
		t.Fatalf("%d %v", code, out)
	}
	if code, _, _ := e.do(t, "GET", "/api/veridian/mail.messages.get?workspace_id=ws1", nil); code != 400 {
		t.Fatalf("id manquant: %d", code)
	}
	if code, _, _ := e.do(t, "GET", "/api/veridian/mail.messages.get?id=x", nil); code != 400 {
		t.Fatalf("workspace manquant: %d", code)
	}
}

func TestAuthErrorsMapToStatus(t *testing.T) {
	e, _ := newEnv(t)
	cases := []struct {
		err  error
		want int
	}{
		{&domain.ErrAuthenticationFailed{Message: "clé révoquée"}, 401},
		{&domain.ErrUnauthorized{Message: "pas membre"}, 403},
		{domain.NewPermissionError(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead, "non"), 403},
	}
	for _, c := range cases {
		e.auth.err = c.err
		for _, r := range []string{"mail.mailboxes.list", "mail.messages.list", "mail.messages.get", "mail.messages.raw", "mail.messages.attachment", "mail.threads.get", "mail.threads.list", "mail.events"} {
			if code, _, _ := e.do(t, "GET", "/api/veridian/"+r+"?workspace_id=ws1&id=x&thread_id=t&contact_email=a@b.fr", nil); code != c.want {
				t.Errorf("%s: %d, attendu %d", r, code, c.want)
			}
		}
	}
	e.auth.err = errors.New("panne base")
	if code, out, _ := e.do(t, "GET", "/api/veridian/mail.messages.list?workspace_id=ws1", nil); code != 500 || strings.Contains(out["error"].(string), "panne base") {
		t.Fatalf("une panne interne ne fuit pas: %d %v", code, out)
	}
}

func TestMessagesList_FiltersAndValidation(t *testing.T) {
	e, _ := newEnv(t)
	seed(t, e)
	code, out, _ := e.do(t, "POST", "/api/veridian/mail.messages.list", map[string]any{"workspace_id": ws, "kind": "bounce_hard,reply_human", "limit": 10})
	if code != 200 || len(out["messages"].([]any)) != 2 {
		t.Fatalf("%d %v", code, out)
	}
	_, out, _ = e.do(t, "GET", "/api/veridian/mail.messages.list?workspace_id=ws1&kind=bounce_hard", nil)
	rows := out["messages"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["dsn_reason_fr"] == "" {
		t.Fatalf("%v", rows)
	}
	_, out, _ = e.do(t, "GET", "/api/veridian/mail.messages.list?workspace_id=ws1&handled=false&direction=in&mailbox=imap1&limit=1", nil)
	if len(out["messages"].([]any)) != 1 || out["next_cursor"] == "" {
		t.Fatalf("pagination: %v", out)
	}
	for _, bad := range []string{"kind=nimporte", "direction=sideways", "handled=peut-etre", "since=hier", "limit=-3", "cursor=pas-un-curseur", "x=%zz"} {
		if code, _, _ := e.do(t, "GET", "/api/veridian/mail.messages.list?workspace_id=ws1&"+bad, nil); code != 400 {
			t.Errorf("%s doit être refusé (400), obtenu %d", bad, code)
		}
	}
	_, out, _ = e.do(t, "GET", "/api/veridian/mail.messages.list?workspace_id=ws1&q=intéresse&body=true", nil)
	if len(out["messages"].([]any)) != 1 {
		t.Fatalf("recherche dans les corps: %v", out)
	}
}

func TestMessagesRaw(t *testing.T) {
	e, _ := newEnv(t)
	reply, _ := seed(t, e)
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/veridian/mail.messages.raw?workspace_id=ws1&id="+reply.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "message/rfc822" || !strings.Contains(resp.Header.Get("Content-Disposition"), ".eml") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	if string(b) != string(replyEML(1)) {
		t.Fatal("le message brut reçu doit être rendu octet pour octet")
	}
	if resp.Header.Get("X-Veridian-Reconstructed") != "false" {
		t.Fatal("un mail reçu n'est pas reconstitué")
	}
	// mail envoyé : reconstitué, et le dit
	if _, err := e.ing.RecordSent(context.Background(), mailbox.SentInput{WorkspaceID: ws, MessageHistoryID: "ws1_" + j0id, ContactEmail: "marie@exemple-animaux.fr",
		FromAddress: "hugo.bourdat@animalservicedistribution.fr", Subject: "Présentation ASD", HTML: "<p>Bonjour Marie</p>", SentAt: time.Now().Add(-48 * time.Hour), BodySource: mailbox.BodyCaptured}); err != nil {
		t.Fatal(err)
	}
	_, out, _ := e.do(t, "GET", "/api/veridian/mail.messages.list?workspace_id=ws1&direction=out", nil)
	id := out["messages"].([]any)[0].(map[string]any)["id"].(string)
	resp2, _ := http.Get(e.srv.URL + "/api/veridian/mail.messages.raw?workspace_id=ws1&id=" + id)
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.Header.Get("X-Veridian-Reconstructed") != "true" || !strings.Contains(string(b2), "X-Veridian-Reconstructed") || !strings.Contains(string(b2), "Bonjour Marie") {
		t.Fatalf("%v\n%s", resp2.Header, b2)
	}
}

func TestAttachmentDownload(t *testing.T) {
	e, _ := newEnv(t)
	reply, _ := seed(t, e)
	resp, err := http.Get(e.srv.URL + "/api/veridian/mail.messages.attachment?workspace_id=ws1&index=0&id=" + reply.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	h := resp.Header
	if resp.StatusCode != 200 || string(b) != "%PDF-1.4 contenu" {
		t.Fatalf("%d %q", resp.StatusCode, b)
	}
	if h.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") ||
		h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("une pièce jointe n'est jamais servie comme une page: %v", h)
	}
	if code, _, _ := e.do(t, "GET", "/api/veridian/mail.messages.attachment?workspace_id=ws1&index=7&id="+reply.ID, nil); code != 404 {
		t.Fatalf("pièce absente: %d", code)
	}
}

func TestUpdate_HandledAndLabels(t *testing.T) {
	e, _ := newEnv(t)
	reply, _ := seed(t, e)
	code, out, _ := e.do(t, "POST", "/api/veridian/mail.messages.update", map[string]any{"workspace_id": ws, "id": reply.ID, "handled": true, "labels": []string{"a-rappeler", "chaud"}})
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	m := out["message"].(map[string]any)
	if m["handled_by"] != "agent:key-1" || m["handled_at"] == nil || len(m["labels"].([]any)) != 2 {
		t.Fatalf("%v", m)
	}
	if got := e.auth.granted(); got[len(got)-1] != (grant{"message_history", "read"}) || got[0] != (grant{"message_history", "write"}) {
		t.Fatalf("écriture demandée avant lecture: %v", got)
	}
	_, out, _ = e.do(t, "POST", "/api/veridian/mail.messages.update", map[string]any{"workspace_id": ws, "id": reply.ID, "handled": false})
	if m := out["message"].(map[string]any); m["handled_at"] != nil || len(m["labels"].([]any)) != 2 {
		t.Fatalf("handled=false rouvre sans toucher aux libellés: %v", m)
	}
	for name, body := range map[string]map[string]any{
		"rien":            {"workspace_id": ws, "id": reply.ID},
		"libellé vide":    {"workspace_id": ws, "id": reply.ID, "labels": []string{" "}},
		"libellé énorme":  {"workspace_id": ws, "id": reply.ID, "labels": []string{strings.Repeat("x", 41)}},
		"trop de libellés": {"workspace_id": ws, "id": reply.ID, "labels": make([]string, 21)},
		"handled invalide": {"workspace_id": ws, "id": reply.ID, "handled": "peut-etre"},
	} {
		if code, _, _ := e.do(t, "POST", "/api/veridian/mail.messages.update", body); code != 400 {
			t.Errorf("%s: %d, attendu 400", name, code)
		}
	}
	if code, _, _ := e.do(t, "POST", "/api/veridian/mail.messages.update", map[string]any{"workspace_id": ws, "id": "inconnu", "handled": true}); code != 404 {
		t.Fatalf("%d", code)
	}
	// lecture seule : refus 403 sur l'écriture
	e.auth.deny = map[grant]error{{"message_history", "write"}: domain.NewPermissionError(domain.PermissionResourceMessageHistory, domain.PermissionTypeWrite, "non")}
	if code, _, _ := e.do(t, "POST", "/api/veridian/mail.messages.update", map[string]any{"workspace_id": ws, "id": reply.ID, "handled": true}); code != 403 {
		t.Fatalf("%d", code)
	}
}

func TestThreads(t *testing.T) {
	e, _ := newEnv(t)
	reply, bounce := seed(t, e)
	if _, err := e.ing.RecordSent(context.Background(), mailbox.SentInput{WorkspaceID: ws, MessageHistoryID: "ws1_" + j0id, ContactEmail: "marie@exemple-animaux.fr",
		FromAddress: "hugo.bourdat@animalservicedistribution.fr", Subject: "Présentation ASD", HTML: "<p>Bonjour Marie</p>", SentAt: time.Now().Add(-3 * time.Hour), BodySource: mailbox.BodyCaptured}); err != nil {
		t.Fatal(err)
	}
	if reply.ThreadID != bounce.ThreadID {
		t.Fatalf("réponse et rebond du même envoi dans le même fil: %s %s", reply.ThreadID, bounce.ThreadID)
	}
	code, out, _ := e.do(t, "GET", "/api/veridian/mail.threads.get?workspace_id=ws1&thread_id="+reply.ThreadID, nil)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	th := out["thread"].(map[string]any)
	msgs := th["messages"].([]any)
	if len(msgs) != 3 || msgs[0].(map[string]any)["kind"] != "sent" || msgs[2].(map[string]any)["kind"] != "bounce_hard" {
		t.Fatalf("ordre chronologique J0, réponse, rebond: %v", msgs)
	}
	ms := th["milestones"].([]any)
	if ms[0].(map[string]any)["label"] != "Premier mail" || ms[1].(map[string]any)["label"] != "Réponse" || ms[2].(map[string]any)["label"] != "Rebond définitif" {
		t.Fatalf("jalons: %v", ms)
	}
	for _, m := range msgs {
		if _, has := m.(map[string]any)["raw_eml"]; has {
			t.Fatal("pas de message brut dans un fil")
		}
	}
	_, out, _ = e.do(t, "GET", "/api/veridian/mail.threads.get?workspace_id=ws1&contact_email=Marie@exemple-animaux.fr", nil)
	if ths := out["threads"].([]any); len(ths) != 1 || len(ths[0].(map[string]any)["messages"].([]any)) != 3 {
		t.Fatalf("fils du contact: %v", out)
	}
	_, out, _ = e.do(t, "GET", "/api/veridian/mail.threads.list?workspace_id=ws1&contact_email=marie@exemple-animaux.fr", nil)
	if ths := out["threads"].([]any); len(ths) != 1 || ths[0].(map[string]any)["message_count"].(float64) != 3 {
		t.Fatalf("%v", out)
	}
	if code, _, _ := e.do(t, "GET", "/api/veridian/mail.threads.get?workspace_id=ws1&thread_id=t_inconnu", nil); code != 404 {
		t.Fatalf("%d", code)
	}
	if code, _, _ := e.do(t, "GET", "/api/veridian/mail.threads.get?workspace_id=ws1", nil); code != 400 {
		t.Fatalf("%d", code)
	}
}

func TestMailboxesList(t *testing.T) {
	e, _ := newEnv(t)
	seed(t, e)
	code, out, _ := e.do(t, "GET", "/api/veridian/mail.mailboxes.list?workspace_id=ws1", nil)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	boxes := out["mailboxes"].([]any)
	var prof map[string]any
	for _, b := range boxes {
		if b.(map[string]any)["profile_id"] == "prof1" {
			prof = b.(map[string]any)
		}
	}
	if prof == nil || prof["address"] != "hugo.bourdat@animalservicedistribution.fr" || prof["integration_id"] != "imap1" || prof["folder"] != "INBOX" {
		t.Fatalf("%v", boxes)
	}
	if prof["count_in"].(float64) != 2 || prof["count_unhandled"].(float64) != 1 {
		t.Fatalf("compteurs: %v", prof)
	}
	if len(boxes) != 3 { // 2 profils d'envoi + le profil sans IMAP; l'IMAP lié ne double pas la boîte
		t.Logf("boîtes: %v", boxes)
	}
}

// lit les événements SSE jusqu'à n événements ou délai
func readEvents(t *testing.T, resp *http.Response, n int, wait time.Duration) []map[string]string {
	t.Helper()
	var out []map[string]string
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(resp.Body)
		cur := map[string]string{}
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if len(cur) > 0 && cur["event"] != "" {
					out = append(out, cur)
					if len(out) >= n {
						return
					}
				}
				cur = map[string]string{}
				continue
			}
			if k, v, ok := strings.Cut(line, ": "); ok {
				cur[k] = v
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(wait):
	}
	return out
}

func TestEvents_StreamAndResume(t *testing.T) {
	e, _ := newEnv(t)
	seed(t, e) // seq 1, 2 déjà là
	// flux sans repère : seulement le neuf
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/veridian/mail.events?workspace_id=ws1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%v %v", err, resp)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		e.ingest(t, 3, replyEML(3), time.Now())
	}()
	evs := readEvents(t, resp, 1, 3*time.Second)
	resp.Body.Close()
	if len(evs) != 1 || evs[0]["event"] != "message.received" || evs[0]["id"] != "3" {
		t.Fatalf("un seul événement neuf attendu (seq 3): %v", evs)
	}
	var payload map[string]any
	json.Unmarshal([]byte(evs[0]["data"]), &payload)
	if payload["kind"] != "reply_human" || payload["contact_email"] != "marie@exemple-animaux.fr" {
		t.Fatalf("%v", payload)
	}
	if _, has := payload["body_text"]; has {
		t.Fatal("un événement ne porte pas le corps")
	}
	// reprise exacte : Last-Event-ID 1 rend 2 (rebond) puis 3, rien de perdu
	req2, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/veridian/mail.events?workspace_id=ws1", nil)
	req2.Header.Set("Last-Event-ID", "1")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	evs2 := readEvents(t, resp2, 2, 3*time.Second)
	resp2.Body.Close()
	if len(evs2) != 2 || evs2[0]["id"] != "2" || evs2[1]["id"] != "3" {
		t.Fatalf("reprise après Last-Event-ID=1: %v", evs2)
	}
	// filtre par nature
	req3, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/veridian/mail.events?workspace_id=ws1&since_seq=0&kind=bounce_hard", nil)
	resp3, _ := http.DefaultClient.Do(req3)
	evs3 := readEvents(t, resp3, 5, 600*time.Millisecond)
	resp3.Body.Close()
	if len(evs3) != 1 || evs3[0]["id"] != "2" {
		t.Fatalf("filtre kind: %v", evs3)
	}
	// mauvais paramètres
	for _, q := range []string{"direction=x", "since_seq=abc", "kind=zzz"} {
		if code, _, _ := e.do(t, "GET", "/api/veridian/mail.events?workspace_id=ws1&"+q, nil); code != 400 {
			t.Errorf("%s: %d", q, code)
		}
	}
}

func TestEvents_LimitPerWorkspace(t *testing.T) {
	e, _ := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var open []*http.Response
	for i := 0; i < maxStreamsPerWS; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/veridian/mail.events?workspace_id=ws1", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("flux %d: %v", i, err)
		}
		open = append(open, resp)
	}
	if code, out, _ := e.do(t, "GET", "/api/veridian/mail.events?workspace_id=ws1", nil); code != 429 || out["code"] != "too_many_streams" {
		t.Fatalf("le flux de trop doit être refusé: %d %v", code, out)
	}
	for _, r := range open {
		r.Body.Close()
	}
}
