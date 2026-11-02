package http

// === Veridian — export OPÉRATEUR des identifiants SMTP et IMAP ===
//
// POST /api/veridian/admin/email-integration.export (HMAC Hub SEUL).
// Body : {workspace_id, integration_id (optionnel : vide = toutes les intégrations)}.
// Réponse : cf. service.VeridianIntegrationExport (SMTP/IMAP en clair, reste listé
// « non exportable » sans aucun champ).
//
// Garde-fous (fiche docs/claude/52) :
//   - montée sur `hmac` UNIQUEMENT. PAS `writeRoute` : le middleware d'idempotence
//     persiste le corps de réponse 24 h en base, ce qui écrirait les mots de passe
//     au repos. Un JWT owner ou une clé API client est refusé par le middleware HMAC.
//   - Cache-Control: no-store, Pragma: no-cache sur TOUTE réponse.
//   - rate limit dédié : 20 appels par heure (process), 429 au-delà ;
//   - journal d'audit : une ligne par appel, succès ET échec, SANS aucun secret ni
//     corps de réponse (champs : outcome, workspace, integration, compteurs, origine).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
)

const (
	veridianIntegrationExportMaxPerWindow = 20
	veridianIntegrationExportWindow       = time.Hour
	veridianIntegrationExportAuditMsg     = "veridian_integration_export"
)

// VeridianIntegrationExporter : capacité consommée par le handler, implémentée par *service.EmailService.
type VeridianIntegrationExporter interface {
	VeridianAdminExportIntegrations(ctx context.Context, workspaceID, integrationID string) (*service.VeridianIntegrationExport, error)
}

// SetIntegrationExporter injecte l'exporteur et arme le rate limit dédié. nil => 503.
func (h *VeridianHandler) SetIntegrationExporter(e VeridianIntegrationExporter) {
	h.integrationExporter = e
	h.integrationExportLimiter = newVeridianExportRateLimiter(veridianIntegrationExportMaxPerWindow, veridianIntegrationExportWindow)
}

type veridianIntegrationExportRequest struct {
	WorkspaceID   string `json:"workspace_id"`
	IntegrationID string `json:"integration_id"`
}

// veridianExportRateLimiter : fenêtre glissante en mémoire, bornée à max appels par window.
type veridianExportRateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   []time.Time
	now    func() time.Time
}

func newVeridianExportRateLimiter(max int, window time.Duration) *veridianExportRateLimiter {
	return &veridianExportRateLimiter{max: max, window: window, now: time.Now}
}

// allow enregistre un appel et renvoie false si la fenêtre est pleine.
func (l *veridianExportRateLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	kept := l.hits[:0]
	for _, t := range l.hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.hits = kept
	if len(l.hits) >= l.max {
		return false
	}
	l.hits = append(l.hits, now)
	return true
}

func (h *VeridianHandler) handleEmailIntegrationExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")

	if h.integrationExporter == nil || h.integrationExportLimiter == nil {
		h.auditIntegrationExport(r, "unavailable", "", "", 0, 0, http.StatusServiceUnavailable)
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "integration export unavailable", http.StatusServiceUnavailable, nil)
		return
	}
	if !h.integrationExportLimiter.allow() {
		h.auditIntegrationExport(r, "rate_limited", "", "", 0, 0, http.StatusTooManyRequests)
		// Même forme que le rate-limit global (veridian_rate_limit.go) : code "rate_limited".
		w.Header().Set("Retry-After", "3600")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "Too many integration export requests. Retry later.",
			"code":  "rate_limited",
		})
		return
	}

	var req veridianIntegrationExportRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		h.auditIntegrationExport(r, "invalid", "", "", 0, 0, http.StatusBadRequest)
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.IntegrationID = strings.TrimSpace(req.IntegrationID)
	if req.WorkspaceID == "" {
		h.auditIntegrationExport(r, "invalid", "", req.IntegrationID, 0, 0, http.StatusBadRequest)
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "workspace_id is required", http.StatusBadRequest, nil)
		return
	}

	export, err := h.integrationExporter.VeridianAdminExportIntegrations(r.Context(), req.WorkspaceID, req.IntegrationID)
	if err != nil {
		var notFound *domain.ErrWorkspaceNotFound
		switch {
		case errors.As(err, &notFound):
			h.auditIntegrationExport(r, "not_found", req.WorkspaceID, req.IntegrationID, 0, 0, http.StatusNotFound)
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "workspace not found", http.StatusNotFound, nil)
		case errors.Is(err, service.ErrVeridianExportIntegrationNotFound):
			h.auditIntegrationExport(r, "not_found", req.WorkspaceID, req.IntegrationID, 0, 0, http.StatusNotFound)
			WriteJSONErrorCode(w, ErrCodeInvalidPayload, "integration not found", http.StatusNotFound, nil)
		default:
			// Message générique : l'erreur brute peut contenir des détails de déchiffrement.
			// Journalisée côté serveur sans aucune valeur de champ.
			h.logError("email_integration_export", err, map[string]interface{}{
				"workspace_id":   req.WorkspaceID,
				"integration_id": req.IntegrationID,
			})
			h.auditIntegrationExport(r, "error", req.WorkspaceID, req.IntegrationID, 0, 0, http.StatusInternalServerError)
			WriteJSONErrorCode(w, ErrCodeInternalError, "integration export failed", http.StatusInternalServerError, nil)
		}
		return
	}

	h.auditIntegrationExport(r, "ok", req.WorkspaceID, req.IntegrationID, len(export.Exported), len(export.NotExportable), http.StatusOK)
	writeJSON(w, http.StatusOK, export)
}

// auditIntegrationExport écrit UNE ligne par appel. Aucun champ ne porte un secret,
// un jeton, ni le corps de la réponse. integration_id vide = « toutes ».
func (h *VeridianHandler) auditIntegrationExport(r *http.Request, outcome, workspaceID, integrationID string, exported, notExportable, status int) {
	if h.logger == nil {
		return
	}
	scope := integrationID
	if scope == "" {
		scope = "all"
	}
	h.logger.WithFields(map[string]interface{}{
		"event":          veridianIntegrationExportAuditMsg,
		"outcome":        outcome,
		"workspace_id":   workspaceID,
		"integration_id": scope,
		"exported":       exported,
		"not_exportable": notExportable,
		"http_status":    status,
		"origin":         veridianExportOrigin(r),
	}).Info(veridianIntegrationExportAuditMsg)
}

// veridianExportOrigin : IP du pair (sans port). X-Forwarded-For n'est pas lu : l'appelant
// est authentifié par HMAC, et l'IP sert seulement à retrouver une ligne d'audit.
func veridianExportOrigin(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
