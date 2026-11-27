package liensclics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	httpHandler "github.com/Notifuse/notifuse/internal/http"
)

// Authenticator : droits de l'appelant sur l'espace (service d'authentification upstream).
type Authenticator interface {
	AuthenticateUserForWorkspace(ctx context.Context, workspaceID string) (context.Context, *domain.User, *domain.UserWorkspace, error)
}

// Rapport : service de lecture du détail des clics par lien d'un envoi.
type Rapport struct {
	Auth Authenticator
	// Bases : résout la base workspace (WorkspaceRepository().GetConnection).
	Bases interface {
		GetConnection(ctx context.Context, workspaceID string) (*sql.DB, error)
	}
}

// Lignes rend, pour un envoi, les clics totaux et cliqueurs uniques par URL. Droit requis : lecture de l'historique
// des messages. Un envoi sans clic donne une liste vide.
func (s *Rapport) Lignes(ctx context.Context, workspaceID, broadcastID string) ([]Ligne, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(broadcastID) == "" {
		return nil, fmt.Errorf("workspace_id et broadcast_id sont requis")
	}
	ctx, _, uw, err := s.Auth.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("authentification : %w", err)
	}
	if !uw.HasPermission(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead) {
		return nil, domain.NewPermissionError(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead, "droit messages:read requis")
	}
	db, err := s.Bases.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("base de l'espace : %w", err)
	}
	rows, err := db.QueryContext(ctx, `SELECT clicked_links FROM message_history WHERE broadcast_id = $1 AND clicked_links IS NOT NULL`, broadcastID)
	if err != nil {
		return nil, fmt.Errorf("lecture des clics : %w", err)
	}
	defer func() { _ = rows.Close() }()
	var cartes []Carte
	for rows.Next() {
		var brut []byte
		if err := rows.Scan(&brut); err != nil {
			return nil, fmt.Errorf("lecture des clics : %w", err)
		}
		carte, err := Decoder(brut)
		if err != nil {
			return nil, fmt.Errorf("carte de clics illisible : %w", err)
		}
		cartes = append(cartes, carte)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lecture des clics : %w", err)
	}
	return Rapporter(cartes), nil
}

// Handler : GET /api/veridian/liens.rapport?workspace_id=…&broadcast_id=…
type Handler struct {
	Svc         *Rapport
	RequireAuth func(http.Handler) http.Handler
}

// RegisterRoutes pose la route sur le mux de l'application.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/veridian/liens.rapport", h.RequireAuth(http.HandlerFunc(h.rapport)))
}

func (h *Handler) rapport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lignes, err := h.Svc.Lignes(r.Context(), q.Get("workspace_id"), q.Get("broadcast_id"))
	if err != nil {
		httpHandler.WriteAuthAwareError(w, err, "détail des clics : échec", http.StatusInternalServerError)
		return
	}
	if lignes == nil {
		lignes = []Ligne{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"lignes": lignes})
}
