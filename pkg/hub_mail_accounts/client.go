// Package hub_mail_accounts — client HTTP signe HMAC pour les endpoints Hub
// multi-comptes mail OAuth :
//
//	GET  /api/users/{userId}/mail-accounts
//	POST /api/users/{userId}/mail-accounts/{accountId}/default
//
// Specifies par ticket Hub `2026-05-25-mail-provider-status-endpoint.md`.
//
// Sens du flux : **Notifuse (downstream) -> Hub** (Pattern A §6.1
// CONTRAT-HUB v1.5). Secret partage `NOTIFUSE_HUB_API_SECRET` (cote
// Notifuse) = `HUB_API_SECRET` (cote Hub) — symetrique (matrice HMAC v3
// CLAUDE.md Notifuse).
//
// Canonical string signee — IMPORTANT : le ticket Hub specifie
// "Canonical = `${ts}.`" (body vide) pour GET ET pour POST sans body.
// Cf. §1 et §2 du ticket Hub. C'est le pattern Hub-side INBOUND-style
// (`${ts}.${rawBody}`) ou rawBody="" pour les 2 endpoints. ATTENTION :
// ce N'EST PAS le pattern outbound discovery (`${ts}.METHOD.path?query`)
// utilise par `pkg/hub_discovery`. La raison : ces endpoints Hub sont
// concus pour etre symetriques au middleware HMAC inbound deja existant
// cote Hub (`x-veridian-hub-signature`). Voir matrice HMAC v3
// `CLAUDE.md` Notifuse pour la justification de l'asymetrie cross-flux.
//
// Mode optimiste : si Hub retourne 404 (endpoints pas encore livres au
// moment de l'integration Notifuse vague 7), le client mappe en
// `ListAccountsResult{Accounts: []}` avec `HubAvailable: false` pour que
// l'UI affiche un fallback "Pas de compte connecte" sans crash.
package hub_mail_accounts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
)

const (
	// DefaultHubBaseURL prod Veridian.
	DefaultHubBaseURL = "https://app.veridian.site"

	// AppHeaderName : nom de l'app downstream.
	AppHeaderName = "x-veridian-app"
	// TimestampHeaderName : unix epoch ms.
	TimestampHeaderName = "x-veridian-timestamp"
	// SignatureHeaderName : hex sha256 de la string canonique.
	SignatureHeaderName = "x-veridian-hub-signature"

	// CallerApp identifiant Notifuse cote Hub.
	CallerApp = "notifuse"

	// DefaultTimeout per-attempt. Pas de retry sur ces endpoints (read
	// rapide + admin write idempotent — le caller peut re-tenter).
	DefaultTimeout = 5 * time.Second
)

// MailAccount — entree d'un compte OAuth user-side (cf. spec Hub §1).
type MailAccount struct {
	ID          string    `json:"id"`
	Provider    string    `json:"provider"` // "google" | "microsoft"
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	IsDefault   bool      `json:"is_default"`
	NeedsReauth bool      `json:"needs_reauth"`
	ConnectedAt time.Time `json:"connected_at"`
}

// ListAccountsResult — resultat retourne par ListMailAccounts.
//
// HubAvailable distingue les 3 cas :
//   - Hub OK, accounts retournes (eventuellement vide) -> HubAvailable=true
//   - Hub 404 user_not_found OU client disabled -> HubAvailable=true,
//     Accounts=[] (l'UI affiche "Pas de compte connecte")
//   - Hub down / 5xx / timeout -> HubAvailable=false, Accounts=[]
//
// Le caller peut differencier les cas pour montrer un message d'erreur
// approprie ("Hub indisponible, retentez plus tard") mais le DEFAULT
// est "afficher liste vide et bouton Connect".
type ListAccountsResult struct {
	HubAvailable bool          `json:"hub_available"`
	Accounts     []MailAccount `json:"accounts"`
}

