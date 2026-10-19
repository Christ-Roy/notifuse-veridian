package http

// Journal d'acces API (fiche 67) : une ligne JSON par requete /api/*, sur
// stdout (collectee par Loki). Il n'existait AUCUNE trace de requete (ni app,
// ni Traefik) : impossible de savoir qui avait appele quoi.
//
// Champs : route (chemin SANS query), methode, workspace demande (query ou
// champ workspace_id du corps JSON), type d'identite (jwt_user / api_key /
// hmac / none), identifiant opaque du porteur, workspace du jeton (cle API
// seulement, par recherche en base mise en cache), code, duree.
// JAMAIS : corps, query string, en-tetes, jeton, cle, e-mail.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const veridianAccessLogMsg = "veridian_access"

// VeridianTokenWorkspaceLookup renvoie les workspaces d'un utilisateur (cle API).
type VeridianTokenWorkspaceLookup func(ctx context.Context, userID string) ([]string, error)

type accessLogWS struct {
	http.ResponseWriter
	status int
}

func (w *accessLogWS) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

type tokenWSCacheEntry struct {
	ws      string
	expires time.Time
}

// VeridianAccessLogMiddleware journalise les requetes /api/*.
func VeridianAccessLogMiddleware(log logger.Logger, lookup VeridianTokenWorkspaceLookup) func(http.Handler) http.Handler {
	var cache sync.Map
	tokenWorkspace := func(ctx context.Context, userID string) string {
		if lookup == nil || userID == "" {
			return ""
		}
		if v, ok := cache.Load(userID); ok {
			if e := v.(tokenWSCacheEntry); time.Now().Before(e.expires) {
				return e.ws
			}
		}
		ids, err := lookup(ctx, userID)
		if err != nil || len(ids) == 0 {
			return ""
		}
		ws := ids[0]
		if len(ids) > 1 {
			ws = "multiple"
		}
		cache.Store(userID, tokenWSCacheEntry{ws: ws, expires: time.Now().Add(5 * time.Minute)})
		return ws
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			if !strings.HasPrefix(p, "/api/") || p == "/api/health" || p == "/api/version" || strings.HasSuffix(p, "/health") {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			reqWS := r.URL.Query().Get("workspace_id")
			if reqWS == "" && r.Method == http.MethodPost && r.Body != nil && r.ContentLength > 0 && r.ContentLength <= 1<<20 &&
				strings.Contains(r.Header.Get("Content-Type"), "json") {
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				_ = r.Body.Close()
				r.Body = io.NopCloser(bytes.NewReader(body))
				if err == nil {
					var probe struct {
						WorkspaceID string `json:"workspace_id"`
					}
					_ = json.Unmarshal(body, &probe)
					reqWS = probe.WorkspaceID
				}
			}
			idType, tokenUser := "none", ""
			if r.Header.Get("X-Veridian-Hub-Signature") != "" {
				idType = "hmac"
			} else if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				claims := &service.UserClaims{}
				if _, _, err := jwt.NewParser().ParseUnverified(strings.TrimPrefix(h, "Bearer "), claims); err == nil {
					tokenUser = claims.UserID
					if claims.Type == "api_key" {
						idType = "api_key"
					} else {
						idType = "jwt_user"
					}
				} else {
					idType = "invalid"
				}
			}
			rec := &accessLogWS{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			fields := map[string]interface{}{
				"method":        r.Method,
				"route":         p,
				"workspace_req": reqWS,
				"token_type":    idType,
				"token_user_id": tokenUser,
				"status":        rec.status,
				"duration_ms":   time.Since(start).Milliseconds(),
			}
			if idType == "api_key" {
				fields["token_workspace"] = tokenWorkspace(r.Context(), tokenUser)
			}
			log.WithFields(fields).Info(veridianAccessLogMsg)
		})
	}
}
