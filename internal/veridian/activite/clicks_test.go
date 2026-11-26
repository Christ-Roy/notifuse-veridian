package activite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/pkg/crypto"
)

type seen struct{ ws, mid, host string }

func record(t *testing.T) chan seen {
	t.Helper()
	ch := make(chan seen, 4)
	SetClickRecorder(func(_ context.Context, ws, mid, host string) { ch <- seen{ws, mid, host} })
	t.Cleanup(func() { SetClickRecorder(nil) })
	return ch
}

func wait(t *testing.T, ch chan seen) seen {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("aucun hôte posé")
	}
	return seen{}
}

// Les deux routes de suivi de l'upstream : la redirection passe intacte, seul l'hôte du lien est posé.
func TestWrapClicks_HoteSeulApresLaRedirection(t *testing.T) {
	ch := record(t)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://ailleurs.fr/", http.StatusSeeOther)
	})
	h := WrapClicks(upstream)

	q := url.Values{"mid": {"ws1_m1"}, "wid": {"ws1"}, "url": {"https://Boutique.exemple.fr/p?jeton=abc"}}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/visit?"+q.Encode(), nil))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("redirection de l'upstream : %d", rr.Code)
	}
	if s := wait(t, ch); s != (seen{"ws1", "ws1_m1", "boutique.exemple.fr"}) {
		t.Fatalf("/visit : %+v", s)
	}

	tok, err := crypto.EncryptTrackingToken("ws1_m2\nws1\n1700000000\nhttps://www.exemple.fr/offre?u=marie%40x.fr")
	if err != nil {
		t.Fatal(err)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/r/"+tok, nil))
	if s := wait(t, ch); s != (seen{"ws1", "ws1_m2", "www.exemple.fr"}) {
		t.Fatalf("/r/ : %+v", s)
	}

	// Ni une autre route, ni un jeton illisible, ni un lien sans hôte.
	for _, p := range []string{"/api/x?mid=a&wid=b&url=https://x.fr", "/r/illisible", "/visit?mid=a&wid=b&url=mailto:a@b.fr"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	select {
	case s := <-ch:
		t.Fatalf("rien à poser : %+v", s)
	case <-time.After(200 * time.Millisecond):
	}
}
