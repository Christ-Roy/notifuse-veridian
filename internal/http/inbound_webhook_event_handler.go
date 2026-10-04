package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// InboundWebhookEventHandler handles HTTP requests for inbound webhook events
type InboundWebhookEventHandler struct {
	service      domain.InboundWebhookEventServiceInterface
	logger       logger.Logger
	getJWTSecret func() ([]byte, error)
}

// NewInboundWebhookEventHandler creates a new inbound webhook event handler
func NewInboundWebhookEventHandler(service domain.InboundWebhookEventServiceInterface, getJWTSecret func() ([]byte, error), logger logger.Logger) *InboundWebhookEventHandler {
	return &InboundWebhookEventHandler{
		service:      service,
		logger:       logger,
		getJWTSecret: getJWTSecret,
	}
}

// RegisterRoutes registers the inbound webhook event HTTP endpoints
func (h *InboundWebhookEventHandler) RegisterRoutes(mux *http.ServeMux) {
	// Create auth middleware
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()

	// Public webhooks endpoint for receiving events from email providers
	mux.Handle("/webhooks/email", http.HandlerFunc(h.handleIncomingWebhook))

	// Authenticated endpoints for accessing inbound webhook event data
	mux.Handle("/api/inboundWebhookEvents.list", requireAuth(http.HandlerFunc(h.handleList)))
}

// handleIncomingWebhook handles incoming webhook events from email providers
func (h *InboundWebhookEventHandler) handleIncomingWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract provider, workspace_id and integration_id from query parameters
	// Format: /webhooks/email?provider={provider}&workspace_id={id}&integration_id={id}
	provider := r.URL.Query().Get("provider")
	workspaceID := r.URL.Query().Get("workspace_id")
	integrationID := r.URL.Query().Get("integration_id")

	if provider == "" {
		WriteJSONError(w, "Provider is required", http.StatusBadRequest)
		return
	}

	if workspaceID == "" || integrationID == "" {
		WriteJSONError(w, "Workspace ID and integration ID are required", http.StatusBadRequest)
		return
	}

	// Veridian fork — durcissement 2026-10-04 (audit sécurité) : ce point
	// d'entrée est PUBLIC et recevait n'importe quel POST forgé sans aucune
	// vérification (preuve d'audit : ?provider=smtp&workspace_id=...&
	// integration_id=... basculait des contacts en bounced via
	// MarkEmailsAsBounced). L'authentification (secret par intégration,
	// comparé en temps constant, + signature officielle du fournisseur
	// quand elle est vérifiable) vit dans le service, AVANT tout parsing
	// provider et AVANT toute écriture : voir
	// InboundWebhookEventService.ProcessWebhook / authenticateInboundWebhook.
	// L'appelant INTERNE (relais Postfix / poller IMAP, cf
	// VeridianBounceConsumer) appelle ProcessWebhook directement en Go,
	// jamais par HTTP : il n'est pas concerné par cette porte et continue de
	// fonctionner à l'identique.
	secret := r.URL.Query().Get("secret")

	h.logger.WithField("provider", provider).
		WithField("workspace_id", workspaceID).
		WithField("integration_id", integrationID).
		Info("Received webhook event")

	// Read and parse the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.WithField("error", err.Error()).Error("Failed to read webhook request body")
		WriteJSONError(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Process the webhook event (authentication happens first, inside the service)
	err = h.service.ProcessWebhook(r.Context(), workspaceID, integrationID, body, domain.InboundWebhookAuth{
		Secret:  secret,
		Headers: r.Header,
	})
	if errors.Is(err, domain.ErrWebhookUnauthorized) {
		// Pas de détail (quelle partie a échoué, quel secret était attendu) :
		// une 401 bavarde aiderait à deviner le bon secret par essais.
		h.logger.WithField("workspace_id", workspaceID).
			WithField("integration_id", integrationID).
			WithField("provider", provider).
			Warn("Rejected unauthenticated inbound webhook")
		WriteJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if err != nil {
		h.logger.WithField("error", err.Error()).
			WithField("workspace_id", workspaceID).
			WithField("integration_id", integrationID).
			WithField("provider", provider).
			Error("Failed to process webhook")
		WriteJSONError(w, "Failed to process webhook", http.StatusBadRequest)
		return
	}

	// Return success
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// handleList handles requests to list inbound webhook events by type
func (h *InboundWebhookEventHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters into InboundWebhookEventListParams
	params := domain.InboundWebhookEventListParams{}
	if err := params.FromQuery(r.URL.Query()); err != nil {
		h.logger.WithField("error", err.Error()).
			Error("Invalid inbound webhook event list parameters")
		WriteJSONError(w, "Invalid parameters: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Call the service to list events
	result, err := h.service.ListEvents(r.Context(), params.WorkspaceID, params)
	if err != nil {
		h.logger.WithField("error", err.Error()).
			WithField("workspace_id", params.WorkspaceID).
			Error("Failed to list inbound webhook events")
		WriteAuthAwareError(w, err, "Failed to list inbound webhook events", http.StatusInternalServerError)
		return
	}

	// Return the results
	writeJSON(w, http.StatusOK, result)
}