// SetDefaultResult — resultat retourne par SetDefaultAccount.
type SetDefaultResult struct {
	HubAvailable bool   `json:"hub_available"`
	UserID       string `json:"user_id,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	IsDefault    bool   `json:"is_default"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// Client interface — facilite le mock dans les tests des callers.
type Client interface {
	ListMailAccounts(ctx context.Context, userID string) (*ListAccountsResult, error)
	SetDefaultAccount(ctx context.Context, userID, accountID string) (*SetDefaultResult, error)
}

// HTTPClient abstraction pour injection en test.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config — parametres construction Client.
//
// HMACSecret vide => client "disabled" qui retourne
// (HubAvailable:false, Accounts:[]) en silence.
type Config struct {
	HubURL     string
	HMACSecret string
	HTTPClient HTTPClient
	Logger     logger.Logger
}

// ErrInvalidParams trivial validation locale.
var ErrInvalidParams = errors.New("hub_mail_accounts: invalid params")

type httpClient struct {
	hubURL string
	secret string
	http   HTTPClient
	logger logger.Logger
	now    func() time.Time
}

type disabledClient struct {
	logger logger.Logger
}

func (d *disabledClient) ListMailAccounts(_ context.Context, _ string) (*ListAccountsResult, error) {
	if d.logger != nil {
		d.logger.Debug("hub_mail_accounts: disabled (NOTIFUSE_HUB_API_SECRET missing)")
	}
	return &ListAccountsResult{HubAvailable: false, Accounts: []MailAccount{}}, nil
}

func (d *disabledClient) SetDefaultAccount(_ context.Context, _, _ string) (*SetDefaultResult, error) {
	if d.logger != nil {
		d.logger.Debug("hub_mail_accounts: disabled (NOTIFUSE_HUB_API_SECRET missing)")
	}
	return &SetDefaultResult{HubAvailable: false, Reason: "disabled"}, nil
}

// NewClient construit un Client. HMACSecret vide -> disabled client.
func NewClient(cfg Config) Client {
	if strings.TrimSpace(cfg.HMACSecret) == "" {
		return &disabledClient{logger: cfg.Logger}
	}

	hubURL := cfg.HubURL
	if hubURL == "" {
		hubURL = DefaultHubBaseURL
	}
	hubURL = strings.TrimRight(hubURL, "/")

	var h HTTPClient = cfg.HTTPClient
	if h == nil {
		h = &http.Client{Timeout: DefaultTimeout}
	}

	return &httpClient{
		hubURL: hubURL,
		secret: cfg.HMACSecret,
		http:   h,
		logger: cfg.Logger,
		now:    time.Now,
	}
}

// ListMailAccounts GET /api/users/{userId}/mail-accounts.
//
// Retourne TOUJOURS un *ListAccountsResult non-nil, jamais d'erreur sauf
// si userID est vide (programming error). Mode optimiste :
//   - 200 OK -> HubAvailable=true, Accounts=parsed
//   - 200 OK avec body vide -> HubAvailable=true, Accounts=[]
//   - 404 user_not_found -> HubAvailable=true, Accounts=[] (endpoint
//     existe, juste pas de compte)
//   - 404 catchall Next.js (endpoint pas encore livre) -> HubAvailable=false,
//     Accounts=[]. Le ticket Hub specifie un body {error:"user_not_found"}
//     pour le vrai 404 ; un catchall Next.js retourne soit un HTML soit
//     un {error:"Not Found"} different. On considere les DEUX comme
//     HubAvailable=true (l'UI doit montrer "Connect first account").
//   - 5xx / network -> HubAvailable=false, Accounts=[]
func (c *httpClient) ListMailAccounts(ctx context.Context, userID string) (*ListAccountsResult, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id required", ErrInvalidParams)
	}

	path := "/api/users/" + userID + "/mail-accounts"
	timestamp := strconv.FormatInt(c.now().UnixMilli(), 10)

	// Canonical = `${ts}.` (body vide pour GET — pattern inbound Hub
	// `${ts}.${rawBody}` avec rawBody="").
	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	signature := hex.EncodeToString(mac.Sum(nil))

	reqURL := c.hubURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		c.warn("hub_mail_accounts: build GET request failed", map[string]interface{}{
			"error": err.Error(),
		})
		return &ListAccountsResult{HubAvailable: false, Accounts: []MailAccount{}}, nil
	}
	req.Header.Set(AppHeaderName, CallerApp)
	req.Header.Set(TimestampHeaderName, timestamp)
	req.Header.Set(SignatureHeaderName, signature)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		c.warn("hub_mail_accounts: GET HTTP call failed", map[string]interface{}{
			"error": err.Error(),
		})
		return &ListAccountsResult{HubAvailable: false, Accounts: []MailAccount{}}, nil
	}
	defer resp.Body.Close()
	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if readErr != nil {
		c.warn("hub_mail_accounts: read GET body failed", map[string]interface{}{
			"error":  readErr.Error(),
			"status": resp.StatusCode,
		})
		return &ListAccountsResult{HubAvailable: false, Accounts: []MailAccount{}}, nil
	}

	if resp.StatusCode == http.StatusOK {
		var parsed struct {
			Accounts []MailAccount `json:"accounts"`
		}
		if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
			c.warn("hub_mail_accounts: decode GET body failed", map[string]interface{}{
				"error": err.Error(),
			})
			// 200 avec body invalide = endpoint pas vraiment celui qu'on
			// pense (catchall HTML, ou contrat casse). On considere
			// indisponible pour ne pas montrer une liste vide trompeuse.
			return &ListAccountsResult{HubAvailable: false, Accounts: []MailAccount{}}, nil
		}
		if parsed.Accounts == nil {
			parsed.Accounts = []MailAccount{}
		}
		return &ListAccountsResult{HubAvailable: true, Accounts: parsed.Accounts}, nil
	}

	if resp.StatusCode == http.StatusNotFound {
		// Distinguer un vrai 404 user_not_found (Hub repond bien, juste
		// pas de user) d'un catchall Next.js (endpoint pas livre).
		// Heuristique : si le body parse en {error: "user_not_found"} on
		// considere Hub OK (l'UI affichera "Pas de compte connecte").
		// Sinon (HTML, autre error) on flag indisponible.
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(bodyBytes, &errBody)
		if errBody.Error == "user_not_found" {
			return &ListAccountsResult{HubAvailable: true, Accounts: []MailAccount{}}, nil
		}
		c.warn("hub_mail_accounts: GET 404 catchall (endpoint not deployed?)", map[string]interface{}{
			"body_len": len(bodyBytes),
		})
		return &ListAccountsResult{HubAvailable: false, Accounts: []MailAccount{}}, nil
	}

	c.warn("hub_mail_accounts: GET non-OK", map[string]interface{}{
		"status": resp.StatusCode,
	})
	return &ListAccountsResult{HubAvailable: false, Accounts: []MailAccount{}}, nil
}

