package boxapi

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const accID = "0a000000-0000-4000-8000-0000000000aa"

type quota struct{ left int }

func (q *quota) Allow(string, string) bool { q.left--; return q.left >= 0 }

func setupFull(t *testing.T) (*http.ServeMux, *Handler, *memStore) {
	t.Helper()
	s := &memStore{one: &boxaccess.Access{ID: accID, WorkspaceID: "ws", ProfileID: "p", Address: "jc@boite.fr", Label: "Téléphone <1>", CanSend: true}}
	h := &Handler{Store: s, Auth: &authz{}, WS: wsGet{}, Log: logger.NewLogger(), RequireAuth: func(n http.Handler) http.Handler { return n },
		Connection: Connection{IMAP: &Endpoint{Host: "imap.test", Port: 443, Security: "SSL/TLS"}, SMTP: &Endpoint{Host: "smtp.test", Port: 443, Security: "SSL/TLS"}},
		BaseURL:    "https://mail.test/", LinkKey: []byte("clé"), Limiter: &quota{left: 1000},
		Door: func(context.Context, string, *Endpoint) error { return nil }}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux, h, s
}

func TestReadLink_SignLink(t *testing.T) {
	now := time.Now()
	key := []byte("k")
	tok := SignLink(key, "ws", "id", now.Add(time.Minute))
	if ws, id, err := ReadLink(key, tok, now); err != nil || ws != "ws" || id != "id" {
		t.Fatal(ws, id, err)
	}
	for name, c := range map[string]struct {
		key []byte
		tok string
		at  time.Time
	}{
		"expiré": {key, tok, now.Add(2 * time.Minute)}, "autre clé": {[]byte("z"), tok, now}, "sans clé": {nil, tok, now}, "vide": {key, "", now},
		"altéré": {key, "A" + tok, now}, "sans signature": {key, strings.Split(tok, ".")[0] + ".", now}, "énorme": {key, strings.Repeat("a", 2000) + ".b", now},
	} {
		if _, _, err := ReadLink(c.key, c.tok, c.at); err == nil {
			t.Errorf("%s : accepté", name)
		}
	}
}

func TestMobileConfig_SafeAndWithoutPassword(t *testing.T) {
	_, h, s := setupFull(t)
	b, err := MobileConfig(s.one, h.Connection, "Société & fils")
	if err != nil {
		t.Fatal(err)
	}
	dec := xml.NewDecoder(strings.NewReader(string(b)))
	for {
		if _, err := dec.Token(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("plist illisible : %v", err)
		}
	}
	for _, want := range []string{"<key>IncomingMailServerHostName</key><string>imap.test</string>", "<key>OutgoingMailServerHostName</key><string>smtp.test</string>",
		"<key>IncomingMailServerPortNumber</key><integer>443</integer>", "<key>IncomingMailServerUseSSL</key><true/>", "<key>OutgoingMailServerUseSSL</key><true/>",
		"<key>OutgoingPasswordSameAsIncomingPassword</key><true/>", "<key>IncomingMailServerUsername</key><string>jc@boite.fr</string>", "Téléphone &lt;1&gt;", "Société &amp; fils"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("manque %s", want)
		}
	}
	if strings.Contains(string(b), "ServerPassword") || strings.Contains(string(b), "<key>IncomingPassword") {
		t.Fatal("aucun mot de passe dans un profil")
	}
	again, _ := MobileConfig(s.one, h.Connection, "Société & fils")
	if string(again) != string(b) {
		t.Fatal("identifiants stables : réinstaller remplace")
	}
	if _, err := MobileConfig(s.one, Connection{IMAP: h.Connection.IMAP}, "x"); err == nil {
		t.Fatal("sans envoi servi, pas de profil")
	}
	if _, err := MobileConfig(&boxaccess.Access{ID: "x"}, h.Connection, "x"); err == nil {
		t.Fatal("identifiant illisible")
	}
}

