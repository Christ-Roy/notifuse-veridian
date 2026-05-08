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
	"html/template"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// === Veridian patch ===
// Logique HMAC token (BuildAutoLoginURL, VerifyAutoLoginToken,
// AutoLoginPayload, AutoLoginTokenTTL) extraite dans
// internal/domain/veridian_token.go pour partage avec le service. Voir la
// section "extraction" dans le commit f43ce239..718c976d.

// Re-exports pour minimiser le delta avec le code existant qui peut encore
// references domain.AutoLoginTokenTTL etc.
const AutoLoginTokenTTL = domain.AutoLoginTokenTTL

// AutoLoginPayload est un alias de type vers domain.AutoLoginPayload pour
// preserver les references existantes au sein du package http (handlers,
// tests). Toute nouvelle utilisation devrait directement importer le type
// domain.AutoLoginPayload.
type AutoLoginPayload = domain.AutoLoginPayload

// BuildAutoLoginURL est l'API publique exportee historiquement par ce
// package. Delegue au domain. Conserve pour compat avec d'autres callers
// qui pourraient l'avoir importe (ex: tests).
func BuildAutoLoginURL(apiEndpoint, hubSecret, workspaceID, email string) (string, time.Time, error) {
	return domain.BuildAutoLoginURL(apiEndpoint, hubSecret, workspaceID, email)
}

// VerifyAutoLoginToken est l'API publique historiquement exportee par
// ce package. Delegue au domain.
func VerifyAutoLoginToken(token, hubSecret string) (*AutoLoginPayload, error) {
	return domain.VerifyAutoLoginToken(token, hubSecret)
}

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
