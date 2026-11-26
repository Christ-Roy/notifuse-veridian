package http

// Garde d'autorisation workspace des routes qui n'en avaient AUCUNE (fiche 67).
//
// Constat : webhookSubscriptions.*, segments.* et timeline.list passent
// workspace_id du client tel quel au repository ; l'authService est stocke
// mais jamais appele. Tout porteur d'un JWT valide (meme celui d'un AUTRE
// workspace) pouvait lister, creer ou supprimer des abonnements webhook,
// lire ou reconstruire des segments d'un tenant tiers.
//
// Un middleware unique, devant le mux, plutot qu'un patch de trois services
// upstream. Table des routes ci-dessous = la liste faisant foi.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
)

type veridianAuthzLevel int

const (
	veridianAuthzMember veridianAuthzLevel = iota + 1 // membre du workspace (cle API scopee comprise)
	veridianAuthzOwner                                // propriétaire ou admin, humain seulement
)

// VeridianWorkspaceAuthzRoutes : route -> niveau exige.
var VeridianWorkspaceAuthzRoutes = map[string]veridianAuthzLevel{
	"/api/webhookSubscriptions.create":           veridianAuthzOwner,
	"/api/webhookSubscriptions.update":           veridianAuthzOwner,
	"/api/webhookSubscriptions.delete":           veridianAuthzOwner,
	"/api/webhookSubscriptions.toggle":           veridianAuthzOwner,
	"/api/webhookSubscriptions.regenerateSecret": veridianAuthzOwner,
	"/api/webhookSubscriptions.test":             veridianAuthzOwner,
	"/api/webhookSubscriptions.list":             veridianAuthzMember,
	"/api/webhookSubscriptions.get":              veridianAuthzMember,
	"/api/webhookSubscriptions.deliveries":       veridianAuthzMember,
	"/api/segments.list":                         veridianAuthzMember,
	"/api/segments.get":                          veridianAuthzMember,
	"/api/segments.create":                       veridianAuthzMember,
	"/api/segments.update":                       veridianAuthzMember,
	"/api/segments.delete":                       veridianAuthzMember,
	"/api/segments.rebuild":                      veridianAuthzMember,
	"/api/segments.preview":                      veridianAuthzMember,
	"/api/segments.contacts":                     veridianAuthzMember,
	"/api/timeline.list":                         veridianAuthzMember,
	// Fiche 79 (04/11/2026) : TaskService n a aucun controle d appartenance (0 appel a
	// AuthenticateUserForWorkspace). Mesure staging : la cle du workspace A listait les taches de B.
	"/api/tasks.list":    veridianAuthzMember,
	"/api/tasks.get":     veridianAuthzMember,
	"/api/tasks.create":  veridianAuthzMember,
	"/api/tasks.delete":  veridianAuthzMember,
	"/api/tasks.reset":   veridianAuthzMember,
	"/api/tasks.trigger": veridianAuthzMember,
}

// VeridianWorkspaceAuthzDeps : le minimum de l'AuthService.
type VeridianWorkspaceAuthzDeps interface {
	AuthenticateUserForWorkspace(ctx context.Context, workspaceID string) (context.Context, *domain.User, *domain.UserWorkspace, error)
}

// VeridianWorkspaceAuthzMiddleware authentifie (JWT) puis autorise sur le
// workspace_id de la requete (query ou corps JSON) pour les routes de la table.
func VeridianWorkspaceAuthzMiddleware(auth VeridianWorkspaceAuthzDeps, getJWTSecret func() ([]byte, error)) func(http.Handler) http.Handler {
	requireAuth := middleware.NewAuthMiddleware(getJWTSecret).RequireAuth()
	return func(next http.Handler) http.Handler {
		guard := requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			level := VeridianWorkspaceAuthzRoutes[r.URL.Path]
			wsID, ok := veridianAuthzWorkspaceID(w, r)
			if !ok {
				return
			}
			_, user, uw, err := auth.AuthenticateUserForWorkspace(r.Context(), wsID)
			if err != nil {
				WriteAuthAwareError(w, err, "Failed to authorize workspace access", http.StatusInternalServerError)
				return
			}
			if level == veridianAuthzOwner && (!uw.VeridianAdministers() || user == nil || user.Type == domain.UserTypeAPIKey) {
				WriteJSONError(w, "Forbidden: only the workspace owner can do this", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		}))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := VeridianWorkspaceAuthzRoutes[r.URL.Path]; ok && r.Method != http.MethodOptions {
				guard.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// veridianAuthzWorkspaceID rend l'UNIQUE workspace_id de la requête, lu dans la query ET dans le corps JSON.
// Faille du 26/11 (chasse sécu, lot B) : seule la query était lue quand elle était présente, alors que les handlers
// POST lisent le corps. `?workspace_id=MIEN` + corps `{"workspace_id":"VICTIME"}` passait le garde sur MIEN et
// agissait sur VICTIME (webhook créé chez un autre client). Deux valeurs distinctes, où qu'elles soient : 400.
func veridianAuthzWorkspaceID(w http.ResponseWriter, r *http.Request) (string, bool) {
	ids := map[string]bool{}
	for _, v := range r.URL.Query()["workspace_id"] {
		ids[v] = true
	}
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, veridianAuthzMaxBody+1))
		_ = r.Body.Close()
		if err != nil {
			WriteJSONError(w, "Invalid request body", http.StatusBadRequest)
			return "", false
		}
		if len(body) > veridianAuthzMaxBody {
			WriteJSONError(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return "", false
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var probe struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if json.Unmarshal(body, &probe) == nil {
			ids[probe.WorkspaceID] = true
		}
	}
	delete(ids, "")
	if len(ids) > 1 {
		WriteJSONError(w, "workspace_id differs between query and body", http.StatusBadRequest)
		return "", false
	}
	for id := range ids {
		return id, true
	}
	WriteJSONError(w, "workspace_id is required", http.StatusBadRequest)
	return "", false
}

// veridianAuthzMaxBody : au-delà, refus net plutôt qu'un corps tronqué passé en silence au handler.
const veridianAuthzMaxBody = 4 << 20
