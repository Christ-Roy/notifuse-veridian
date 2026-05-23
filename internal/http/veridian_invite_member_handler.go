package http

// === Veridian patch — Hub invitation flow (2026-05-23) ===
// Handler `POST /api/veridian/workspaces.inviteMember` qui delegue
// l'invitation cross-app au Hub Veridian au lieu de creer une invitation
// locale (notifuse_invitations).
//
// Flow :
//   1. Auth JWT (requireAuth) — recupere user_id du caller (owner Notifuse)
//   2. AuthService.AuthenticateUserForWorkspace — verifie que le caller a
//      bien acces au workspace cible (membership check). Renvoie *User avec
//      `HubUserID *string` (charge depuis V46 par user_postgres).
//   3. Resout l'inviter_user_id Hub : user.HubUserID si non-nil, sinon
//      fallback lookup directe sur DB (defense in depth si le SELECT du
//      repo a oublie le hub_user_id dans le futur).
//   4. Call hub `POST /api/invitations/create` via VeridianHubInvitationClient
//   5. Map les erreurs Hub vers codes HTTP propres
//   6. Retourne {status, message, hub_invitation_id, magic_link_url, expires_at, reused}
//
// Pourquoi un nouvel endpoint et pas patcher /api/workspaces.inviteMember ?
// Convention Veridian zero-upstream-patch : on cree un endpoint dedie. Le
// front detecte `mode === 'veridian-managed'` (via /api/veridian/mode) et
// branche sur cet endpoint plutot que sur la route upstream.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// HubUserIDResolver lookup `users.hub_user_id` pour un user Notifuse. Permet
// de mocker la lookup DB en test sans setup sqlmock complet. La sentinel
// `sql.ErrNoRows` doit etre retournee si le user n'existe pas — le handler
// renvoie alors 404 (le user_id du token ne correspond a aucun user en DB).
// Si `hub_user_id` est NULL (V46 row non backfillee), retourne `("", nil)`.
type HubUserIDResolver interface {
	GetHubUserID(ctx context.Context, userID string) (string, error)
}

// sqlHubUserIDResolver implementation concrete sur *sql.DB.
type sqlHubUserIDResolver struct {
	db *sql.DB
}

// NewSQLHubUserIDResolver wrappe a.db.
func NewSQLHubUserIDResolver(db *sql.DB) HubUserIDResolver {
	return &sqlHubUserIDResolver{db: db}
}

