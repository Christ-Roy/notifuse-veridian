package middleware

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/ratelimiter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// okHandler : handler aval qui répond 200 et compte ses invocations.
func okHandler(hits *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			*hits++
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// makeBearer forge un JWT non signé (header.payload.sig) portant un user_id.
// Le middleware ne valide PAS la signature, seul le payload compte.
func makeBearer(t *testing.T, userID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(map[string]string{"user_id": userID, "type": "user"})
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	sig := base64.RawURLEncoding.EncodeToString([]byte("fake-signature"))
	return "Bearer " + header + "." + payload + "." + sig
}

// newTestMiddleware construit le middleware avec une config explicite + un
// limiter neuf (stoppé en cleanup pour éviter la fuite de goroutine).
func newTestMiddleware(t *testing.T, cfg VeridianAPIRateLimitConfig, next http.Handler) http.Handler {
	t.Helper()
	rl := ratelimiter.NewRateLimiter()
	t.Cleanup(rl.Stop)
	return VeridianAPIRateLimitMiddleware(rl, cfg, logger.NewLogger())(next)
}

func defaultTestConfig() VeridianAPIRateLimitConfig {
	return VeridianAPIRateLimitConfig{
		Enabled:     true,
		PerIP:       3,
		PerIdentity: 100, // large pour isoler la dimension IP
		Window:      time.Minute,
	}
}

func doReq(t *testing.T, h http.Handler, method, path, ip, auth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- 429 après dépassement + Retry-After présent (dimension IP) ---

func TestVeridianAPIRateLimit_429AfterLimit_PerIP(t *testing.T) {
	hits := 0
	h := newTestMiddleware(t, defaultTestConfig(), okHandler(&hits))

	// 3 requêtes autorisées (PerIP=3).
	for i := 0; i < 3; i++ {
		rec := doReq(t, h, http.MethodPost, "/api/contacts.list", "1.2.3.4", "")
		require.Equal(t, http.StatusOK, rec.Code, "req %d should pass", i)
	}

	// 4e : bloquée.
	rec := doReq(t, h, http.MethodPost, "/api/contacts.list", "1.2.3.4", "")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, 3, hits, "downstream handler ne doit pas être appelé sur le 429")

	// Retry-After présent et entier positif.
	ra := rec.Header().Get("Retry-After")
	require.NotEmpty(t, ra, "Retry-After doit être présent sur un 429")
	n, err := strconv.Atoi(ra)
	require.NoError(t, err)
	assert.Greater(t, n, 0)

	// Body JSON avec code rate_limited.
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "rate_limited", body["code"])
}

// --- Deux IP distinctes ont des buckets indépendants ---

func TestVeridianAPIRateLimit_PerIP_Isolated(t *testing.T) {
	h := newTestMiddleware(t, defaultTestConfig(), okHandler(nil))

	// IP A sature (3 ok + 1 bloqué).
	for i := 0; i < 3; i++ {
		require.Equal(t, http.StatusOK, doReq(t, h, http.MethodPost, "/api/x.y", "10.0.0.1", "").Code)
	}
	assert.Equal(t, http.StatusTooManyRequests,
		doReq(t, h, http.MethodPost, "/api/x.y", "10.0.0.1", "").Code)

	// IP B intacte.
	assert.Equal(t, http.StatusOK,
		doReq(t, h, http.MethodPost, "/api/x.y", "10.0.0.2", "").Code)
}

// --- Deux identités distinctes ont des buckets indépendants ---
// (même IP, users différents → la dimension identité les sépare.)

func TestVeridianAPIRateLimit_PerIdentity_Distinct(t *testing.T) {
	cfg := VeridianAPIRateLimitConfig{
		Enabled:     true,
		PerIP:       1000, // large pour isoler la dimension identité
		PerIdentity: 2,
		Window:      time.Minute,
	}
	h := newTestMiddleware(t, cfg, okHandler(nil))

	userA := makeBearer(t, "user-aaaa")
	userB := makeBearer(t, "user-bbbb")
	const ip = "9.9.9.9"

	// userA : 2 ok puis 429 (même IP).
	require.Equal(t, http.StatusOK, doReq(t, h, http.MethodPost, "/api/contacts.list", ip, userA).Code)
	require.Equal(t, http.StatusOK, doReq(t, h, http.MethodPost, "/api/contacts.list", ip, userA).Code)
	assert.Equal(t, http.StatusTooManyRequests,
		doReq(t, h, http.MethodPost, "/api/contacts.list", ip, userA).Code)

	// userB sur la MÊME IP : intact (bucket identité séparé, IP large).
	assert.Equal(t, http.StatusOK,
		doReq(t, h, http.MethodPost, "/api/contacts.list", ip, userB).Code)
}

// --- Exemptions : health / version / tenants health jamais bloqués ---

