package http

import (
	"context"
	"testing"
	"time"
)

// Le hook par défaut ne fait rien ; SetVeridianOnLinkClick le remplace, et un nil est ignoré (la redirection
// continue sans enregistrement).
func TestSetVeridianOnLinkClick_AppelleLeHookBranche(t *testing.T) {
	defer func(prev func(context.Context, string, string, string, time.Time)) { veridianOnLinkClick = prev }(veridianOnLinkClick)

	var appels []string
	SetVeridianOnLinkClick(func(ctx context.Context, ws, mid, url string, at time.Time) {
		appels = append(appels, ws+"|"+mid+"|"+url)
	})
	veridianOnLinkClick(context.Background(), "ws1", "m1", "https://a.test/", time.Now())
	if len(appels) != 1 || appels[0] != "ws1|m1|https://a.test/" {
		t.Fatalf("hook branché non appelé comme attendu : %v", appels)
	}

	// Un hook nil est ignoré : le hook précédent reste branché, donc le second appel est encore enregistré.
	SetVeridianOnLinkClick(nil)
	veridianOnLinkClick(context.Background(), "ws1", "m2", "https://a.test/2", time.Now())
	if len(appels) != 2 || appels[1] != "ws1|m2|https://a.test/2" {
		t.Fatalf("un hook nil doit être ignoré et garder le précédent, appels = %v", appels)
	}
}
