package http

// === Veridian patch ===
// Handler POST + GET /api/users/by-email — pattern Hub discovery cross-app.
//
// Securite : HMAC Hub (meme middleware que les autres endpoints /api/tenants/*).
//
// Verbe HTTP :
//   - POST (body JSON `{"email": "..."}`) — initial, evite de logger l email en URL
//   - GET (querystring `?email=...`) — utilise par le Hub `lib/sync/discovery.ts`
//     (signature HMAC body vide, `${ts}.`) pour le cron reconcile cross-app.
//     Le passage en GET cote Hub a casse silencieusement la discovery en prod
//     (200 body vide via root_handler catchall) — cf. ticket
//     2026-05-25-discovery-by-email-prod-returns-empty-body.md.
//
// Use case : le Hub appelle cet endpoint au login user OU via cron reconcile
// pour decouvrir si l utilisateur a un compte Notifuse et quels workspaces il
// possede, sans dependre des colonnes denormalisees hub_app.tenants. Pattern
// "discovery pull" (CONTRAT-HUB 2026-05-20-hub-discovery-by-email-pattern.md).
//
// Semantique :
//   - 200 {"found": true, "workspaces": [...]}  si user connu
//   - 200 {"found": false, "workspaces": []}   si user inconnu (jamais 404)
//   - 400 si email manquant ou invalide
//   - 401 si signature HMAC invalide ou drift > 5min

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleDiscovery : POST /api/users/by-email.
// Enregistree dans veridian_handler.go:RegisterRoutes sous HMAC read-only.
//
// Body :
//
//	{"email": "alice@example.com"}
//
// Reponse 200 :
//
//	{
//	  "found": true,
//	  "user_email": "alice@example.com",
//	  "workspaces": [
//	    {
//	      "workspace_id": "ws-alice",
//	      "workspace_name": "Alice Corp",
//	      "role": "owner",
//	      "plan": "pro",
//	      "magic_link_capable": true,
//	      "fallback_url": "https://notifuse.app.veridian.site/console/signin"
//	    }
//	  ]
//	}
//
// Reponse found:false (200, pas 404) :
//
//	{"found": false, "user_email": "ghost@example.com", "workspaces": []}
func (h *VeridianHandler) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	h.runDiscovery(w, r, req.Email)
}

// handleDiscoveryGET : GET /api/users/by-email?email=...
// Variante GET pour le client Hub `lib/sync/discovery.ts`. La signature HMAC
// est calculee sur `${ts}.` (body vide) — le middleware HMAC accepte cela
// nativement (lit r.Body, qui est vide pour un GET).
func (h *VeridianHandler) handleDiscoveryGET(w http.ResponseWriter, r *http.Request) {
	h.runDiscovery(w, r, r.URL.Query().Get("email"))
}

// runDiscovery : logique partagee POST + GET.
func (h *VeridianHandler) runDiscovery(w http.ResponseWriter, r *http.Request, rawEmail string) {
	email := strings.TrimSpace(rawEmail)
	if email == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "email is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"email"},
		})
		return
	}
	if !strings.Contains(email, "@") {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "email is invalid", http.StatusBadRequest, map[string]interface{}{
			"hint": "email must contain @",
		})
		return
	}

	resp, err := h.service.LookupByEmail(r.Context(), email)
	if err != nil {
		// PII : on ne log pas l'email en clair — juste la longueur.
		h.logError("discovery_by_email", err, map[string]interface{}{
			"email_len": len(email),
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
