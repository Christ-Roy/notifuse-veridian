package relaycert

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testToken   = "cf-token-DO-NOT-LEAK-0123456789"
	testKeyAuth = "tokenvalue.thumbprint"
	testZoneID  = "zone-example"
	testRecID   = "rec-42"
)

// cfFake est un faux API Cloudflare : il note chaque requête et rejoue des réponses programmées.
type cfFake struct {
	mu       sync.Mutex
	requests []string // "METHOD path"
	auth     []string // en-têtes Authorization reçus
	zoneHits int
	statuses []int // statuts successifs pour POST /dns_records (vide = 200)
	delFail  int   // nombre d'échecs 500 successifs sur DELETE
	noZone   bool
}

func (f *cfFake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/client/v4/zones":
		f.zoneHits++
		if f.noZone || r.URL.Query().Get("name") != "example.test" {
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"` + testZoneID + `"}]}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dns_records"):
		if len(f.statuses) > 0 {
			st := f.statuses[0]
			f.statuses = f.statuses[1:]
			if st != http.StatusOK {
				w.WriteHeader(st)
				_, _ = w.Write([]byte(`{"success":false}`))
				return
			}
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"` + testRecID + `"}}`))
	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/dns_records/"):
		if f.delFail > 0 {
			f.delFail--
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"success":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"` + testRecID + `"}}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false}`))
	}
}

func newCFTest(t *testing.T, f *cfFake) (*CloudflareDNS, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	p, err := NewCloudflareDNS(testToken)
	if err != nil {
		t.Fatal(err)
	}
	p.baseURL = srv.URL + "/client/v4"
	old := retryPause
	retryPause = time.Millisecond
	return p, func() { srv.Close(); retryPause = old }
}

func TestNewCloudflareDNS_RequiresToken(t *testing.T) {
	if _, err := NewCloudflareDNS("   "); err == nil {
		t.Fatal("jeton vide doit être refusé sans appel réseau")
	}
}

// Création puis suppression : la suppression vise l'identifiant conservé, sans lookup par nom.
func TestCloudflareDNS_PresentThenCleanUpByID(t *testing.T) {
	f := &cfFake{}
	p, done := newCFTest(t, f)
	defer done()
	if err := p.Present("relais-smtp.example.test", "", testKeyAuth); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if err := p.CleanUp("relais-smtp.example.test", "", testKeyAuth); err != nil {
		t.Fatalf("CleanUp: %v", err)
	}
	want := "DELETE /client/v4/zones/" + testZoneID + "/dns_records/" + testRecID
	found := false
	for _, r := range f.requests {
		if r == want {
			found = true
		}
		if strings.HasPrefix(r, "GET ") && strings.Contains(r, "/dns_records") {
			t.Fatalf("suppression par recherche de nom interdite : %s", r)
		}
	}
	if !found {
		t.Fatalf("suppression par identifiant %q attendue, requêtes: %v", want, f.requests)
	}
}

func TestCloudflareDNS_ZoneNotFound(t *testing.T) {
	f := &cfFake{noZone: true}
	p, done := newCFTest(t, f)
	defer done()
	err := p.Present("relais-smtp.example.test", "", testKeyAuth)
	if err == nil || !strings.Contains(err.Error(), "aucune zone") {
		t.Fatalf("zone introuvable : erreur explicite attendue, got %v", err)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "POST ") {
			t.Fatalf("aucun TXT ne doit être créé sans zone : %s", r)
		}
	}
}

// Un jeton refusé (403) échoue sans que le jeton apparaisse dans l'erreur.
func TestCloudflareDNS_InvalidTokenHidesSecret(t *testing.T) {
	f := &cfFake{statuses: []int{http.StatusForbidden}}
	p, done := newCFTest(t, f)
	defer done()
	err := p.Present("relais-smtp.example.test", "", testKeyAuth)
	if err == nil {
		t.Fatal("403 doit être une erreur")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("le jeton ne doit jamais apparaître dans une erreur : %v", err)
	}
}

// 429 puis succès : un seul nouvel essai, puis création.
func TestCloudflareDNS_RetriesOnceOn429(t *testing.T) {
	f := &cfFake{statuses: []int{http.StatusTooManyRequests}}
	p, done := newCFTest(t, f)
	defer done()
	if err := p.Present("relais-smtp.example.test", "", testKeyAuth); err != nil {
		t.Fatalf("429 puis succès attendu : %v", err)
	}
	posts := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, "POST ") {
			posts++
		}
	}
	if posts != 2 {
		t.Fatalf("un réessai attendu après 429 (2 POST), got %d", posts)
	}
}

// Nettoyage vu échouer : si la suppression échoue, l'enregistrement reste connu et CleanUpAll le reprend.
func TestCloudflareDNS_CleanupFailureIsKept(t *testing.T) {
	f := &cfFake{delFail: 2}
	p, done := newCFTest(t, f)
	defer done()
	if err := p.Present("relais-smtp.example.test", "", testKeyAuth); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if err := p.CleanUpAll(); err == nil {
		t.Fatal("suppression en échec : CleanUpAll doit signaler l'erreur")
	}
	if len(p.created) != 1 {
		t.Fatalf("l'enregistrement doit rester connu après un échec de suppression, got %d", len(p.created))
	}
	if err := p.CleanUpAll(); err != nil {
		t.Fatalf("second nettoyage après reprise attendu sans erreur : %v", err)
	}
	if len(p.created) != 0 {
		t.Fatal("après suppression réussie, plus aucun enregistrement connu")
	}
}

func TestCloudflareDNS_TokenOnlyInAuthorizationHeader(t *testing.T) {
	f := &cfFake{}
	p, done := newCFTest(t, f)
	defer done()
	_ = p.Present("relais-smtp.example.test", "", testKeyAuth)
	for _, a := range f.auth {
		if a != "Bearer "+testToken {
			t.Fatalf("jeton attendu uniquement dans Authorization, got %q", a)
		}
	}
}