func TestVeridianAPIRateLimit_ExemptPaths(t *testing.T) {
	cfg := VeridianAPIRateLimitConfig{Enabled: true, PerIP: 1, PerIdentity: 1, Window: time.Minute}
	h := newTestMiddleware(t, cfg, okHandler(nil))

	exempt := []string{
		"/api/health",
		"/api/version",
		"/api/tenants/abc-123/health",
	}
	for _, p := range exempt {
		// Bien au-delà de la limite (PerIP=1) : doit TOUJOURS passer.
		for i := 0; i < 5; i++ {
			rec := doReq(t, h, http.MethodGet, p, "5.5.5.5", "")
			require.Equal(t, http.StatusOK, rec.Code, "%s req %d doit rester exempté", p, i)
		}
	}
}

// --- Exemption HMAC Hub : X-Veridian-Hub-Signature → jamais bloqué ---

func TestVeridianAPIRateLimit_ExemptHMAC(t *testing.T) {
	cfg := VeridianAPIRateLimitConfig{Enabled: true, PerIP: 1, PerIdentity: 1, Window: time.Minute}
	h := newTestMiddleware(t, cfg, okHandler(nil))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/users/by-email?email=a@b.com", nil)
		req.Header.Set("X-Forwarded-For", "6.6.6.6")
		req.Header.Set("X-Veridian-Hub-Signature", "deadbeef")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "HMAC Hub req %d doit rester exempté", i)
	}
}

// --- Non-API jamais inspecté (console, assets, /subscribe, /health racine) ---

func TestVeridianAPIRateLimit_NonAPIPassthrough(t *testing.T) {
	cfg := VeridianAPIRateLimitConfig{Enabled: true, PerIP: 1, PerIdentity: 1, Window: time.Minute}
	h := newTestMiddleware(t, cfg, okHandler(nil))

	nonAPI := []string{"/", "/console/assets/app.js", "/subscribe", "/preferences", "/health", "/healthz"}
	for _, p := range nonAPI {
		for i := 0; i < 5; i++ {
			rec := doReq(t, h, http.MethodGet, p, "7.7.7.7", "")
			require.Equal(t, http.StatusOK, rec.Code, "%s doit passer sans rate-limit", p)
		}
	}
}

// --- Désactivation par config → passthrough total ---

func TestVeridianAPIRateLimit_DisabledPassthrough(t *testing.T) {
	cfg := VeridianAPIRateLimitConfig{Enabled: false, PerIP: 1, PerIdentity: 1, Window: time.Minute}
	h := newTestMiddleware(t, cfg, okHandler(nil))

	for i := 0; i < 10; i++ {
		rec := doReq(t, h, http.MethodPost, "/api/contacts.list", "8.8.8.8", "")
		require.Equal(t, http.StatusOK, rec.Code, "désactivé : req %d doit passer", i)
	}
}

// --- rl == nil → passthrough total (best-effort, jamais de panic) ---

func TestVeridianAPIRateLimit_NilLimiterPassthrough(t *testing.T) {
	hits := 0
	h := VeridianAPIRateLimitMiddleware(nil, defaultTestConfig(), logger.NewLogger())(okHandler(&hits))

	for i := 0; i < 10; i++ {
		rec := doReq(t, h, http.MethodPost, "/api/contacts.list", "8.8.8.8", "")
		require.Equal(t, http.StatusOK, rec.Code)
	}
	assert.Equal(t, 10, hits)
}

// --- Identité absente → seule la dimension IP s'applique (pas de panic, pas
//     de bucket identité consommé) ---

func TestVeridianAPIRateLimit_NoIdentity_IPOnly(t *testing.T) {
	cfg := VeridianAPIRateLimitConfig{Enabled: true, PerIP: 2, PerIdentity: 1, Window: time.Minute}
	h := newTestMiddleware(t, cfg, okHandler(nil))

	// Pas d'Authorization : PerIdentity=1 NE doit PAS s'appliquer (sinon
	// blocage au 2e). Seul PerIP=2 borne → 2 ok puis 429.
	require.Equal(t, http.StatusOK, doReq(t, h, http.MethodPost, "/api/x.y", "3.3.3.3", "").Code)
	require.Equal(t, http.StatusOK, doReq(t, h, http.MethodPost, "/api/x.y", "3.3.3.3", "").Code)
	assert.Equal(t, http.StatusTooManyRequests,
		doReq(t, h, http.MethodPost, "/api/x.y", "3.3.3.3", "").Code)
}

// --- Extraction user_id depuis Bearer (best-effort, sans validation) ---

