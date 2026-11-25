// Package planview rend le plan d'un espace à ses membres : GET /api/veridian/me/workspace-plan?workspace_id=.
//
// La page Réglages > Plan de la console l'appelle depuis le 21/05/2026, mais la route n'a jamais existé : le repli de l'écran affichait
// « Free, essai de 15 jours » à tout le monde, espaces payants compris (mesuré le 25/11/2026). Ni quota ni compteur ici (règle 8, fiche 47).
package planview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func init() {
	platform.Register(platform.Capability{
		Name: "planview",
		Wire: func(h platform.Host) error {
			if h.DB() == nil {
				h.Logger().Warn("planview: capacité inactive, application assemblée sans base")
				return nil
			}
			if h.Mux() == nil || h.AuthService() == nil {
				return fmt.Errorf("planview: mux ou authentification absent")
			}
			handler := &Handler{Plans: repository.NewVeridianPlanRepository(h.DB()), Auth: h.AuthService()}
			mux := h.Mux()
			mux.Handle("GET /api/veridian/me/workspace-plan", h.RequireAuth()(handler))
			return nil
		},
	})
}

// Plans : la seule lecture du dépôt dont on a besoin.
type Plans interface {
	Get(ctx context.Context, workspaceID string) (*domain.VeridianPlan, error)
}

// Auth : l'appartenance à l'espace.
type Auth interface {
	AuthenticateUserForWorkspace(ctx context.Context, workspaceID string) (context.Context, *domain.User, *domain.UserWorkspace, error)
}

// Handler sert la route.
type Handler struct {
	Plans Plans
	Auth  Auth
}

// Plan est la réponse : de quoi nommer le plan, rien de plus.
type Plan struct {
	TenantID    string            `json:"tenant_id"`
	Plan        string            `json:"plan"`
	PlanSource  domain.PlanSource `json:"plan_source"`
	Status      domain.PlanStatus `json:"status"`
	GeneratedAt time.Time         `json:"generated_at"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace_id")
	if ws == "" {
		write(w, http.StatusBadRequest, map[string]string{"code": "invalid_payload", "error": "workspace_id requis"})
		return
	}
	if _, _, _, err := h.Auth.AuthenticateUserForWorkspace(r.Context(), ws); err != nil {
		write(w, http.StatusForbidden, map[string]string{"code": "forbidden", "error": "vous n'êtes pas membre de cet espace"})
		return
	}
	p, err := h.Plans.Get(r.Context(), ws)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && p == nil) {
		write(w, http.StatusNotFound, map[string]string{"code": "plan_not_found", "error": "aucun plan pour cet espace"})
		return
	}
	if err != nil {
		write(w, http.StatusInternalServerError, map[string]string{"code": "internal_error", "error": "lecture du plan impossible"})
		return
	}
	write(w, http.StatusOK, Plan{TenantID: p.WorkspaceID, Plan: p.Plan, PlanSource: p.PlanSource, Status: p.Status, GeneratedAt: time.Now().UTC()})
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
