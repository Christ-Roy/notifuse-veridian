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
		WriteJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Restreindre aux API keys.
	userType, _ := r.Context().Value(domain.UserTypeKey).(string)
	if userType != string(domain.UserTypeAPIKey) {
		WriteJSONError(w, "this endpoint requires an API key", http.StatusForbidden)
		return
	}

	apiUserID, _ := r.Context().Value(domain.UserIDKey).(string)
	if apiUserID == "" {
		WriteJSONError(w, "missing user id in token", http.StatusUnauthorized)
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
		WriteJSONError(w, "failed to resolve workspace", http.StatusInternalServerError)
		return
	}
	if len(uws) == 0 {
		WriteJSONError(w, "api key not attached to any workspace", http.StatusForbidden)
		return
	}
	if len(uws) > 1 {
		WriteJSONError(w, "api key attached to multiple workspaces; ambiguous", http.StatusConflict)
		return
	}
	workspaceID := uws[0].WorkspaceID

	var input domain.MagicLinkInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if input.UserEmail == "" {
		WriteJSONError(w, "user_email is required", http.StatusBadRequest)
		return
	}

	resp, err := h.service.GenerateMagicLink(r.Context(), workspaceID, input.UserEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if errors.As(err, &notFound) {
			WriteJSONError(w, "user not found", http.StatusNotFound)
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"email":        input.UserEmail,
			"error":        err.Error(),
		}).Error("magic link: generation failed")
		WriteJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
