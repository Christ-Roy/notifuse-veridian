package http

// === Veridian patch — Multi-comptes mail proxy (vague 7, 2026-05-25) ===
//
// VeridianMailAccountsProxyHandler expose 2 endpoints user-auth qui
// proxifient les endpoints Hub HMAC vers la console Notifuse :
//
//	GET  /api/veridian/mail-accounts/me                              -> liste
//	POST /api/veridian/mail-accounts/me/{accountId}/default          -> set default
//
// Sens du flux : Console UI -> Notifuse (JWT user auth) -> Hub (HMAC
// Pattern A `${ts}.`). Le proxy resout l'email du JWT, recupere
// `hub_user_id` du user local, puis appelle `pkg/hub_mail_accounts`.
//
// Pourquoi un proxy au lieu d'appeler le Hub direct depuis le navigateur :
//   - Le Hub exige du HMAC server-to-server (secret partage), pas
//     consommable depuis un browser sans exposer le secret
//   - Evite la friction CORS Hub * Notifuse SPA
//   - Centralise la resolution user_id Notifuse -> hub_user_id (V46
//     backfilled, peut etre nil pour les root/internal users)
//
// Securite :
//   - JWT obligatoire (RequireAuth) : impossible d'enumerer les comptes
//     d'un autre user — on prend TOUJOURS le hub_user_id du caller
//   - Si hub_user_id nil (user pre-V46 ou internal) -> 200 avec
//     hub_available=false (UI affiche "Pas de compte connecte")
//   - HUB_API_SECRET vide cote Notifuse -> client disabled, 200
//     hub_available=false (mode self-hosted)
//
// Cf. ticket Hub `2026-05-25-mail-provider-status-endpoint.md` pour la
// spec des endpoints Hub consomes ici.

