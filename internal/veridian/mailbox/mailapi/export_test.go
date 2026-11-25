package mailapi

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
)

func TestWriteMbox_EscapesAndNormalizes(t *testing.T) {
	var b bytes.Buffer
	at := time.Date(2026, 11, 25, 18, 4, 5, 0, time.UTC)
	raw := "From: A <a@b.fr>\r\nSubject: x\r\n\r\nFrom moi\r\n>From lui\r\nFromage\r\nfin\r\n\r\n"
	if err := writeMbox(&b, "Anne <a@b.fr>", at, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	want := "From a@b.fr Wed Nov 25 18:04:05 2026\nFrom: A <a@b.fr>\nSubject: x\n\n>From moi\n>>From lui\nFromage\nfin\n\n"
	if b.String() != want {
		t.Fatalf("%q", b.String())
	}
	b.Reset()
	if err := writeMbox(&b, "illisible", at, []byte("a")); err != nil || !strings.HasPrefix(b.String(), "From MAILER-DAEMON ") {
		t.Fatalf("%q %v", b.String(), err)
	}
}

func get(t *testing.T, url string) (int, http.Header, string, error) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body), err
}

// boxView pose la table des drapeaux (V75) : la vue « client mail » s'en sert.
func boxView(t *testing.T, e *env) {
	t.Helper()
	for _, sp := range veridianmig.Default.List() {
		if sp.Number != 75 {
			continue
		}
		for _, q := range sp.Workspace {
			if _, err := e.db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestExport_DownloadsAFolder(t *testing.T) {
	e, _ := newEnv(t)
	boxView(t, e)
	seed(t, e)                          // deux entrants du profil prof1 : une réponse, un rebond
	time.Sleep(2500 * time.Millisecond) // BoxIndex attend 2 s avant de montrer une ligne
	base := e.srv.URL + "/api/veridian/mail.export?workspace_id=ws1&profile_id=prof1&folder="
	counts := map[string]int{}
	for _, f := range []string{"inbox", "rejects", "sent", "trash"} {
		code, hd, body, err := get(t, base+f)
		if err != nil || code != 200 || hd.Get("Content-Type") != "application/mbox" || !strings.Contains(hd.Get("Content-Disposition"), f) {
			t.Fatalf("%s : %d %v %v", f, code, hd, err)
		}
		counts[f] = strings.Count("\n"+body, "\nFrom ") // lignes de séparation ; le rebond cite un autre message
	}
	if counts["inbox"] != 1 || counts["rejects"] != 1 {
		t.Fatalf("%v", counts)
	}
	if code, _, body, _ := get(t, strings.Replace(base, "prof1", "autre", 1)+"inbox"); code != 200 || body != "" {
		t.Fatalf("un autre profil ne rend rien : %d %q", code, body)
	}
	for _, q := range []string{"", "x", "inbox&profile_id="} {
		if code, _, _, _ := get(t, e.srv.URL+"/api/veridian/mail.export?workspace_id=ws1&folder="+q); code != 400 {
			t.Errorf("%q : %d", q, code)
		}
	}
	e.auth.mu.Lock()
	e.auth.err = &domain.ErrUnauthorized{Message: "x"}
	e.auth.mu.Unlock()
	if code, _, _, _ := get(t, base+"inbox"); code != 403 {
		t.Fatalf("sans droit : %d", code)
	}
}

// Droit perdu en cours d'export : la connexion est coupée, jamais un fichier tronqué rendu comme complet.
func TestExport_AbortsWhenRightIsLost(t *testing.T) {
	e, _ := newEnv(t)
	boxView(t, e)
	seed(t, e)
	time.Sleep(2500 * time.Millisecond)
	e.auth.mu.Lock()
	e.auth.failAfter, e.auth.err2 = len(e.auth.calls)+1, &domain.ErrUnauthorized{Message: "x"}
	e.auth.mu.Unlock()
	_, _, body, err := get(t, e.srv.URL+"/api/veridian/mail.export?workspace_id=ws1&profile_id=prof1&folder=inbox")
	if err == nil || strings.Contains(body, "Subject") {
		t.Fatalf("coupure attendue : %v %q", err, body)
	}
}

func TestExport_OneAtATime(t *testing.T) {
	_, h := newEnv(t)
	if !h.acquireExport("ws1") || h.acquireExport("ws1") || !h.acquireExport("ws2") {
		t.Fatal("un export à la fois par espace")
	}
	h.releaseExport("ws1")
	if !h.acquireExport("ws1") {
		t.Fatal("place rendue")
	}
}
