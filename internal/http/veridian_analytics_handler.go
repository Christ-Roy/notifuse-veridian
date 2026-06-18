package http

// === Veridian patch ===
// Wrapper veridian des routes analytics (/api/analytics.query + /api/analytics.schemas).
//
// POURQUOI : le handler upstream internal/http/analytics_handler.go renvoie sur
// erreur un corps NON-STANDARD `{"error": true, "message": "..."}` (error =
// BOOLÉEN), alors que tout le reste de l'API Veridian/Notifuse renvoie
// `{"error": "<message string>"}` (cf. internal/http/utils.go:WriteJSONError).
// Cet écart a causé un faux affichage « ApiError: true » côté dashboard (le front
// lisait `errorData.error` comme un message). Le front est désormais durci, donc
// ce wrapper est du PUR alignement de cohérence API (ticket
// todo/2026-06-17-analytics-handler-error-shape-non-standard.md).
//
// CONVENTION : on ne patche JAMAIS le fichier upstream. Ce handler veridian
// REMPLACE l'enregistrement des routes analytics dans le mux (app.go route
// CE handler à la place de AnalyticsHandler.RegisterRoutes). Il réutilise le
// MÊME service domain.AnalyticsService que l'upstream — aucune logique métier
// dupliquée, juste la fine couche de transport HTTP + le bon error-shape.
//
// NON-RÉGRESSION : les réponses 200 sont STRICTEMENT identiques à l'upstream —
// handleQuery renvoie le *analytics.Response brut ; handleGetSchemas renvoie
// {"schemas": ...}. Le front consomme déjà cette forme (AnalyticsResponse direct
// pour la query). SEULES les erreurs changent de forme (booléen → string).
//
// ⚠️ Go 1.22+ method routing : le front ne fait que des POST sur ces routes.
// On route POST explicitement (l'upstream rejetait déjà non-POST en 405). Un GET
// tomberait dans le catchall SPA de root_handler.go (HTML 200 trompeur) — mais
// aucun caller ne fait de GET ici, et l'upstream ne l'autorisait pas non plus
// (405). On reste donc iso-comportement : POST routé, autres méthodes = pas de
// route dédiée (comme avant, le mux upstream matchait tout puis renvoyait 405 ;
// ici un non-POST tombe sur le catchall — différence inoffensive car non utilisée).

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianAnalyticsHandler enveloppe les routes analytics pour aligner le
// error-shape sur le standard {"error": "<string>"}.
type VeridianAnalyticsHandler struct {
	service      domain.AnalyticsService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianAnalyticsHandler construit le wrapper veridian. Il prend le MÊME
// service que le handler upstream (aucune nouvelle dépendance).
func NewVeridianAnalyticsHandler(
	service domain.AnalyticsService,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) *VeridianAnalyticsHandler {
	return &VeridianAnalyticsHandler{
		service:      service,
		getJWTSecret: getJWTSecret,
		logger:       log,
	}
}

// RegisterRoutes enregistre les routes analytics protégées par RequireAuth.
// REMPLACE AnalyticsHandler.RegisterRoutes (ne PAS enregistrer les deux : le mux
// paniquerait sur un pattern dupliqué).
func (h *VeridianAnalyticsHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()

	mux.Handle("POST /api/analytics.query", requireAuth(http.HandlerFunc(h.handleQuery)))
	mux.Handle("POST /api/analytics.schemas", requireAuth(http.HandlerFunc(h.handleGetSchemas)))
}

// handleQuery réplique la fine logique de l'upstream handleQuery, mais renvoie
// les erreurs au format standard {"error": "<string>"}. Le succès est identique
// (le *analytics.Response brut).
func (h *VeridianAnalyticsHandler) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req AnalyticsQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.WithField("error", err.Error()).Error("Failed to decode analytics query request")
		WriteJSONError(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	if req.WorkspaceID == "" {
		WriteJSONError(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	response, err := h.service.Query(r.Context(), req.WorkspaceID, req.Query)
	if err != nil {
		h.logger.WithField("workspace_id", req.WorkspaceID).WithField("error", err.Error()).Error("Analytics query failed")
		WriteJSONError(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// handleGetSchemas réplique la fine logique de l'upstream handleGetSchemas, avec
// le même error-shape standard. Le succès est identique ({"schemas": ...}).
func (h *VeridianAnalyticsHandler) handleGetSchemas(w http.ResponseWriter, r *http.Request) {
	var req AnalyticsSchemasRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.WithField("error", err.Error()).Error("Failed to decode analytics schemas request")
		WriteJSONError(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	if req.WorkspaceID == "" {
		WriteJSONError(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	schemas, err := h.service.GetSchemas(r.Context(), req.WorkspaceID)
	if err != nil {
		h.logger.WithField("workspace_id", req.WorkspaceID).WithField("error", err.Error()).Error("Failed to get analytics schemas")
		WriteJSONError(w, fmt.Sprintf("Failed to get schemas: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"schemas": schemas,
	})
}