import (
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/hub_mail_accounts"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// MailAccountsClient — alias du client `pkg/hub_mail_accounts` pour
// faciliter le mock dans les tests.
type MailAccountsClient = hub_mail_accounts.Client

// VeridianMailAccountsProxyHandler — handler proxy mail-accounts.
type VeridianMailAccountsProxyHandler struct {
	client       MailAccountsClient
	userService  UserLookupService // partage avec VeridianHubDiscoveryHandler
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianMailAccountsProxyHandler constructeur.
//
// client peut etre disabled (HUB_API_SECRET vide) — dans ce cas tous les
// endpoints retournent hub_available=false sans appel reseau.
func NewVeridianMailAccountsProxyHandler(
	client MailAccountsClient,
	userService UserLookupService,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) *VeridianMailAccountsProxyHandler {
	return &VeridianMailAccountsProxyHandler{
		client:       client,
		userService:  userService,
		getJWTSecret: getJWTSecret,
		logger:       log,
	}
}

// RegisterRoutes attache les endpoints au mux sous RequireAuth (JWT).
//
// Route GET ET POST explicitement (cf. memory feedback_marathon_vagues_1_5_patterns
// "catchall root_handler trap" : un POST non route tombe sur la SPA console).
func (h *VeridianMailAccountsProxyHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()
	mux.Handle("GET /api/veridian/mail-accounts/me", requireAuth(http.HandlerFunc(h.handleList)))
	mux.Handle("POST /api/veridian/mail-accounts/me/{accountId}/default", requireAuth(http.HandlerFunc(h.handleSetDefault)))
}

// MailAccountsListResponse forme renvoyee a la console.
//
// HubAvailable false signale a l'UI que le Hub est down OU que
// l'endpoint n'est pas livre OU que le user n'a pas de hub_user_id.
// Dans tous ces cas l'UI affiche "Pas de compte connecte" + bouton
// "Connect your first Gmail account".
type MailAccountsListResponse struct {
	HubAvailable bool                          `json:"hub_available"`
	Accounts     []hub_mail_accounts.MailAccount `json:"accounts"`
}

// MailAccountSetDefaultResponse forme renvoyee a la console apres set-default.
type MailAccountSetDefaultResponse struct {
	HubAvailable bool   `json:"hub_available"`
	UserID       string `json:"user_id,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	IsDefault    bool   `json:"is_default"`
	Reason       string `json:"reason,omitempty"`
}

// handleList GET /api/veridian/mail-accounts/me.
func (h *VeridianMailAccountsProxyHandler) handleList(w http.ResponseWriter, r *http.Request) {
	hubUserID, resolved := h.resolveHubUserID(w, r)
	if !resolved {
		return // 401 deja ecrit
	}
	if hubUserID == "" {
		// User valide mais pas de hub_user_id (pre-V46, root, internal) :
		// fallback 200 hub_available=false pour que l'UI montre le bouton
		// "Connect first account" qui pointe vers le Hub.
		writeJSON(w, http.StatusOK, MailAccountsListResponse{
			HubAvailable: false,
			Accounts:     []hub_mail_accounts.MailAccount{},
		})
		return
	}

	res, err := h.client.ListMailAccounts(r.Context(), hubUserID)
	if err != nil {
		h.warn("list_mail_accounts: client error", map[string]interface{}{
			"error": err.Error(),
		})
		writeJSON(w, http.StatusOK, MailAccountsListResponse{
			HubAvailable: false,
			Accounts:     []hub_mail_accounts.MailAccount{},
		})
		return
	}

	accounts := res.Accounts
	if accounts == nil {
		accounts = []hub_mail_accounts.MailAccount{}
	}
	writeJSON(w, http.StatusOK, MailAccountsListResponse{
		HubAvailable: res.HubAvailable,
		Accounts:     accounts,
	})
}

// handleSetDefault POST /api/veridian/mail-accounts/me/{accountId}/default.
func (h *VeridianMailAccountsProxyHandler) handleSetDefault(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("accountId")
	if accountID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "account_id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"accountId"},
		})
		return
	}

	hubUserID, resolved := h.resolveHubUserID(w, r)
	if !resolved {
		return
	}
	if hubUserID == "" {
		writeJSON(w, http.StatusOK, MailAccountSetDefaultResponse{
			HubAvailable: false,
			Reason:       "no_hub_user_id",
		})
		return
	}

	res, err := h.client.SetDefaultAccount(r.Context(), hubUserID, accountID)
	if err != nil {
		h.warn("set_default_mail_account: client error", map[string]interface{}{
			"error":      err.Error(),
			"account_id": accountID,
		})
		writeJSON(w, http.StatusOK, MailAccountSetDefaultResponse{
			HubAvailable: false,
			Reason:       "client_error",
		})
		return
	}

	writeJSON(w, http.StatusOK, MailAccountSetDefaultResponse{
		HubAvailable: res.HubAvailable,
		UserID:       res.UserID,
		AccountID:    res.AccountID,
		IsDefault:    res.IsDefault,
		Reason:       res.Reason,
	})
}

// resolveHubUserID retourne (hubUserID, resolved). Trois cas :
//   - JWT user_id manquant -> ecrit 401, retourne ("", false)
//   - User trouvable + hub_user_id present -> ("<hub_user_id>", true)
//   - User pas trouvable OU hub_user_id nil -> ("", true) — caller doit
//     ecrire un fallback 200 hub_available=false
func (h *VeridianMailAccountsProxyHandler) resolveHubUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, _ := r.Context().Value(domain.UserIDKey).(string)
	if userID == "" {
		WriteJSONErrorCode(w, ErrCodeUnauthorized, "missing user id in token", http.StatusUnauthorized, nil)
		return "", false
	}

	user, err := h.userService.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		h.warn("mail-accounts proxy: resolve user failed", map[string]interface{}{
			"user_id_len": len(userID),
			"err_nil":     err == nil,
		})
		return "", true
	}

	if user.HubUserID == nil || *user.HubUserID == "" {
		return "", true
	}

	return *user.HubUserID, true
}

func (h *VeridianMailAccountsProxyHandler) warn(msg string, fields map[string]interface{}) {
	if h.logger == nil {
		return
	}
	h.logger.WithFields(fields).Warn(msg)
}
