package http

// === Veridian patch ===
// VeridianAutoLoginHandler — endpoint GET /veridian/auto-login?token=<HMAC>
// qui transforme un token HMAC signé par le Hub en session Notifuse + JWT,
// puis renvoie une page HTML inline qui stocke l'auth_token dans localStorage
// (Notifuse frontend natif lit l'auth depuis localStorage, pas un cookie)
// et redirect vers /console/{workspace_id}.
//
// Le flow :
//   1. Hub appelle Provision (HMAC) → reçoit `auto_login_url` self-contained
//   2. Hub redirect le user sur cette URL
//   3. Cet endpoint vérifie le token HMAC, crée une session JWT
//   4. Page HTML stocke le token + redirect — user est immédiatement loggé
//
// Sécurité :
//   - Token HMAC signé avec HUB_API_SECRET (même secret que /api/tenants/*)
//   - TTL court (60s) → minimal window pour replay si l'URL fuit
//   - Une fois consommé, redirect 302 — l'URL avec token n'apparaît pas dans
//     l'historique navigateur du user (replace cote frontend)
//
// Le seul endpoint Veridian non-protégé par middleware HMAC standard car le
// token EST l'auth (signature dans l'URL, pas dans les headers).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// AutoLoginTokenTTL est la fenetre temporelle pendant laquelle un token
// auto-login est valide. Court (60s) pour limiter les replays si l'URL
// avec token fuit (ex: log proxy, history navigateur).
const AutoLoginTokenTTL = 60 * time.Second

// VeridianAutoLoginHandler regroupe les helpers + handler GET /veridian/auto-login.
type VeridianAutoLoginHandler struct {
	userService domain.UserServiceInterface
	hubSecret   string
	apiEndpoint string
	logger      logger.Logger
}

// NewVeridianAutoLoginHandler construit un handler. hubSecret = HUB_API_SECRET.
func NewVeridianAutoLoginHandler(
	userService domain.UserServiceInterface,
	hubSecret, apiEndpoint string,
	log logger.Logger,
) *VeridianAutoLoginHandler {
	return &VeridianAutoLoginHandler{
		userService: userService,
		hubSecret:   hubSecret,
		apiEndpoint: apiEndpoint,
		logger:      log,
	}
}

// RegisterRoutes attache le handler au mux racine.
func (h *VeridianAutoLoginHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /veridian/auto-login", h.handleAutoLogin)
}

// AutoLoginPayload est le contenu signe HMAC contenu dans le token URL.
type AutoLoginPayload struct {
	WorkspaceID string `json:"w"`
	Email       string `json:"e"`
	IssuedAt    int64  `json:"i"` // unix ms
	ExpiresAt   int64  `json:"x"` // unix ms
}

