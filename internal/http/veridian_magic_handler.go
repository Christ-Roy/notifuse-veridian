package http

// === Veridian patch ===
// Endpoint POST /api/workspaces.generateMagicLink protege par auth API
// key tenant Notifuse (PAS HMAC Hub).
//
// Cas d'usage : le Hub Veridian (cote serveur) appelle cet endpoint avec
// l'API key qu'il a recue lors du provisioning, pour obtenir un magic
// link cross-app pour un user du workspace. Le user_email doit deja
// exister et etre membre du workspace.
//
// Le workspace_id est INFERE de l'API key (l'API key Notifuse est
// rattachee a un user de type api_key membre d'un seul workspace).

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianMagicHandler regroupe le handler magic link.
type VeridianMagicHandler struct {
	service       domain.VeridianService
	workspaceRepo domain.WorkspaceRepository
	getJWTSecret  func() ([]byte, error)
	logger        logger.Logger
}

// NewVeridianMagicHandler construit le handler.
func NewVeridianMagicHandler(
	service domain.VeridianService,
	workspaceRepo domain.WorkspaceRepository,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) *VeridianMagicHandler {
	return &VeridianMagicHandler{
		service:       service,
		workspaceRepo: workspaceRepo,
		getJWTSecret:  getJWTSecret,
		logger:        log,
	}
}

// RegisterRoutes enregistre la route protegee par RequireAuth (JWT). Le
// JWT peut etre soit user soit api_key — on n'autorise ici que api_key
// pour eviter qu'un user humain genere des magic links pour autrui.
func (h *VeridianMagicHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()
	mux.Handle("/api/workspaces.generateMagicLink", requireAuth(http.HandlerFunc(h.handleGenerateMagicLink)))
}

func (h *VeridianMagicHandler) handleGenerateMagicLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSONErrorCode(w, ErrCodeMethodNotAllowed, "Method not allowed", http.StatusMethodNotAllowed, nil)
		return
	}

	// Restreindre aux API keys.
	userType, _ := r.Context().Value(domain.UserTypeKey).(string)
	if userType != string(domain.UserTypeAPIKey) {
		WriteJSONErrorCode(w, ErrCodeForbidden, "this endpoint requires an API key", http.StatusForbidden, nil)
		return
	}

	apiUserID, _ := r.Context().Value(domain.UserIDKey).(string)
	if apiUserID == "" {
		WriteJSONErrorCode(w, ErrCodeUnauthorized, "missing user id in token", http.StatusUnauthorized, nil)
		return
	}

	// Inferer le workspace depuis l'API key user. Une API key Notifuse
	// est rattachee a UN seul workspace ; si l'utilisateur a plusieurs
	// workspaces (cas non standard), on rejette pour eviter une
	// ambiguite silencieuse.
	uws, err := h.workspaceRepo.GetUserWorkspaces(r.Context(), apiUserID)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"api_user_id": apiUserID,
			"error":       err.Error(),
		}).Error("magic link: failed to load workspaces for api key")
		WriteJSONErrorCode(w, ErrCodeInternalError, "failed to resolve workspace", http.StatusInternalServerError, nil)
		return
	}
	if len(uws) == 0 {
		WriteJSONErrorCode(w, ErrCodeApiKeyNoWorkspace, "api key not attached to any workspace", http.StatusForbidden, nil)
		return
	}
	if len(uws) > 1 {
		WriteJSONErrorCode(w, ErrCodeApiKeyMultiWorkspace, "api key attached to multiple workspaces; ambiguous", http.StatusConflict, map[string]interface{}{
			"workspaces_count": len(uws),
		})
		return
	}
	workspaceID := uws[0].WorkspaceID

	// Borne le body (OWASP API4:2023 — Unrestricted Resource Consumption).
	// Cet endpoint JWT (API key) ne passe PAS par le middleware HMAC (qui
	// borne déjà à 1 MiB) : sans cap, le body serait illimité. Le payload
	// attendu est minuscule ({user_email}).
	var input domain.MagicLinkInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.UserEmail == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "user_email is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"user_email"},
		})
		return
	}

	resp, err := h.service.GenerateMagicLink(r.Context(), workspaceID, input.UserEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if errors.As(err, &notFound) {
			WriteJSONErrorCode(w, ErrCodeUserNotFound, "user not found", http.StatusNotFound, map[string]interface{}{
				"user_email": input.UserEmail,
			})
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"email":        input.UserEmail,
			"error":        err.Error(),
		}).Error("magic link: generation failed")
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
