package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const (
	maxRequestBytes = 256 << 10
	maxResultChars  = 30000 // le Hub plafonne a ~16 000 de son cote ; on borne avant
	serverVersion   = "1.0"
)

var supportedProtocols = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Member : l'espace de l'utilisateur et ses droits.
type Member struct {
	UserID    string
	Email     string
	Workspace *domain.UserWorkspace
}

// Store : retrouve l'utilisateur par hub_user_id (jamais de creation) et choisit son espace.
type Store interface {
	// Resolve rend ErrUnknownUser si aucun humain actif ne porte ce hub_user_id, ErrNoWorkspace s'il n'a aucun espace,
	// ErrNotMember si tenantID (non vide) est un espace Notifuse mais pas un espace actif de l'utilisateur,
	// ErrUnknownTenant si aucun espace ne porte cet id (slug ou id de tenant du Hub appris par update-plan).
	Resolve(ctx context.Context, hubUserID, tenantID string) (*Member, error)
}

var (
	ErrUnknownUser = errors.New("mcp: hub user unknown in this app")
	ErrNoWorkspace = errors.New("mcp: user has no workspace")
	ErrNotMember   = errors.New("mcp: user is not a member of the requested tenant")
	// ErrUnknownTenant : aucun espace Notifuse ne porte cet id (ni slug, ni id de tenant du Hub appris).
	ErrUnknownTenant = errors.New("mcp: tenant_id is not linked to any Notifuse workspace")
)

// Dispatcher rejoue une requete sur le routeur de l'app, au nom de l'utilisateur.
type Dispatcher interface {
	Do(ctx context.Context, m *Member, method, path string, query map[string][]string, body []byte) (status int, resp []byte, err error)
}

// Handler : POST /api/mcp, JSON-RPC 2.0 (Streamable HTTP en reponse JSON).
type Handler struct {
	Verifier   *Verifier
	Store      Store
	Dispatcher Dispatcher
	Logger     logger.Logger
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func rpcError(id json.RawMessage, code int, msg string) map[string]any {
	var rid any
	if len(id) > 0 {
		rid = id
	}
	return map[string]any{"jsonrpc": "2.0", "id": rid, "error": map[string]any{"code": code, "message": msg}}
}

func rpcResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func (h *Handler) unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="notifuse-mcp"`)
	writeJSON(w, http.StatusUnauthorized, rpcError(nil, -32001, msg))
}

// ServeHTTP authentifie (JWT du Hub) puis traite UNE requete JSON-RPC.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	authz := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(authz, "Bearer ")
	if !ok || token == "" {
		h.unauthorized(w, "jeton du Hub requis")
		return
	}
	claims, err := h.Verifier.Verify(r.Context(), token)
	if err != nil {
		h.unauthorized(w, "jeton refuse")
		return
	}
	member, err := h.Store.Resolve(r.Context(), claims.Subject, claims.TenantID)
	switch {
	case errors.Is(err, ErrUnknownUser):
		// Le Hub reconnait cette chaine : il cree le compte par son chemin habituel puis reessaie une fois.
		h.unauthorized(w, "hub_user_unknown : utilisateur inconnu de Notifuse, le Hub le cree, pas l'app")
		return
	case errors.Is(err, ErrUnknownTenant):
		writeJSON(w, http.StatusNotFound, rpcError(nil, -32003, "tenant_id inconnu de Notifuse : cet espace n'est pas relie a ce tenant du Hub (le Hub pousse le lien par update-plan)"))
		return
	case errors.Is(err, ErrNotMember):
		writeJSON(w, http.StatusForbidden, rpcError(nil, -32002, "tu n'es pas membre de cet espace"))
		return
	case errors.Is(err, ErrNoWorkspace):
		writeJSON(w, http.StatusForbidden, rpcError(nil, -32002, "aucun espace Notifuse pour cet utilisateur"))
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, rpcError(nil, -32603, "erreur interne"))
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(raw) > maxRequestBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, rpcError(nil, -32600, "requete trop grande"))
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		writeJSON(w, http.StatusBadRequest, rpcError(nil, -32600, "requete JSON-RPC 2.0 unique attendue"))
		return
	}
	if len(req.ID) == 0 { // notification : pas de reponse
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, h.dispatch(r.Context(), member, claims, &req))
}

func (h *Handler) dispatch(ctx context.Context, m *Member, claims *Claims, req *rpcRequest) map[string]any {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := supportedProtocols[0]
		for _, v := range supportedProtocols {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		return rpcResult(req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "notifuse-veridian", "version": serverVersion},
			"instructions":    "Outils Notifuse de l'espace " + m.Workspace.WorkspaceID + " (deduit de ton compte).",
		})
	case "ping":
		return rpcResult(req.ID, map[string]any{})
	case "tools/list":
		tools := []map[string]any{}
		for i := range catalog {
			if catalog[i].allowed(m.Workspace) {
				tools = append(tools, catalog[i].descriptor())
			}
		}
		return rpcResult(req.ID, map[string]any{"tools": tools})
	case "tools/call":
		return h.call(ctx, m, claims, req)
	}
	return rpcError(req.ID, -32601, "methode inconnue")
}

func toolResult(id json.RawMessage, text string, isErr bool) map[string]any {
	if len(text) > maxResultChars {
		text = text[:maxResultChars] + "\n[... tronque : affine le filtre ou pagine avec limit/cursor]"
	}
	return rpcResult(id, map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr})
}

func (h *Handler) call(ctx context.Context, m *Member, claims *Claims, req *rpcRequest) map[string]any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
		return rpcError(req.ID, -32602, "name requis")
	}
	t, ok := findTool(p.Name)
	if !ok {
		return rpcError(req.ID, -32602, "outil inconnu")
	}
	start := time.Now()
	outcome := "ok"
	defer func() {
		if h.Logger != nil { // jamais les arguments : le Hub tient l'audit, l'app trace l'issue
			h.Logger.WithFields(map[string]interface{}{"tool": p.Name, "client_id": claims.ClientID, "workspace_id": m.Workspace.WorkspaceID,
				"outcome": outcome, "duration_ms": time.Since(start).Milliseconds()}).Info("veridian mcp: tool call")
		}
	}()
	if !t.allowed(m.Workspace) {
		outcome = "forbidden"
		return toolResult(req.ID, "droit insuffisant pour cet outil sur ton espace", true)
	}
	query, body, err := t.request(p.Arguments, m.Workspace.WorkspaceID)
	if err != nil {
		outcome = "bad_args"
		return toolResult(req.ID, err.Error(), true)
	}
	status, resp, err := h.Dispatcher.Do(ctx, m, t.Method, t.Path, query, body)
	if err != nil {
		outcome = "error"
		return toolResult(req.ID, "appel impossible", true)
	}
	if status >= 400 {
		outcome = "http_" + http.StatusText(status)
		return toolResult(req.ID, strings.TrimSpace(string(resp)), true)
	}
	return toolResult(req.ID, strings.TrimSpace(string(resp)), false)
}