func TestVeridianUserIDFromBearer(t *testing.T) {
	tests := []struct {
		name string
		hdr  string
		want string
	}{
		{"empty", "", ""},
		{"not bearer", "Basic abc", ""},
		{"malformed jwt 2 segments", "Bearer aaa.bbb", ""},
		{"garbage payload", "Bearer aaa.!!!!.ccc", ""},
		{"valid", makeBearerStr(t, "user-xyz"), "user-xyz"},
		{"bearer case-insensitive", "bearer " + bearerToken(t, "u2"), "u2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, veridianUserIDFromBearer(tc.hdr))
		})
	}
}

func bearerToken(t *testing.T, uid string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
	pb, _ := json.Marshal(map[string]string{"user_id": uid})
	payload := base64.RawURLEncoding.EncodeToString(pb)
	return header + "." + payload + ".sig"
}

func makeBearerStr(t *testing.T, uid string) string {
	t.Helper()
	return "Bearer " + bearerToken(t, uid)
}

// --- Identité HMAC app caller (sans header Hub-Signature) ---

func TestVeridianRateLimitIdentity_App(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/foo?workspace_id=ws-1", nil)
	req.Header.Set("x-veridian-app", "prospection")
	assert.Equal(t, "app:prospection:ws-1", veridianRateLimitIdentity(req))

	// Sans workspace.
	req2 := httptest.NewRequest(http.MethodPost, "/api/foo", nil)
	req2.Header.Set("x-veridian-app", "cms")
	assert.Equal(t, "app:cms:", veridianRateLimitIdentity(req2))

	// Rien.
	req3 := httptest.NewRequest(http.MethodGet, "/api/foo", nil)
	assert.Equal(t, "", veridianRateLimitIdentity(req3))
}

// --- Extraction IP : XFF premier hop > X-Real-IP > RemoteAddr ---

func TestVeridianRateLimitClientIP(t *testing.T) {
	r1 := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r1.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	assert.Equal(t, "1.1.1.1", veridianRateLimitClientIP(r1))

	r2 := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r2.Header.Set("X-Real-IP", "3.3.3.3")
	assert.Equal(t, "3.3.3.3", veridianRateLimitClientIP(r2))

	r3 := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r3.RemoteAddr = "4.4.4.4:5678"
	assert.Equal(t, "4.4.4.4", veridianRateLimitClientIP(r3))
}

// --- LoadVeridianAPIRateLimitConfig : défauts + override + valeurs invalides ---

func TestLoadVeridianAPIRateLimitConfig(t *testing.T) {
	// Défauts (env vidé).
	t.Setenv("VERIDIAN_API_RATE_LIMIT_ENABLED", "")
	t.Setenv("VERIDIAN_API_RATE_LIMIT_PER_IP", "")
	t.Setenv("VERIDIAN_API_RATE_LIMIT_PER_IDENTITY", "")
	cfg := LoadVeridianAPIRateLimitConfig()
	assert.True(t, cfg.Enabled)
	assert.Equal(t, veridianRateLimitDefaultPerIP, cfg.PerIP)
	assert.Equal(t, veridianRateLimitDefaultPerIdentity, cfg.PerIdentity)

	// Override valide.
	t.Setenv("VERIDIAN_API_RATE_LIMIT_PER_IP", "42")
	t.Setenv("VERIDIAN_API_RATE_LIMIT_PER_IDENTITY", "84")
	cfg = LoadVeridianAPIRateLimitConfig()
	assert.Equal(t, 42, cfg.PerIP)
	assert.Equal(t, 84, cfg.PerIdentity)

	// Valeurs invalides → on garde le défaut (best-effort).
	t.Setenv("VERIDIAN_API_RATE_LIMIT_PER_IP", "abc")
	t.Setenv("VERIDIAN_API_RATE_LIMIT_PER_IDENTITY", "-5")
	cfg = LoadVeridianAPIRateLimitConfig()
	assert.Equal(t, veridianRateLimitDefaultPerIP, cfg.PerIP)
	assert.Equal(t, veridianRateLimitDefaultPerIdentity, cfg.PerIdentity)

	// Désactivation explicite.
	t.Setenv("VERIDIAN_API_RATE_LIMIT_ENABLED", "false")
	cfg = LoadVeridianAPIRateLimitConfig()
	assert.False(t, cfg.Enabled)

	// "0" désactive aussi.
	t.Setenv("VERIDIAN_API_RATE_LIMIT_ENABLED", "0")
	cfg = LoadVeridianAPIRateLimitConfig()
	assert.False(t, cfg.Enabled)
}

func TestVeridianAPIRateLimitConfig_String(t *testing.T) {
	assert.Equal(t, "disabled", VeridianAPIRateLimitConfig{Enabled: false}.String())
	s := VeridianAPIRateLimitConfig{Enabled: true, PerIP: 10, PerIdentity: 20}.String()
	assert.Contains(t, s, "per_ip=10")
	assert.Contains(t, s, "per_identity=20")
}