// SetDefaultAccount POST /api/users/{userId}/mail-accounts/{accountId}/default.
//
// Body vide. Canonical = `${ts}.`.
func (c *httpClient) SetDefaultAccount(ctx context.Context, userID, accountID string) (*SetDefaultResult, error) {
	userID = strings.TrimSpace(userID)
	accountID = strings.TrimSpace(accountID)
	if userID == "" || accountID == "" {
		return nil, fmt.Errorf("%w: user_id and account_id required", ErrInvalidParams)
	}

	path := "/api/users/" + userID + "/mail-accounts/" + accountID + "/default"
	timestamp := strconv.FormatInt(c.now().UnixMilli(), 10)

	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	signature := hex.EncodeToString(mac.Sum(nil))

	reqURL := c.hubURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		c.warn("hub_mail_accounts: build POST request failed", map[string]interface{}{
			"error": err.Error(),
		})
		return &SetDefaultResult{HubAvailable: false, Reason: "unreachable"}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(AppHeaderName, CallerApp)
	req.Header.Set(TimestampHeaderName, timestamp)
	req.Header.Set(SignatureHeaderName, signature)

	resp, err := c.http.Do(req)
	if err != nil {
		c.warn("hub_mail_accounts: POST HTTP call failed", map[string]interface{}{
			"error": err.Error(),
		})
		return &SetDefaultResult{HubAvailable: false, Reason: "unreachable"}, nil
	}
	defer resp.Body.Close()
	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if readErr != nil {
		c.warn("hub_mail_accounts: read POST body failed", map[string]interface{}{
			"error":  readErr.Error(),
			"status": resp.StatusCode,
		})
		return &SetDefaultResult{HubAvailable: false, HTTPStatus: resp.StatusCode, Reason: "read_failed"}, nil
	}

	if resp.StatusCode == http.StatusOK {
		var parsed struct {
			UserID    string `json:"user_id"`
			AccountID string `json:"account_id"`
			IsDefault bool   `json:"is_default"`
		}
		if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
			c.warn("hub_mail_accounts: decode POST 200 body failed", map[string]interface{}{
				"error": err.Error(),
			})
			return &SetDefaultResult{HubAvailable: true, IsDefault: true, HTTPStatus: 200}, nil
		}
		return &SetDefaultResult{
			HubAvailable: true,
			UserID:       parsed.UserID,
			AccountID:    parsed.AccountID,
			IsDefault:    parsed.IsDefault,
			HTTPStatus:   200,
		}, nil
	}

	// Non-200 : mapping minimal.
	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(bodyBytes, &errBody)
	reason := errBody.Error
	if reason == "" {
		switch {
		case resp.StatusCode == http.StatusNotFound:
			reason = "account_not_found"
		case resp.StatusCode == http.StatusUnauthorized:
			reason = "invalid_hmac"
		case resp.StatusCode >= 500:
			reason = "unreachable"
		default:
			reason = "unknown"
		}
	}
	// Pour 404 sans body parsable comme {error:user_not_found}, on
	// considere catchall = HubAvailable false.
	hubAvail := resp.StatusCode < 500
	if resp.StatusCode == http.StatusNotFound && errBody.Error == "" {
		hubAvail = false
	}
	return &SetDefaultResult{
		HubAvailable: hubAvail,
		HTTPStatus:   resp.StatusCode,
		Reason:       reason,
	}, nil
}

func (c *httpClient) warn(msg string, fields map[string]interface{}) {
	if c.logger == nil {
		return
	}
	c.logger.WithFields(fields).Warn(msg)
}
