// Package tenantlink relie un espace Notifuse à son tenant du Hub (veridian_plan.hub_tenant_id, V73). Fiche 88.
//
// Le lien est ce par quoi un jeton d'action ou une organisation poussée par le Hub retrouve l'espace : sans lui, 409
// espace_non_relie. Avant le 26/11/2026 il ne s'écrivait qu'en passant par update-plan (qui exige un plan et le réécrit)
// et sans contrôle : un tenant pouvait se poser sur deux espaces, le premier par ordre alphabétique gagnait, en silence.
// Link est l'unique écriture du lien : la route POST /api/hub/tenants/link et update-plan passent par lui.
package tenantlink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
)

func init() {
	platform.Register(platform.Capability{
		Name: "tenantlink",
		Wire: func(h platform.Host) error {
			if h.DB() == nil {
				h.Logger().Warn("tenantlink: capacité inactive, application assemblée sans base")
				return nil
			}
			if h.Mux() == nil {
				return fmt.Errorf("tenantlink: mux absent")
			}
			Mount(h.Mux(), &Handler{Store: &Store{DB: h.DB()}, Log: h.Logger()}, h.Config().HubAPISecret)
			return nil
		},
	})
}

// Mount pose la route. Sans secret Hub, elle répond 503 (middleware).
func Mount(mux *http.ServeMux, h *Handler, hubSecret string) {
	mux.Handle("POST /api/hub/tenants/link", middleware.VeridianHMACMiddleware(hubSecret)(h))
}

// Store écrit le lien sur la base système.
type Store struct{ DB *sql.DB }

// Link relie workspaceID à hubTenantID. changed=false : le lien était déjà celui-là.
// sql.ErrNoRows : l'espace n'a pas de plan. *domain.HubTenantConflict : le lien contredit un lien en place, rien n'est écrit.
// replace=true autorise seulement à re-pointer CET espace vers un autre tenant ; un tenant déjà porté par un autre espace vivant
// est toujours refusé (deux espaces sur un tenant = une résolution au hasard).
func (s *Store) Link(ctx context.Context, workspaceID, hubTenantID string, replace bool) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Verrou sur le tenant : deux liens concurrents du même tenant se sérialisent ici.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('veridian_hub_tenant:' || $1))`, hubTenantID); err != nil {
		return false, err
	}
	var current sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT hub_tenant_id FROM veridian_plan WHERE workspace_id = $1 FOR UPDATE`, workspaceID).Scan(&current)
	if err != nil {
		return false, err // sql.ErrNoRows tel quel
	}
	if current.Valid && current.String == hubTenantID {
		return false, tx.Commit()
	}
	if current.Valid && current.String != "" && !replace {
		return false, &domain.HubTenantConflict{WorkspaceID: workspaceID, HubTenantID: hubTenantID, LinkedTenant: current.String}
	}
	var other string
	err = tx.QueryRowContext(ctx, `SELECT workspace_id FROM veridian_plan WHERE hub_tenant_id = $1 AND workspace_id <> $2 AND deleted_at IS NULL
		ORDER BY workspace_id LIMIT 1`, hubTenantID, workspaceID).Scan(&other)
	switch {
	case err == nil:
		return false, &domain.HubTenantConflict{WorkspaceID: workspaceID, HubTenantID: hubTenantID, LinkedWorkspace: other}
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE veridian_plan SET hub_tenant_id = $2, updated_at = NOW() WHERE workspace_id = $1`, workspaceID, hubTenantID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Handler sert POST /api/hub/tenants/link.
type Handler struct {
	Store interface {
		Link(ctx context.Context, workspaceID, hubTenantID string, replace bool) (bool, error)
	}
	Log logger.Logger
}

// Request : le corps signé du Hub.
type Request struct {
	TenantID    string `json:"tenant_id"`
	HubTenantID string `json:"hub_tenant_id"`
	// Replace re-pointe un espace déjà relié à un autre tenant. Jamais par défaut.
	Replace bool `json:"replace,omitempty"`
}

// Response : le lien en place après l'appel.
type Response struct {
	TenantID    string `json:"tenant_id"`
	HubTenantID string `json:"hub_tenant_id"`
	Changed     bool   `json:"changed"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// refuse : même forme que les autres routes du Hub ({code, error, message, details}).
func refuse(w http.ResponseWriter, status int, code, msg string, details map[string]any) {
	writeJSON(w, status, map[string]any{"code": code, "error": code, "message": msg, "details": details})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		refuse(w, http.StatusRequestEntityTooLarge, "invalid_payload", "16 Ko au plus", nil)
		return
	}
	var in Request
	if err := json.Unmarshal(raw, &in); err != nil {
		refuse(w, http.StatusBadRequest, "invalid_payload", "JSON illisible", nil)
		return
	}
	in.TenantID, in.HubTenantID = strings.TrimSpace(in.TenantID), strings.TrimSpace(in.HubTenantID)
	var missing []string
	if in.TenantID == "" {
		missing = append(missing, "tenant_id")
	}
	if in.HubTenantID == "" {
		missing = append(missing, "hub_tenant_id")
	}
	if len(missing) > 0 {
		refuse(w, http.StatusBadRequest, "invalid_payload", strings.Join(missing, " et ")+" requis", map[string]any{"missing": missing})
		return
	}
	if !domain.IsValidHubTenantID(in.HubTenantID) {
		refuse(w, http.StatusBadRequest, "invalid_payload", "hub_tenant_id invalide", map[string]any{"field": "hub_tenant_id"})
		return
	}
	changed, err := h.Store.Link(r.Context(), in.TenantID, in.HubTenantID, in.Replace)
	var conflict *domain.HubTenantConflict
	switch {
	case errors.Is(err, sql.ErrNoRows):
		refuse(w, http.StatusNotFound, "tenant_not_found", "espace inconnu", map[string]any{"tenant_id": in.TenantID})
		return
	case errors.As(err, &conflict):
		refuse(w, http.StatusConflict, "hub_tenant_conflict", conflict.Error(), conflict.Details())
		return
	case err != nil:
		if h.Log != nil {
			h.Log.WithField("error", err.Error()).WithField("tenant_id", in.TenantID).Error("tenantlink: écriture du lien")
		}
		refuse(w, http.StatusInternalServerError, "internal_error", "échec interne, réessayer", nil)
		return
	}
	if changed && h.Log != nil {
		h.Log.WithField("tenant_id", in.TenantID).WithField("hub_tenant_id", in.HubTenantID).Info("tenantlink: espace relié au tenant du Hub")
	}
	writeJSON(w, http.StatusOK, Response{TenantID: in.TenantID, HubTenantID: in.HubTenantID, Changed: changed})
}
