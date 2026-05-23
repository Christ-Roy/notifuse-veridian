package http

// === Veridian patch ===
// VeridianHubDiscoveryHandler — endpoint GET /api/veridian/hub-discovery/me.
//
// Permet a la console Notifuse de decouvrir, juste apres un login reussi,
// si l'email du user est connu cote Hub et quelles APPS Veridian (autres
// que Notifuse) lui sont rattachees. Le front utilise le resultat pour :
//   1. Decider du redirect post-login (menu "tu as plusieurs apps -> Hub")
//   2. Pre-charger les cards cross-app du dashboard ("Tu as aussi Prospection")
//
// Flow concret (sens FRONT -> NOTIFUSE -> HUB) :
//   - Console authentifiee envoie GET /api/veridian/hub-discovery/me
//     avec son JWT habituel (RequireAuth).
//   - Handler resout l'email du user via JWT user_id -> GetUserByID.
//   - Handler appelle `pkg/hub_discovery` (HMAC GET Hub /api/users/by-email).
//   - Reponse 200 toujours, meme si Hub down -> exists=false, tenants=[],
//     hub_available=false. Le front ne bloque jamais le login dessus.
//
// Anti-pattern interdit (§1.4 invariants CONTRAT-HUB) : on ne fait PAS
// d'appel synchrone bloquant au Hub dans le hot path Notifuse. Cet
// endpoint est un opt-in cote console (best-effort, fail-safe).
//
// Securite :
//   - JWT obligatoire (RequireAuth) : pas d'email arbitraire en query,
//     seul l'email du user authentifie est lookup-able. Evite qu'un
//     attacker enumere les emails connus du Hub.
//   - Pas de PII dans les logs (email_len, hub_available, tenants_count).
//
// Cf. ticket todo/2026-05-23-call-hub-discovery-by-email.md.

import (
	"context"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/hub_discovery"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// HubDiscoveryClient — interface du client Hub (alias de
// `pkg/hub_discovery.Client`) pour permettre le mock dans les tests.
type HubDiscoveryClient = hub_discovery.Client

// UserLookupService — sous-ensemble du UserServiceInterface dont on a
// besoin pour resoudre l'email depuis le user_id JWT. Pattern Interface
// Segregation : on n'impose pas la totalite de UserServiceInterface au
// handler, ce qui simplifie les mocks de test.
type UserLookupService interface {
	GetUserByID(ctx context.Context, userID string) (*domain.User, error)
}

// VeridianHubDiscoveryHandler — handler /api/veridian/hub-discovery/me.
type VeridianHubDiscoveryHandler struct {
	client       HubDiscoveryClient
	userService  UserLookupService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianHubDiscoveryHandler — constructeur.
//
// `client` peut etre un client "disabled" (HUB_API_SECRET vide) — dans
// ce cas le endpoint repond toujours hub_available=false.
func NewVeridianHubDiscoveryHandler(
	client HubDiscoveryClient,
	userService UserLookupService,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) *VeridianHubDiscoveryHandler {
	return &VeridianHubDiscoveryHandler{
		client:       client,
		userService:  userService,
		getJWTSecret: getJWTSecret,
		logger:       log,
	}
}

// RegisterRoutes attache le endpoint au mux sous RequireAuth (JWT).
func (h *VeridianHubDiscoveryHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()
	mux.Handle("/api/veridian/hub-discovery/me", requireAuth(http.HandlerFunc(h.handleDiscoverMe)))
}

// HubDiscoveryResponse — schema retourne au front.
//
// HubAvailable = false quand le client est disabled (HUB_API_SECRET vide
// cote Notifuse) OU quand le Hub a retourne une erreur (timeout, 5xx,
// 4xx). Dans tous ces cas, Exists et Tenants sont des valeurs par
// defaut (false, []) — le front sait qu'il ne doit pas tenir compte du
// resultat et continuer le login normalement.
type HubDiscoveryResponse struct {
	HubAvailable bool                              `json:"hub_available"`
	Exists       bool                              `json:"exists"`
	Tenants      []hub_discovery.DiscoveryTenant   `json:"tenants"`
}

func (h *VeridianHubDiscoveryHandler) handleDiscoverMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteJSONErrorCode(w, ErrCodeMethodNotAllowed, "Method not allowed", http.StatusMethodNotAllowed, nil)
		return
	}

	// 1) Resoudre l'email depuis le JWT user_id (defense en profondeur :
	//    on ne fait JAMAIS confiance a un email en query, sinon un user
	//    authentifie pourrait enumerer les comptes Hub par email arbitraire).
	userID, _ := r.Context().Value(domain.UserIDKey).(string)
	if userID == "" {
		WriteJSONErrorCode(w, ErrCodeUnauthorized, "missing user id in token", http.StatusUnauthorized, nil)
		return
	}

	user, err := h.userService.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		// User existe forcement (JWT valide), donc une erreur ici est un
		// signal d'instabilite DB. On repond best-effort (200) pour ne
		// pas bloquer le login flow front-side.
		if h.logger != nil {
			fields := map[string]interface{}{"user_id_len": len(userID)}
			if err != nil {
				fields["error"] = err.Error()
			}
			h.logger.WithFields(fields).Warn("veridian hub-discovery: failed to resolve user")
		}
		writeJSON(w, http.StatusOK, HubDiscoveryResponse{
			HubAvailable: false,
			Exists:       false,
			Tenants:      []hub_discovery.DiscoveryTenant{},
		})
		return
	}

	// 2) Appel Hub best-effort (timeout 2s contractuel).
	exists, tenants, callErr := h.client.LookupByEmail(r.Context(), user.Email)

	// Hub available = pas d'erreur ET (exists vrai OU tenants pas nil
	// retourne par un disabled client). On utilise une heuristique
	// simple : si callErr != nil, on log et on marque hub_available=false.
	// Si callErr == nil et tenants == nil (client disabled), on garde
	// hub_available=false. Dans tous les autres cas hub_available=true.
	hubAvailable := callErr == nil && tenants != nil
	if !hubAvailable && callErr != nil && h.logger != nil {
		h.logger.WithFields(map[string]interface{}{
			"email_len": len(user.Email),
			"error":     callErr.Error(),
		}).Warn("veridian hub-discovery: hub call failed (best-effort)")
	}

	// Defense : si tenants nil (client disabled OU erreur), normaliser en []
	// pour que le front recoive un JSON array et pas null.
	if tenants == nil {
		tenants = []hub_discovery.DiscoveryTenant{}
	}

	writeJSON(w, http.StatusOK, HubDiscoveryResponse{
		HubAvailable: hubAvailable,
		Exists:       exists,
		Tenants:      tenants,
	})
}