func (r *sqlHubUserIDResolver) GetHubUserID(ctx context.Context, userID string) (string, error) {
	var hubUserID sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT hub_user_id FROM users WHERE id = $1`, userID).Scan(&hubUserID)
	if err != nil {
		return "", err
	}
	if hubUserID.Valid {
		return hubUserID.String, nil
	}
	return "", nil
}

// VeridianInviteMemberHandler regroupe les dependances du handler.
type VeridianInviteMemberHandler struct {
	hubClient        service.HubInvitationClient
	authService      domain.AuthService
	userService      domain.UserServiceInterface
	hubUserResolver  HubUserIDResolver
	getJWTSecret     func() ([]byte, error)
	logger           logger.Logger
	// managedMode true => endpoint actif. False => 503 (mode self-hosted, pas de Hub).
	managedMode bool
}

// NewVeridianInviteMemberHandler construit le handler.
//
// managedMode = (config.HubAPISecret != "") — meme conditionnement que le
// reste des endpoints Veridian. Si false, le handler renvoie 503 sur tous
// les POST.
func NewVeridianInviteMemberHandler(
	hubClient service.HubInvitationClient,
	authService domain.AuthService,
	userService domain.UserServiceInterface,
	hubUserResolver HubUserIDResolver,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
	managedMode bool,
) *VeridianInviteMemberHandler {
	return &VeridianInviteMemberHandler{
		hubClient:       hubClient,
		authService:     authService,
		userService:     userService,
		hubUserResolver: hubUserResolver,
		getJWTSecret:    getJWTSecret,
		logger:          log,
		managedMode:     managedMode,
	}
}

// RegisterRoutes enregistre `POST /api/veridian/workspaces.inviteMember`
// wrappe dans le middleware JWT requireAuth (claims user => owner Notifuse).
func (h *VeridianInviteMemberHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()
	mux.Handle("/api/veridian/workspaces.inviteMember", requireAuth(http.HandlerFunc(h.handleInvite)))
}

// VeridianInviteMemberRequest est le payload accepte depuis le front.
// `Role` optionnel (defaut "member" cote Hub). `Message` optionnel pour
// affichage email d'invitation.
type VeridianInviteMemberRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Email       string `json:"email"`
	Role        string `json:"role,omitempty"`
	Message     string `json:"message,omitempty"`
}

// VeridianInviteMemberResponse est le retour OK vers le front.
// MagicLinkURL est inclus pour debug/admin (le Hub envoie deja l'email par
// defaut, mais le front peut l'afficher en cas de demande "copier le lien").
type VeridianInviteMemberResponse struct {
	Status          string `json:"status"`
	Message         string `json:"message"`
	HubInvitationID string `json:"hub_invitation_id"`
	MagicLinkURL    string `json:"magic_link_url"`
	ExpiresAt       string `json:"expires_at"`
	Reused          bool   `json:"reused"`
}

func (h *VeridianInviteMemberHandler) handleInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Mode self-hosted => endpoint indisponible.
	if !h.managedMode {
		WriteJSONError(w, "hub invitation flow disabled (self-hosted mode)", http.StatusServiceUnavailable)
		return
	}

	var req VeridianInviteMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.WorkspaceID == "" || req.Email == "" {
		WriteJSONError(w, "workspace_id and email are required", http.StatusBadRequest)
		return
	}

	// Auth + membership check : verifie que le caller a bien acces au
	// workspace (sinon n'importe qui pourrait inviter chez un workspace tiers).
	// Note : on accepte tous les membres avec un JWT user-type. Le Hub fera
	// l'audit de qui invite qui via inviter_email/inviter_user_id.
	ctx, user, _, err := h.authService.AuthenticateUserForWorkspace(r.Context(), req.WorkspaceID)
	if err != nil {
		WriteJSONError(w, "Forbidden: not a member of this workspace", http.StatusForbidden)
		return
	}

	// Refuse les API key (type api_key) — seul un humain peut inviter.
	if user.Type == domain.UserTypeAPIKey {
		WriteJSONError(w, "API key users cannot invite members", http.StatusForbidden)
		return
	}

	// Refuse l'auto-invitation (sanity check cote app, le Hub le re-verifie).
	if user.Email == req.Email {
		WriteJSONError(w, "cannot invite yourself", http.StatusBadRequest)
		return
	}

	// Resout l'inviter_user_id Hub. 1er choix : user.HubUserID (V46 livre par
	// AttachOwner / AttachMember / OAuth Hub). Fallback : lookup directe sur
	// DB (defense in depth si user.HubUserID nil malgre un row backfille).
	// Last resort : user.ID local (le Hub renverra inviter_not_found et on
	// remappe 422 pour inciter l'owner a signer une fois via le Hub).
	var hubUserID string
	if user.HubUserID != nil && *user.HubUserID != "" {
		hubUserID = *user.HubUserID
	} else if h.hubUserResolver != nil {
		v, lookupErr := h.hubUserResolver.GetHubUserID(ctx, user.ID)
		if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
			if h.logger != nil {
				h.logger.WithFields(map[string]interface{}{
					"user_id": user.ID,
					"error":   lookupErr.Error(),
				}).Error("VeridianInviteMemberHandler: hub_user_id lookup failed")
			}
		}
		hubUserID = v
	}
	inviterID := hubUserID
	if inviterID == "" {
		inviterID = user.ID
		if h.logger != nil {
			h.logger.WithFields(map[string]interface{}{
				"user_id":      user.ID,
				"user_email":   user.Email,
				"workspace_id": req.WorkspaceID,
			}).Warn("VeridianInviteMemberHandler: hub_user_id NULL, using local user_id as inviter_user_id (Hub may reject)")
		}
	}

	// Defaut role = member (cote Hub si target_role omis).
	role := req.Role
	if role == "" {
		role = "member"
	}

	// Appel Hub.
	result, err := h.hubClient.Create(ctx, service.HubInvitationInput{
		InviterUserID:     inviterID,
		InviterEmail:      user.Email,
		InviteeEmail:      req.Email,
		TargetApp:         "notifuse",
		TargetWorkspaceID: req.WorkspaceID,
		TargetRole:        role,
		Message:           req.Message,
	})
	if err != nil {
		h.mapHubErrorToHTTP(w, err)
		return
	}

	resp := VeridianInviteMemberResponse{
		Status:          "success",
		Message:         "Invitation sent via Veridian Hub",
		HubInvitationID: result.InvitationID,
		MagicLinkURL:    result.MagicLinkURL,
		ExpiresAt:       result.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		Reused:          result.Reused,
	}
	writeJSON(w, http.StatusOK, resp)
}

// mapHubErrorToHTTP traduit les erreurs Hub en codes HTTP coherents pour le
// front. La policy est conservative : tout ce qui n'est pas explicitement
// un client error (4xx clair) renvoie 502 (Bad Gateway) — le front affiche
// alors "Hub indisponible, reessayez".
func (h *VeridianInviteMemberHandler) mapHubErrorToHTTP(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrHubInvitationDisabled) {
		WriteJSONError(w, "hub invitation flow disabled (HUB_INVITATION_SECRET_NOTIFUSE not configured)", http.StatusServiceUnavailable)
		return
	}

	var hubErr *service.HubInvitationError
	if !errors.As(err, &hubErr) {
		// Erreur Go imprevisible — 500.
		if h.logger != nil {
			h.logger.WithField("error", err.Error()).Error("VeridianInviteMemberHandler: unexpected error from hub client")
		}
		WriteJSONError(w, "internal error calling hub", http.StatusInternalServerError)
		return
	}

	// Erreur reseau / hub down => 502.
	if hubErr.HubStatus == 0 || hubErr.Code == "hub_unreachable" {
		WriteJSONError(w, "hub unreachable: "+hubErr.Message, http.StatusBadGateway)
		return
	}

	// Mapping codes Hub vers HTTP front.
	switch hubErr.Code {
	case "inviter_not_found":
		// Le Hub ne connait pas l'inviteur — typiquement le owner Notifuse n'a
		// pas encore signe via le Hub (pas de row hub_app.users). 422 plutot
		// que 404 car l'objet "workspace" existe.
		WriteJSONError(w, "Your account is not yet linked to the Veridian Hub. Sign in once via the Hub to enable team invitations.", http.StatusUnprocessableEntity)
	case "self_invitation":
		WriteJSONError(w, "cannot invite yourself", http.StatusBadRequest)
	case "invalid_payload", "invalid_target_app", "invalid_target_role":
		WriteJSONError(w, "invalid invitation: "+hubErr.Message, http.StatusBadRequest)
	case "unauthorized":
		// HMAC invalide cote Hub => probleme de config app (secret rotue ou non-aligne).
		// Pas une erreur user — 502 et log explicite pour Robert.
		if h.logger != nil {
			h.logger.WithField("hub_message", hubErr.Message).Error("VeridianInviteMemberHandler: Hub rejected HMAC — secret mismatch?")
		}
		WriteJSONError(w, "hub authentication failed (server config issue)", http.StatusBadGateway)
	case "rate_limited":
		WriteJSONError(w, "too many invitations sent, please retry in a moment", http.StatusTooManyRequests)
	case "app_mismatch":
		WriteJSONError(w, "hub configuration mismatch", http.StatusBadGateway)
	default:
		// Status Hub propage tel quel si >= 400, sinon 502.
		if hubErr.HubStatus >= 400 && hubErr.HubStatus < 600 {
			WriteJSONError(w, "hub error: "+hubErr.Message, hubErr.HubStatus)
			return
		}
		WriteJSONError(w, "hub error: "+hubErr.Message, http.StatusBadGateway)
	}
}
