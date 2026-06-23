package http

// === Veridian patch — Couche 4 Bounce OAuth Hub (CONTRAT-HUB §6bis.8) ===
//
// Handler POST /api/sso/issue-magic-link — endpoint contractuel cross-app
// appele par le Hub apres un OAuth Google/Microsoft reussi pour delivrer un
// magic link self-contained a l'user.
//
// Securite :
//   - HMAC Hub (X-Veridian-Hub-Signature) — meme middleware que les autres
//     endpoints Hub→Notifuse. Reject anti-replay > 5min.
//   - Le path /api/sso/* signale le domaine bounce OAuth (vs /api/tenants/*
//     pour le provisioning et /api/users/by-email pour la discovery).
//
// Semantique (§6bis.8.3) :
//   - 200 {"magic_link_url": "https://notifuse.app.veridian.site/veridian/auto-login?token=..."}
//   - 400 {"error": "user_not_in_app", "hint": "no workspace for this hub_user_id"}
//     (Hub redirige automatiquement vers signup-app)
//   - 401 si HMAC invalide / timestamp drift > 5min
//   - 500 erreur infra (DB, HUB_API_SECRET absent)
//
// Reutilise la logique magic_link Couche 3 via VeridianService.IssueMagicLinkForHub.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
)

// handleIssueMagicLink : POST /api/sso/issue-magic-link.
// Enregistree dans veridian_handler.go:RegisterRoutes sous HMAC.
//
// Body :
//
//	{"hub_user_id": "<uuid>", "email": "<string>"}
//
// Reponse 200 :
//
//	{"magic_link_url": "https://notifuse.app.veridian.site/veridian/auto-login?token=..."}
//
// Reponse 400 user_not_in_app :
//
//	{"error": "user_not_in_app", "code": "user_not_in_app", "details": {"hint": "..."}}
//
// Note format reponse 400 : le contrat impose un body avec champ `error`
// litteral "user_not_in_app" parce que le Hub parse exactement ce champ
// (cf. veridian-hub/lib/auth/bounce-apps.ts:228). Le format
// VeridianErrorResponse duplique error+code, ce qui reste compatible (le
// Hub lit `error`). On passe le code comme message → error sera
// "user_not_in_app".
func (h *VeridianHandler) handleIssueMagicLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HubUserID string `json:"hub_user_id"`
		Email     string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}

	hubUserID := strings.TrimSpace(body.HubUserID)
	email := strings.TrimSpace(body.Email)
	missing := missingFields(
		hubUserID == "", "hub_user_id",
		email == "", "email",
	)
	if len(missing) > 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "hub_user_id and email are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missing,
		})
		return
	}
	if !strings.Contains(email, "@") {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "email is invalid", http.StatusBadRequest, map[string]interface{}{
			"hint": "email must contain @",
		})
		return
	}

	resp, err := h.service.IssueMagicLinkForHub(r.Context(), domain.IssueMagicLinkInput{
		HubUserID: hubUserID,
		Email:     email,
	})
	if err != nil {
		// 400 user_not_in_app : sentinel ErrUserNotInApp → reponse contractuelle
		// avec champ `error` litteral "user_not_in_app" + hint dans details.
		if errors.Is(err, service.ErrUserNotInApp) {
			// PII : on ne log pas l email en clair, juste sa longueur.
			h.logError("issue_magic_link.user_not_in_app", err, map[string]interface{}{
				"hub_user_id": hubUserID,
				"email_len":   len(email),
			})
			WriteJSONErrorCode(w, ErrCodeUserNotInApp, "user_not_in_app", http.StatusBadRequest, map[string]interface{}{
				"hint": "no workspace for this hub_user_id",
			})
			return
		}
		// PII : pas d email en clair dans les logs.
		h.logError("issue_magic_link", err, map[string]interface{}{
			"hub_user_id": hubUserID,
			"email_len":   len(email),
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, veridianGenericInternalError, http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
