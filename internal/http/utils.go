package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
)

// WriteJSONError writes a JSON error response with the given message and status code.
// It sets the Content-Type header to application/json and automatically formats
// the response as {"error": "message"}.
func WriteJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}

// WriteAuthAwareError writes the right HTTP status for a service-layer
// error, classifying the two auth-related domain error types before
// falling back to the caller's own default (identical to today's
// behavior for anything that isn't one of them, so replacing a bare
// WriteJSONError(w, err.Error(), http.StatusInternalServerError) call with
// this helper can never make a genuine 500 WORSE — it can only turn a
// previously-mislabeled 500 into the correct 401/403).
//
// === Veridian patch — mesure prod 2026-10-03 (mission "API & agents") ===
// Mesuré en prod : une clé API scopée utilisée sur un AUTRE workspace, sur
// une route owner-only, ou après révocation recevait un 500 générique au
// lieu d'un 401/403 — l'accès était bien bloqué (zéro fuite de données),
// mais un agent/CLI scripté ne pouvait pas distinguer "clé révoquée"
// (401 : il faut en remettre une) de "pas les droits" (403 : inutile de
// réessayer) de "panne serveur réelle" (500 : à remonter). Les deux types
// ci-dessous sont posés UNE FOIS à la source (internal/service/auth_service.go
// AuthenticateUserForWorkspace) ; ce helper les traduit en status HTTP
// partout où un handler appelle ce chemin, sans toucher 46 fichiers à la
// main.
func WriteAuthAwareError(w http.ResponseWriter, err error, fallbackMessage string, fallbackStatus int) {
	var authErr *domain.ErrAuthenticationFailed
	if errors.As(err, &authErr) {
		WriteJSONError(w, authErr.Error(), http.StatusUnauthorized)
		return
	}
	var unauthorized *domain.ErrUnauthorized
	if errors.As(err, &unauthorized) {
		WriteJSONError(w, unauthorized.Error(), http.StatusForbidden)
		return
	}
	WriteJSONError(w, fallbackMessage, fallbackStatus)
}

// writeJSON writes a JSON response with the given status code and data.
// It sets the Content-Type header to application/json.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
