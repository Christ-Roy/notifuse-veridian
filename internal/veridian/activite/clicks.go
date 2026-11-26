package activite

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Notifuse/notifuse/pkg/crypto"
)

// ClickRecorder pose l'hôte du premier clic d'un message (SetClickHost sur la base de l'espace).
type ClickRecorder func(ctx context.Context, workspaceID, messageID, host string)

var (
	recorder atomic.Pointer[ClickRecorder]
	inflight atomic.Int32
)

// maxInflight : au plus autant d'écritures d'hôte en cours ; au-delà, l'hôte est perdu, jamais la redirection.
const maxInflight = 32

// SetClickRecorder : posé par le module au câblage ; nil = filtre transparent.
func SetClickRecorder(r ClickRecorder) {
	if r == nil {
		recorder.Store(nil)
		return
	}
	recorder.Store(&r)
}

// WrapClicks entoure les routes de suivi des clics de l'upstream (/r/<jeton> et /visit, inchangées) : après la redirection, l'HÔTE du
// lien (jamais l'URL) est posé sur l'événement message.clicked que le déclencheur vient d'écrire. Un clic écarté par l'anti-bot de
// l'upstream n'a pas d'événement : rien n'est posé.
func WrapClicks(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		rec := recorder.Load()
		if rec == nil || r.Method != http.MethodGet {
			return
		}
		ws, mid, host := clickOf(r)
		if ws == "" || mid == "" || host == "" {
			return
		}
		if inflight.Add(1) > maxInflight {
			inflight.Add(-1)
			return
		}
		go func() {
			defer inflight.Add(-1)
			defer func() { _ = recover() }()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			(*rec)(ctx, ws, mid, host)
		}()
	})
}

// clickOf lit (espace, message, hôte) d'un clic suivi, comme l'upstream le lit.
func clickOf(r *http.Request) (ws, mid, host string) {
	switch {
	case r.URL.Path == "/visit":
		q := r.URL.Query()
		return q.Get("wid"), q.Get("mid"), HostOf(q.Get("url"))
	case strings.HasPrefix(r.URL.Path, "/r/"):
		tok := strings.TrimPrefix(r.URL.Path, "/r/")
		if tok == "" {
			return "", "", ""
		}
		dec, err := crypto.DecryptTrackingToken(tok)
		if err != nil {
			return "", "", ""
		}
		p := strings.SplitN(dec, "\n", 4)
		if len(p) != 4 {
			return "", "", ""
		}
		return p[1], p[0], HostOf(p[3])
	}
	return "", "", ""
}
