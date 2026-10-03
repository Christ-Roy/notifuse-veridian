package http

// === Veridian patch ===
// Handler du breakdown contacts par classe de provider (R1, ticket
// todo/2026-06-14-tunnel-vente-ui-controle-et-roadmap.md).
//
// Route POST + GET /api/veridian/contacts.providerBreakdown — auth JWT console
// (RequireAuth), gardien d'appartenance workspace + permission contacts:read
// dans le service.
//
// ⚠️ POST ET GET routés explicitement : Go 1.22+ exige la méthode dans le
// pattern. Un endpoint routé sur une seule méthode laisse l'autre tomber dans
// le catchall SPA de root_handler.go (HTML 200 trompeur, zéro log) — piège P0
// vécu 2026-05-25, cf. CLAUDE.md "Pièges historiques". POST sert le call
// applicatif (RPC-style Notifuse) ; GET sert l'appel direct UI/debug.
//
// Réponse :
//   {"breakdown":{"google":N,"microsoft":N,"yahoo_aol":N,"freemail_fr":N,"corporate":N},"total":N}

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianContactBreakdownHandler expose le breakdown contacts par classe.
type VeridianContactBreakdownHandler struct {
	service      domain.VeridianContactProviderBreakdownService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianContactBreakdownHandler construit le handler.
func NewVeridianContactBreakdownHandler(
	service domain.VeridianContactProviderBreakdownService,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) *VeridianContactBreakdownHandler {
	return &VeridianContactBreakdownHandler{
		service:      service,
		getJWTSecret: getJWTSecret,
		logger:       log,
	}
}

// RegisterRoutes enregistre POST + GET sur la même URL, protégés par RequireAuth.
func (h *VeridianContactBreakdownHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()
	handler := requireAuth(http.HandlerFunc(h.handleProviderBreakdown))
	mux.Handle("POST /api/veridian/contacts.providerBreakdown", handler)
	mux.Handle("GET /api/veridian/contacts.providerBreakdown", handler)
}

func (h *VeridianContactBreakdownHandler) handleProviderBreakdown(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseRequest(r)
	if err != nil {
		WriteJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	breakdown, err := h.service.GetProviderBreakdown(r.Context(), req)
	if err != nil {
		// === Veridian patch 2026-10-03 (mission "401/403 partout") ===
		// Remplace l'ancien double classement a la main (errors.As PermissionError
		// + isAuthFailure par prefixe de message) par la classification UNIQUE
		// partagee (classifyAuthError, internal/http/utils.go) : ErrAuthenticationFailed
		// -> 401, ErrUnauthorized/PermissionError -> 403, sinon le fallback 500
		// inchange. errors.As traverse tout fmt.Errorf("...: %w", err) du service.
		h.logger.WithField("error", err.Error()).Error("Failed to compute provider breakdown")
		WriteAuthAwareError(w, err, "Failed to compute provider breakdown", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, breakdown)
}

// parseRequest lit la requête depuis le body JSON (POST) ou la query (GET).
func (h *VeridianContactBreakdownHandler) parseRequest(r *http.Request) (*domain.VeridianProviderBreakdownRequest, error) {
	req := &domain.VeridianProviderBreakdownRequest{}

	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
		if err != nil {
			return nil, errors.New("failed to read request body")
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, req); err != nil {
				return nil, errors.New("invalid JSON body")
			}
		}
	}

	// La query string surcharge / complète (utile pour GET, et tolérant pour
	// un POST sans body).
	if v := r.URL.Query().Get("workspace_id"); v != "" {
		req.WorkspaceID = v
	}
	if v := r.URL.Query().Get("list_id"); v != "" {
		req.ListID = v
	}

	if req.WorkspaceID == "" {
		return nil, errors.New("workspace_id is required")
	}
	return req, nil
}

