package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianSecurityHeadersMiddleware_CORSAllowlist_AllowedOriginEchoed(t *testing.T) {
	// Un Origin dans l'allowlist par défaut doit être renvoyé tel quel + ACAC:true.
	t.Setenv("CORS_ALLOW_ORIGIN", "")
	handler := VeridianSecurityHeadersMiddleware(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Origin", "https://notifuse.app.veridian.site")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://notifuse.app.veridian.site", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "Origin", w.Header().Get("Vary"))
}

func TestVeridianSecurityHeadersMiddleware_CORSAllowlist_DisallowedOriginNoACAO(t *testing.T) {
	// Un Origin hors allowlist ne doit PAS recevoir d'header ACAO ni ACAC.
	// C'est exactement le fix du pentest doomsday : pas de "* + credentials".
	t.Setenv("CORS_ALLOW_ORIGIN", "")
	handler := VeridianSecurityHeadersMiddleware(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"),
		"origin hors allowlist ne doit JAMAIS recevoir d'ACAO (fix CRITICAL pentest)")
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"),
		"ACAC ne doit pas être posé sans origin matchée")
}

func TestVeridianSecurityHeadersMiddleware_CORSAllowlist_NoOriginHeader(t *testing.T) {
	// Appel server-to-server (HMAC Hub, curl, etc.) sans header Origin.
	// On ne pose AUCUN header CORS — l'appel passe normalement.
	t.Setenv("CORS_ALLOW_ORIGIN", "")
	handler := VeridianSecurityHeadersMiddleware(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
}

func TestVeridianSecurityHeadersMiddleware_CORSAllowlist_CustomEnv(t *testing.T) {
	// L'env var CORS_ALLOW_ORIGIN (comma-separated) remplace le défaut.
	t.Setenv("CORS_ALLOW_ORIGIN", "https://custom.example.com,https://other.example.com")
	handler := VeridianSecurityHeadersMiddleware(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Origin", "https://custom.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, "https://custom.example.com", w.Header().Get("Access-Control-Allow-Origin"))

	// Le défaut notifuse.app.veridian.site ne doit PAS être autorisé quand
	// un override env est fourni (sinon l'env serait un additif, pas un remplacement).
	req2 := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req2.Header.Set("Origin", "https://notifuse.app.veridian.site")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	assert.Empty(t, w2.Header().Get("Access-Control-Allow-Origin"),
		"override env doit remplacer le défaut, pas l'augmenter")
}

func TestVeridianSecurityHeadersMiddleware_CORSAllowlist_StarValueIgnored(t *testing.T) {
	// `CORS_ALLOW_ORIGIN=*` est rejeté : on retombe sur les défauts.
	// C'est explicitement le bug qu'on corrige.
	t.Setenv("CORS_ALLOW_ORIGIN", "*")
	handler := VeridianSecurityHeadersMiddleware(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"),
		"valeur `*` doit être ignorée → fallback aux défauts → evil bloqué")
}

func TestVeridianSecurityHeadersMiddleware_CORSAllowlist_SuffixMatchBlocked(t *testing.T) {
	// Defense en profondeur : pas de match par suffix/prefix.
	// "https://evil.com/notifuse.app.veridian.site" ne doit PAS matcher
	// (bien sûr c'est un Origin invalide, mais on teste qu'on est strict).
	t.Setenv("CORS_ALLOW_ORIGIN", "")
	handler := VeridianSecurityHeadersMiddleware(noopHandler())

	for _, attack := range []string{
		"https://evil.com.notifuse.app.veridian.site",  // suffix attack
		"https://notifuse.app.veridian.site.evil.com",  // prefix attack
		"https://notifuse.app.veridian.site:8080",       // port différent
		"http://notifuse.app.veridian.site",             // scheme http vs https
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Origin", attack)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"),
			"attack origin %q ne doit PAS matcher", attack)
	}
}

func TestVeridianSecurityHeadersMiddleware_SecurityHeadersAlwaysSet(t *testing.T) {
	// Les 4 headers sécu sont posés sur TOUTES les requêtes, qu'il y ait
	// Origin ou pas, allowlist ou pas.
	t.Setenv("CORS_ALLOW_ORIGIN", "")
	handler := VeridianSecurityHeadersMiddleware(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, "max-age=15552000", w.Header().Get("Strict-Transport-Security"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "SAMEORIGIN", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))

	// Volontairement absente — on garde la liberté UI (CSP report-only à
	// faire plus tard si on veut durcir).
	assert.Empty(t, w.Header().Get("Content-Security-Policy"))
}

func TestVeridianSecurityHeadersMiddleware_OPTIONSPreflight(t *testing.T) {
	// Preflight CORS : OPTIONS → 200 sans appeler next handler.
	t.Setenv("CORS_ALLOW_ORIGIN", "")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	handler := VeridianSecurityHeadersMiddleware(next)

	req := httptest.NewRequest(http.MethodOptions, "/api/test", nil)
	req.Header.Set("Origin", "https://notifuse.app.veridian.site")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, called, "next handler ne doit PAS être appelé sur OPTIONS")
	assert.Equal(t, "https://notifuse.app.veridian.site", w.Header().Get("Access-Control-Allow-Origin"))
}

// noopHandler retourne un handler qui renvoie juste 200 OK. Helper pour les tests.
func noopHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// TestDefaultCORSAllowlist documente les domaines hardcodés par défaut.
// Si on en ajoute / retire, ce test sert de garde-fou de revue.
func TestDefaultCORSAllowlist_HardcodedDomains(t *testing.T) {
	if v, ok := os.LookupEnv("CORS_ALLOW_ORIGIN"); ok {
		t.Cleanup(func() { _ = os.Setenv("CORS_ALLOW_ORIGIN", v) })
	}
	_ = os.Unsetenv("CORS_ALLOW_ORIGIN")

	got := defaultCORSAllowlist()
	want := []string{
		"https://notifuse.app.veridian.site",
		"https://notifuse.staging.veridian.site",
		"http://localhost:3000",
		"http://localhost:5173",
	}
	assert.ElementsMatch(t, want, got, "allowlist par défaut a changé — vérifier que c'est intentionnel")
}