// BuildAutoLoginURL genere une URL self-contained `<API_ENDPOINT>/veridian/auto-login?token=<base64(payload).<hex(hmac)>`.
// Utilise par VeridianService.Provision et GenerateMagicLink.
func BuildAutoLoginURL(apiEndpoint, hubSecret, workspaceID, email string) (string, time.Time, error) {
	if hubSecret == "" {
		return "", time.Time{}, fmt.Errorf("HUB_API_SECRET not configured")
	}
	now := time.Now()
	expiresAt := now.Add(AutoLoginTokenTTL)
	payload := AutoLoginPayload{
		WorkspaceID: workspaceID,
		Email:       email,
		IssuedAt:    now.UnixMilli(),
		ExpiresAt:   expiresAt.UnixMilli(),
	}
	rawJSON, err := json.Marshal(payload)
	if err != nil {
		return "", time.Time{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(rawJSON)
	mac := hmac.New(sha256.New, []byte(hubSecret))
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := encoded + "." + sig

	url := strings.TrimRight(apiEndpoint, "/") + "/veridian/auto-login?token=" + token
	return url, expiresAt, nil
}

// VerifyAutoLoginToken parse + verifie le token. Retourne le payload si valide.
func VerifyAutoLoginToken(token, hubSecret string) (*AutoLoginPayload, error) {
	if token == "" {
		return nil, fmt.Errorf("token required")
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed token")
	}
	encoded, sigHex := parts[0], parts[1]

	// Verify HMAC
	expectedMAC := hmac.New(sha256.New, []byte(hubSecret))
	expectedMAC.Write([]byte(encoded))
	expectedSig := expectedMAC.Sum(nil)
	providedSig, err := hex.DecodeString(sigHex)
	if err != nil {
		return nil, fmt.Errorf("invalid signature encoding")
	}
	if !hmac.Equal(expectedSig, providedSig) {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode payload
	rawJSON, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid payload encoding")
	}
	var payload AutoLoginPayload
	if err := json.Unmarshal(rawJSON, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload JSON")
	}

	// Verify expiration
	now := time.Now().UnixMilli()
	if now > payload.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}
	if now < payload.IssuedAt-int64(AutoLoginTokenTTL.Milliseconds()) {
		// Defense en profondeur : token avec issued_at dans le futur (clock skew > TTL)
		return nil, fmt.Errorf("token issued in the future")
	}
	if payload.WorkspaceID == "" || payload.Email == "" {
		return nil, fmt.Errorf("token missing workspace_id or email")
	}
	return &payload, nil
}

// autoLoginPageTemplate est la page HTML inline qui stocke le JWT dans
// localStorage et redirect immediatement vers /console.
//
// On utilise location.replace au lieu de location.href pour pas laisser
// le token dans l'historique navigateur (clic Back).
var autoLoginPageTemplate = template.Must(template.New("autologin").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Signing you in…</title>
  <meta name="robots" content="noindex,nofollow">
  <meta http-equiv="X-Frame-Options" content="DENY">
  <style>
    body { font-family: -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;
           display:flex; align-items:center; justify-content:center; min-height:100vh;
           margin:0; background:#0f172a; color:#cbd5e1; }
    .box { text-align:center; padding:24px 32px; border-radius:8px; background:#1e293b; }
    .spinner { display:inline-block; width:28px; height:28px; border:3px solid #334155;
               border-top-color:#3b82f6; border-radius:50%; animation:spin 0.8s linear infinite; }
    @keyframes spin { to { transform: rotate(360deg); } }
    p { margin:16px 0 0; font-size:14px; }
    noscript { color:#fbbf24; }
  </style>
</head>
<body>
  <div class="box">
    <div class="spinner"></div>
    <p>Signing you in…</p>
    <noscript>JavaScript required for sign-in.</noscript>
  </div>
  <script>
    (function() {
      try {
        localStorage.setItem('auth_token', {{ .Token }});
      } catch (e) {
        document.querySelector('.box p').textContent = 'localStorage unavailable. Cannot sign in.';
        return;
      }
      window.location.replace({{ .RedirectURL }});
    })();
  </script>
</body>
</html>`))

// handleAutoLogin est le endpoint GET /veridian/auto-login?token=...
func (h *VeridianAutoLoginHandler) handleAutoLogin(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		h.respondError(w, "Missing token", http.StatusBadRequest)
		return
	}
	if h.hubSecret == "" {
		h.logger.Error("VeridianAutoLogin: HUB_API_SECRET not configured")
		h.respondError(w, "Service misconfigured", http.StatusServiceUnavailable)
		return
	}

	payload, err := VerifyAutoLoginToken(token, h.hubSecret)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error": err.Error(),
		}).Warn("VeridianAutoLogin: invalid token")
		h.respondError(w, "Invalid or expired sign-in link", http.StatusUnauthorized)
		return
	}

	// Crée la session + JWT pour le user (doit déjà exister, créé au Provision)
	authResp, err := h.userService.CreateAutoLoginSession(r.Context(), payload.Email)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"email":        payload.Email,
			"workspace_id": payload.WorkspaceID,
			"error":        err.Error(),
		}).Error("VeridianAutoLogin: failed to create session")
		h.respondError(w, "Sign-in failed: user not found", http.StatusUnauthorized)
		return
	}

	// Page HTML qui stocke le token et redirect.
	// Notifuse frontend route uniquement /console (single page app), pas
	// /console/{workspace_id}. On redirect direct vers /console et le
	// frontend pickera le workspace courant via API GET /api/user.me
	// (qui retourne la liste des workspaces du user).
	redirectURL := "/console"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(http.StatusOK)

	if err := autoLoginPageTemplate.Execute(w, map[string]interface{}{
		"Token":       authResp.Token,
		"RedirectURL": redirectURL,
	}); err != nil {
		h.logger.WithField("error", err.Error()).Error("VeridianAutoLogin: template execute failed")
	}
}

func (h *VeridianAutoLoginHandler) respondError(w http.ResponseWriter, msg string, status int) {
	// On retourne du HTML plutot que JSON car cet endpoint est consommé par
	// un navigateur (redirect target). Page minimale lisible humaine.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	body := `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Sign-in error</title>
<style>body{font-family:sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:#1e293b;color:#fbbf24;}
.box{text-align:center;padding:24px 32px;background:#0f172a;border-radius:8px;}
a{color:#3b82f6;}</style></head><body><div class="box"><h2>Sign-in error</h2><p>` +
		template.HTMLEscapeString(msg) +
		`</p><p><a href="/console/signin">Try again</a></p></div></body></html>`
	_, _ = w.Write([]byte(body))
}