func TestProfile_LinkThenDownload(t *testing.T) {
	mux, h, s := setupFull(t)
	w := call(mux, "POST", "/api/veridian/mail.access.profileLink", `{"workspace_id":"ws","id":"`+accID+`"}`)
	var out struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || !strings.HasPrefix(out.URL, "https://mail.test/api/veridian/mail.access.profile?t=") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	u, _ := url.Parse(out.URL)
	path := u.RequestURI()
	w = call(mux, "GET", path, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != profileMIME || !strings.Contains(w.Header().Get("Content-Disposition"), "jc-boite.fr.mobileconfig") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	if w = call(mux, "GET", path+"x", ""); w.Code != 404 {
		t.Fatalf("lien altéré : %d", w.Code)
	}
	if w = call(mux, "GET", "/api/veridian/mail.access.profile?t="+url.QueryEscape(SignLink(h.LinkKey, "ws", accID, time.Now().Add(-time.Second))), ""); w.Code != 404 {
		t.Fatalf("lien expiré : %d", w.Code)
	}
	now := time.Now()
	s.one.RevokedAt = &now
	if w = call(mux, "GET", path, ""); w.Code != 404 {
		t.Fatalf("accès révoqué : %d", w.Code)
	}
	if w = call(mux, "POST", "/api/veridian/mail.access.profileLink", `{"workspace_id":"ws","id":"`+accID+`"}`); w.Code != 409 {
		t.Fatalf("lien d'un accès révoqué : %d", w.Code)
	}
	if w = call(mux, "POST", "/api/veridian/mail.access.profileLink", `{"workspace_id":"ws","id":"autre"}`); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
}

func TestSetDisabled_CutsOpenConnections(t *testing.T) {
	var cut []string
	boxaccess.SetCloser(func(ids []string) { cut = append(cut, ids...) })
	t.Cleanup(func() { boxaccess.SetCloser(nil) })
	mux, _, _ := setupFull(t)
	if w := call(mux, "POST", "/api/veridian/mail.access.setDisabled", `{"workspace_id":"ws","id":"acc1","disabled":false}`); w.Code != 200 || len(cut) != 0 {
		t.Fatalf("réactiver ne coupe rien : %d %v", w.Code, cut)
	}
	if w := call(mux, "POST", "/api/veridian/mail.access.setDisabled", `{"workspace_id":"ws","id":"acc1","disabled":true}`); w.Code != 200 || len(cut) != 1 {
		t.Fatalf("%d %v", w.Code, cut)
	}
	if w := call(mux, "POST", "/api/veridian/mail.access.setDisabled", `{"workspace_id":"ws","id":"x","disabled":true}`); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
}

func TestList_HoldersAndAttempts(t *testing.T) {
	old := boxaccess.Journal
	boxaccess.Journal = boxaccess.NewAttempts()
	t.Cleanup(func() { boxaccess.Journal = old })
	mux, _, s := setupFull(t)
	s.created = []boxaccess.Access{{ID: "a", Address: "jc@boite.fr", Holder: "u1"}, {ID: "b", Address: "jc@boite.fr"}}
	boxaccess.Journal.Add("imap", "jc@boite.fr", false, "bad_credentials")
	boxaccess.Journal.Add("imap", "ailleurs@boite.fr", false, "bad_credentials")
	body := call(mux, "GET", "/api/veridian/mail.access.list?workspace_id=ws", "").Body.String()
	var out struct {
		Accesses []boxaccess.Access  `json:"accesses"`
		Attempts []boxaccess.Attempt `json:"attempts"`
		Since    string              `json:"attempts_since"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Accesses) != 2 || out.Accesses[0].HolderEmail != "titulaire@t.fr" || out.Accesses[1].HolderEmail != "" {
		t.Fatalf("%v %s", err, body)
	}
	if len(out.Attempts) != 1 || out.Attempts[0].Address != "jc@boite.fr" || out.Since == "" {
		t.Fatalf("les essais des adresses de l'espace seulement : %s", body)
	}
}

// Le cas de Ben (25/11) : STARTTLS sur 443, mot de passe vide. L'écran doit le dire, avec la valeur à mettre.
func TestTest_SaysWhatToChange(t *testing.T) {
	good := testRequest{WorkspaceID: "ws", Address: "jc@boite.fr", Via: "imap", Host: "imap.test", Port: 443, Security: "ssl", Username: "JC@boite.fr", Password: "le-bon"}
	for name, c := range map[string]struct {
		change func(q *testRequest, s *memStore, h *Handler)
		bad    map[string]string // code -> valeur attendue
	}{
		"tout bon":           {func(*testRequest, *memStore, *Handler) {}, nil},
		"Ben":                {func(q *testRequest, _ *memStore, _ *Handler) { q.Security, q.Password = "starttls", " " }, map[string]string{"security": "ssl", "password_empty": ""}},
		"mauvais port":       {func(q *testRequest, _ *memStore, _ *Handler) { q.Port = 993 }, map[string]string{"port": "443"}},
		"mauvais serveur":    {func(q *testRequest, _ *memStore, _ *Handler) { q.Host = "mail.boite.fr" }, map[string]string{"host": "imap.test"}},
		"identifiant sans @": {func(q *testRequest, _ *memStore, _ *Handler) { q.Username = "jc" }, map[string]string{"username": "jc@boite.fr"}},
		"mot de passe faux":  {func(q *testRequest, _ *memStore, _ *Handler) { q.Password = "faux" }, map[string]string{"credentials": ""}},
		"porte muette": {func(_ *testRequest, _ *memStore, h *Handler) {
			h.Door = func(context.Context, string, *Endpoint) error { return errors.New("x") }
		}, map[string]string{"door": ""}},
		"révoqué":         {func(_ *testRequest, s *memStore, _ *Handler) { n := time.Now(); s.one.RevokedAt = &n }, map[string]string{"access_open": "revoked"}},
		"désactivé":       {func(_ *testRequest, s *memStore, _ *Handler) { n := time.Now(); s.one.DisabledAt = &n }, map[string]string{"access_open": "disabled"}},
		"titulaire parti": {func(_ *testRequest, s *memStore, _ *Handler) { s.reason = boxaccess.ClosedHolder }, map[string]string{"access_open": "holder_gone"}},
		"envoi, tout bon": {func(q *testRequest, _ *memStore, _ *Handler) { q.Via, q.Host = "smtp", "smtp.test" }, nil},
		"envoi, lecture seule": {func(q *testRequest, s *memStore, _ *Handler) {
			q.Via, q.Host = "smtp", "smtp.test"
			s.one.CanSend = false
		}, map[string]string{"can_send": ""}},
		"envoi, espace suspendu": {func(q *testRequest, s *memStore, _ *Handler) { q.Via, q.Host = "smtp", "smtp.test"; s.suspended = true }, map[string]string{"not_suspended": ""}},
	} {
		mux, h, s := setupFull(t)
		q := good
		c.change(&q, s, h)
		body, _ := json.Marshal(q)
		w := call(mux, "POST", "/api/veridian/mail.access.test", string(body))
		var out struct {
			OK     bool    `json:"ok"`
			Checks []Check `json:"checks"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 || out.OK != (len(c.bad) == 0) {
			t.Errorf("%s : %d %s", name, w.Code, w.Body)
			continue
		}
		got := map[string]string{}
		for _, k := range out.Checks {
			if !k.OK {
				got[k.Code] = k.Expected
			}
		}
		if len(got) != len(c.bad) {
			t.Errorf("%s : %v, attendu %v", name, got, c.bad)
		}
		for code, exp := range c.bad {
			if v, ok := got[code]; !ok || v != exp {
				t.Errorf("%s : %s = %q, attendu %q", name, code, v, exp)
			}
		}
		if strings.Contains(w.Body.String(), "le-bon") {
			t.Errorf("%s : le mot de passe ne revient jamais", name)
		}
	}
}

func TestTest_Refusals(t *testing.T) {
	mux, h, _ := setupFull(t)
	for name, body := range map[string]string{
		"via":                `{"workspace_id":"ws","address":"jc@boite.fr","via":"pop","security":"ssl"}`,
		"sécurité":           `{"workspace_id":"ws","address":"jc@boite.fr","via":"imap","security":"x"}`,
		"adresse d'un autre": `{"workspace_id":"ws","address":"x@y.fr","via":"imap","security":"ssl"}`,
		"sans adresse":       `{"workspace_id":"ws","via":"imap","security":"ssl"}`,
	} {
		if w := call(mux, "POST", "/api/veridian/mail.access.test", body); w.Code != 400 {
			t.Errorf("%s : %d", name, w.Code)
		}
	}
	h.Limiter = &quota{left: 0}
	if w := call(mux, "POST", "/api/veridian/mail.access.test", `{"workspace_id":"ws","address":"jc@boite.fr","via":"imap","security":"ssl"}`); w.Code != 429 {
		t.Fatalf("limite : %d", w.Code)
	}
	h.Connection.SMTP = nil
	if w := call(mux, "POST", "/api/veridian/mail.access.test", `{"workspace_id":"ws","address":"jc@boite.fr","via":"smtp","security":"ssl"}`); w.Code != 400 {
		t.Fatalf("porte non servie : %d", w.Code)
	}
}

func TestDialDoor(t *testing.T) {
	serve := func(banner string) *Endpoint {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_, _ = c.Write([]byte(banner))
				_ = c.Close()
			}
		}()
		return &Endpoint{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Security: "starttls"}
	}
	ctx := context.Background()
	if err := dialDoor(ctx, "imap", serve("* OK pret\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := dialDoor(ctx, "smtp", serve("220 pret\r\n")); err != nil {
		t.Fatal(err)
	}
	for name, e := range map[string]*Endpoint{"autre bannière": serve("HTTP/1.1 400\r\n"), "muette": serve("")} {
		if err := dialDoor(ctx, "imap", e); err == nil {
			t.Errorf("%s : acceptée", name)
		}
	}
	tlsWanted := serve("* OK en clair\r\n")
	tlsWanted.Security = "SSL/TLS"
	if err := dialDoor(ctx, "imap", tlsWanted); err == nil {
		t.Fatal("une porte en clair n'est pas une porte TLS")
	}
}
