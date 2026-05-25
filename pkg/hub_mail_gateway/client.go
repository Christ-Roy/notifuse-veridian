// Package hub_mail_gateway — client HTTP signe HMAC pour appeler
// `POST <hub>/api/mail/send-as-user` depuis Notifuse.
//
// Sens du flux : **Notifuse (downstream) -> Hub** (Pattern A §6.1
// CONTRAT-HUB v1.5). On reutilise le meme secret partage
// `NOTIFUSE_HUB_API_SECRET` (cote Notifuse) = `HUB_API_SECRET` (cote
// Hub) — symetrique (cf. matrice HMAC v3 dans CLAUDE.md Notifuse).
//
// Canonical string signee (identique au flux webhook emitter) :
//
//	"${timestamp_ms}.${rawBody}"
//
// avec :
//   - timestamp_ms : Date.now() unix epoch en millisecondes
//   - rawBody      : bytes JSON exacts envoyes dans le POST (encoding-perfect)
//
// Cas d'usage : envoyer un mail transactionnel (welcome workspace,
// magic link, dunning) depuis le Gmail de l'admin du workspace au lieu
// du sender generique. Le Hub :
//  1. Resout le user_id en refresh_token Google
//  2. Construit le MIME RFC 5322
//  3. Envoie via Gmail API
//  4. Audit `hub_app.mail_events`
//  5. Repond `{message_id, sent_at, idempotent_replay?}`
//
// Resilience : le client fait 3 tentatives avec backoff exponentiel
// (1s/3s/10s) sur 5xx UNIQUEMENT — pas de retry sur 4xx. Sur 5xx
// epuisees -> reason `unreachable`. Le caller peut alors decider du
// fallback (typiquement : SMTP generique existant).
package hub_mail_gateway

import (
	"bytes"
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

// Constantes du contrat. Toute modification doit etre synchronisee avec
// `veridian-hub/app/api/mail/send-as-user/route.ts` (Zod schema + HMAC).
const (
	// DefaultHubBaseURL est l'URL canonique du Hub Veridian en prod.
	DefaultHubBaseURL = "https://app.veridian.site"

	// SendAsUserPath est le chemin du endpoint Hub.
	SendAsUserPath = "/api/mail/send-as-user"

	// AppHeaderName : nom de l'app downstream (`notifuse`).
	AppHeaderName = "x-veridian-app"
	// TimestampHeaderName : unix epoch ms.
	TimestampHeaderName = "X-Veridian-Timestamp"
	// SignatureHeaderName : hex sha256 de la string canonique.
	SignatureHeaderName = "X-Veridian-Hub-Signature"

	// CallerApp est l'identifiant de Notifuse cote Hub.
	CallerApp = "notifuse"

	// ContractVersionV1 — forme historique (sans mail_account_id).
	// Envoye quand le caller ne specifie pas MailAccountID (back-compat).
	ContractVersionV1 = "1.0"

	// ContractVersionV11 — additif : champ mail_account_id optionnel
	// pour selectionner un compte OAuth specifique (multi-comptes user).
	// Envoye uniquement si MailAccountID != "".
	ContractVersionV11 = "1.1"

	// ContractVersion (alias historique) = V1 pour compat externe.
	// Deprecated: utiliser ContractVersionV1 explicitement.
	ContractVersion = ContractVersionV1

	// DefaultTimeout per-attempt. Le Hub doit servir 200 ou erreur en
	// < 5s p99 (envoi Gmail API best-effort).
	DefaultTimeout = 5 * time.Second

	// MaxRetries — tentatives totales (1 initiale + 2 retries).
	MaxRetries = 3
)

// retryBackoffs definit le delai d'attente AVANT chaque tentative
// (index 0 = pas de wait avant la 1ere). Indexes 1 et 2 attendent
// respectivement 1s puis 3s. La 3eme tentative qui echoue ne re-wait
// plus, mais avant le 4eme retry hypothetique on attendrait 10s.
//
// Variable de package (pas const) pour permettre l'override dans les
// tests unitaires sans devoir attendre 14s reels. Utilisation :
//
//	old := retryBackoffs
//	retryBackoffs = []time.Duration{0, 0, 0}
//	defer func() { retryBackoffs = old }()
var retryBackoffs = []time.Duration{0, 1 * time.Second, 3 * time.Second}

// SendMailParams parametres POSTes au Hub. Mapping 1:1 avec le Zod
// schema cote Hub (§2 ticket `2026-05-25-mail-send-as-user-via-hub-gateway`).
//
// Convention : `UserID` doit etre le `hub_app.users.id` du user qui
// envoie (= `users.hub_user_id` cote Notifuse, backfille V46). Au moins
// un des deux `BodyText` ou `BodyHTML` doit etre non-vide (validation
// Zod Hub).
type SendMailParams struct {
	UserID         string
	To             []string // au moins 1
	Subject        string   // 1..998 chars
	BodyText       string
	BodyHTML       string
	CC             []string
	BCC            []string
	ReplyTo        string
	IdempotencyKey string // UUID v4 — anti-double-envoi

	// MailAccountID — v1.1 optionnel. Identifiant Hub d'un compte OAuth
	// specifique (`hub_app.mail_accounts.id`) parmi les N comptes du user.
	// Si vide : le Hub envoie avec le compte par defaut du user (back-compat).
	// Si renseigne : upgrade le body en contract_version "1.1".
	MailAccountID string
}

// sendMailRequest est la forme JSON-marshalled envoyee au Hub. Les tags
// json correspondent strictement au Zod cote Hub (snake_case).
//
// `To` est `string | string[]` cote Zod : on envoie TOUJOURS un array
// pour eviter l'ambiguite. Le Hub accepte les deux formes.
type sendMailRequest struct {
	UserID          string   `json:"user_id"`
	To              []string `json:"to"`
	Subject         string   `json:"subject"`
	BodyText        string   `json:"body_text,omitempty"`
	BodyHTML        string   `json:"body_html,omitempty"`
	CC              []string `json:"cc,omitempty"`
	BCC             []string `json:"bcc,omitempty"`
	ReplyTo         string   `json:"reply_to,omitempty"`
	IdempotencyKey  string   `json:"idempotency_key"`
	ContractVersion string   `json:"contract_version"`
	// MailAccountID — v1.1 uniquement. `omitempty` garantit l'absence
	// totale du champ en wire format quand vide => body v1.0 inchange.
	MailAccountID string `json:"mail_account_id,omitempty"`
}

// Reason* sont les codes d'erreur courts retournes dans SendMailResult.Reason
// quand OK == false. Stable cross-version : le caller peut switch dessus
// sans risque de regression.
const (
	ReasonNeedsReauth       = "needs_reauth"
	ReasonProviderNotLinked = "provider_not_linked"
	ReasonRateLimit         = "rate_limit"
	// ReasonRecipientRateLimited — v1.1 : Hub a refuse l'envoi car le
	// destinataire (`Recipient`) a recu un mail < 20 min auparavant.
	// Le caller peut skip ce destinataire et continuer le batch
	// (cf. broadcasts), ou re-tenter apres `RetryAfterSeconds`.
	//
	// Valeur ALIGNEE sur le champ `error` du body Hub (`rate_limit_recipient`,
	// cf. spec `2026-05-25-mail-provider-status-endpoint.md` §3). Comme
	// pour les autres Reason*, on miroite la valeur exacte que le Hub
	// envoie — cf. mapNonOKStatus qui assigne `result.Reason = errBody.Error`.
	ReasonRecipientRateLimited = "rate_limit_recipient"
	ReasonAccountNotFound      = "account_not_found"
	ReasonUserNotFound         = "user_not_found"
	ReasonUnreachable          = "unreachable"
	ReasonInvalidPayload       = "invalid_payload"
	ReasonInvalidHMAC          = "invalid_hmac"
	ReasonUnknown              = "unknown"
)

// SendMailResult — resultat type retourne par SendMailAsUser.
//
// Quand OK == true : MessageID + SentAt sont peuples, et
// IdempotentReplay=true si le Hub a retourne un envoi precedent
// (meme idempotency_key dans la fenetre Hub).
//
// Quand OK == false : Reason explique l'echec (cf. constantes Reason*),
// HTTPStatus reflete le code HTTP recu du Hub (ou 0 si erreur reseau).
type SendMailResult struct {
	OK               bool
	MessageID        string
	SentAt           time.Time
	IdempotentReplay bool
	Reason           string
	HTTPStatus       int

	// MailAccountIDUsed — v1.1 : id du compte OAuth qui a effectivement
	// envoye (utile quand le caller laisse Hub choisir le defaut). Vide
	// si le Hub ne le retourne pas (anciennes reponses v1.0).
	MailAccountIDUsed string

	// Recipient — populated when Reason == ReasonRecipientRateLimited.
	// Email destinataire concrete qui a declenche le 429.
	Recipient string

	// RetryAfterSeconds — populated when Reason == ReasonRecipientRateLimited.
	// Nombre de secondes a attendre avant de pouvoir re-tenter cet envoi.
	RetryAfterSeconds int
}

// Client interface — facilite le mock dans les tests des callers.
type Client interface {
	SendMailAsUser(ctx context.Context, p SendMailParams) (*SendMailResult, error)
}

// HTTPClient — abstraction pour permettre l'injection d'un http.Client
// custom (timeout reduit en tests, transport mocke).
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// httpClient est l'implementation concrete de Client.
type httpClient struct {
	hubURL string
	secret string
	http   HTTPClient
	logger logger.Logger

	// now permet d'injecter une clock fixe en test (sinon time.Now).
	now func() time.Time
}

// Config — parametres de construction d'un Client.
//
// Tous les champs sont optionnels avec des defaults raisonnables sauf
// HMACSecret :
//   - HubURL vide      -> DefaultHubBaseURL
//   - HTTPClient nil   -> &http.Client{Timeout: DefaultTimeout}
//   - Logger nil       -> warnings best-effort silencieux
//
// HMACSecret vide => NewClient retourne un client "disabled" qui repond
// (nil, ErrMailGatewayDisabled) en silence. Permet le mode self-hosted
// ou dev local sans Hub.
type Config struct {
	HubURL     string
	HMACSecret string
	HTTPClient HTTPClient
	Logger     logger.Logger
}

// ErrMailGatewayDisabled est retournee par SendMailAsUser quand le client
// est instancie sans HMACSecret (mode self-hosted). Le caller doit
// fallback (typiquement vers SMTP generique existant).
var ErrMailGatewayDisabled = errors.New("hub_mail_gateway: client disabled (NOTIFUSE_HUB_API_SECRET missing)")

// ErrInvalidParams est retournee si SendMailParams trivialement invalide
// (user_id vide, to vide, subject vide, ni body_text ni body_html, etc.).
// Le client epargne un round-trip Hub dans ces cas.
var ErrInvalidParams = errors.New("hub_mail_gateway: invalid SendMailParams")

// disabledClient noop pour mode self-hosted.
type disabledClient struct {
	logger logger.Logger
}

// SendMailAsUser retourne (nil, ErrMailGatewayDisabled) en silence.
// Caller doit gerer le fallback.
func (d *disabledClient) SendMailAsUser(_ context.Context, _ SendMailParams) (*SendMailResult, error) {
	if d.logger != nil {
		d.logger.Debug("hub_mail_gateway: disabled (NOTIFUSE_HUB_API_SECRET missing)")
	}
	return nil, ErrMailGatewayDisabled
}

// NewClient construit un Client. Si cfg.HMACSecret est vide, retourne
// un client "disabled" qui n'appelle jamais le Hub.
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

// validate verifie les pre-conditions cote client. Le Hub re-valide avec
// Zod, mais on epargne le round-trip si trivialement invalide.
func (p SendMailParams) validate() error {
	if strings.TrimSpace(p.UserID) == "" {
		return fmt.Errorf("%w: user_id required", ErrInvalidParams)
	}
	if len(p.To) == 0 {
		return fmt.Errorf("%w: at least one recipient required", ErrInvalidParams)
	}
	if strings.TrimSpace(p.Subject) == "" {
		return fmt.Errorf("%w: subject required", ErrInvalidParams)
	}
	if strings.TrimSpace(p.BodyText) == "" && strings.TrimSpace(p.BodyHTML) == "" {
		return fmt.Errorf("%w: at least one of body_text or body_html required", ErrInvalidParams)
	}
	if strings.TrimSpace(p.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency_key required", ErrInvalidParams)
	}
	return nil
}

// SendMailAsUser POST vers le Hub. Retourne :
//
//   - (*SendMailResult{OK:true,...}, nil)  : envoi reussi
//   - (*SendMailResult{OK:false,Reason:X}, nil) : echec previsible (4xx, 5xx
//     epuises) — le caller peut switch sur Reason
//   - (nil, error) : erreur de programmation (params invalides, marshal,
//     ErrMailGatewayDisabled) — le caller doit logger et fallback
//
// Retry sur 5xx UNIQUEMENT : 3 tentatives (1ere immediate, 2 retries
// avec backoff 1s puis 3s). Pas de retry sur 4xx (le Hub a refuse
// definitivement). Pas de retry sur reseau errors (timeout, refused) :
// on considere le Hub injoignable et on remonte Reason=unreachable.
//
// Le context.Done() est respecte : si le ctx expire pendant un backoff,
// on retourne immediatement Reason=unreachable.
func (c *httpClient) SendMailAsUser(ctx context.Context, p SendMailParams) (*SendMailResult, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}

	// Contract version : auto-detection sur MailAccountID. Vide -> "1.0"
	// (wire format historique inchange, back-compat Hub v1.0 garanti).
	// Renseigne -> "1.1" + champ mail_account_id ajoute.
	contractVersion := ContractVersionV1
	if strings.TrimSpace(p.MailAccountID) != "" {
		contractVersion = ContractVersionV11
	}

	req := sendMailRequest{
		UserID:          p.UserID,
		To:              p.To,
		Subject:         p.Subject,
		BodyText:        p.BodyText,
		BodyHTML:        p.BodyHTML,
		CC:              p.CC,
		BCC:             p.BCC,
		ReplyTo:         p.ReplyTo,
		IdempotencyKey:  p.IdempotencyKey,
		ContractVersion: contractVersion,
		MailAccountID:   p.MailAccountID, // omitempty => absent du wire si ""
	}

	rawBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("hub_mail_gateway: marshal body: %w", err)
	}

	var lastResult *SendMailResult
	for attempt := 0; attempt < MaxRetries; attempt++ {
		if attempt > 0 {
			wait := retryBackoffs[attempt]
			if wait > 0 {
				select {
				case <-ctx.Done():
					return &SendMailResult{OK: false, Reason: ReasonUnreachable, HTTPStatus: 0}, nil
				case <-time.After(wait):
				}
			}
		}

		result := c.sendOnce(ctx, rawBody)
		lastResult = result

		// Succes -> retour immediat
		if result.OK {
			return result, nil
		}

		// 4xx -> retry inutile (refus client, le Hub a tranche)
		if result.HTTPStatus >= 400 && result.HTTPStatus < 500 {
			return result, nil
		}

		// 5xx ou reseau (HTTPStatus==0) -> retry
		if c.logger != nil {
			c.logger.WithFields(map[string]interface{}{
				"attempt":         attempt + 1,
				"max_retries":     MaxRetries,
				"http_status":     result.HTTPStatus,
				"reason":          result.Reason,
				"user_id":         p.UserID,
				"idempotency_key": p.IdempotencyKey,
			}).Warn("hub_mail_gateway: attempt failed, will retry if not exhausted")
		}
	}

	// Toutes les tentatives epuisees -> unreachable (recouvre 5xx + reseau).
	if lastResult == nil {
		lastResult = &SendMailResult{OK: false, Reason: ReasonUnreachable, HTTPStatus: 0}
	} else {
		lastResult.Reason = ReasonUnreachable
	}
	if c.logger != nil {
		c.logger.WithFields(map[string]interface{}{
			"user_id":         p.UserID,
			"idempotency_key": p.IdempotencyKey,
			"last_status":     lastResult.HTTPStatus,
		}).Error("hub_mail_gateway: gave up after retries")
	}
	return lastResult, nil
}

// sendOnce execute une seule requete HTTP signee. Toujours retourne un
// *SendMailResult (jamais nil) avec OK true/false + Reason + HTTPStatus.
// Erreurs reseau -> HTTPStatus=0, Reason="unreachable".
func (c *httpClient) sendOnce(ctx context.Context, rawBody []byte) *SendMailResult {
	timestamp := strconv.FormatInt(c.now().UnixMilli(), 10)

	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(rawBody)
	signature := hex.EncodeToString(mac.Sum(nil))

	url := c.hubURL + SendAsUserPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		c.warn("hub_mail_gateway: build request failed", map[string]interface{}{
			"error": err.Error(),
		})
		return &SendMailResult{OK: false, Reason: ReasonUnreachable, HTTPStatus: 0}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(AppHeaderName, CallerApp)
	req.Header.Set(TimestampHeaderName, timestamp)
	req.Header.Set(SignatureHeaderName, signature)

	resp, err := c.http.Do(req)
	if err != nil {
		c.warn("hub_mail_gateway: HTTP call failed", map[string]interface{}{
			"error": err.Error(),
		})
		return &SendMailResult{OK: false, Reason: ReasonUnreachable, HTTPStatus: 0}
	}
	// IMPORTANT : on lit le body UNE seule fois et on le garde en var,
	// equivalent Go du pattern readBody Playwright (cf. memory
	// feedback_marathon_vagues_1_5_patterns.md).
	defer resp.Body.Close()
	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if readErr != nil {
		c.warn("hub_mail_gateway: read response body failed", map[string]interface{}{
			"error":  readErr.Error(),
			"status": resp.StatusCode,
		})
		// Body ilisible mais on a un status -> map quand meme
		return mapNonOKStatus(resp.StatusCode, nil)
	}

	if resp.StatusCode == http.StatusOK {
		var parsed struct {
			MessageID         string `json:"message_id"`
			SentAt            string `json:"sent_at"`
			IdempotentReplay  bool   `json:"idempotent_replay"`
			MailAccountIDUsed string `json:"mail_account_id_used"`
		}
		if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
			c.warn("hub_mail_gateway: decode 200 body failed", map[string]interface{}{
				"error": err.Error(),
			})
			return &SendMailResult{OK: false, Reason: ReasonUnknown, HTTPStatus: 200}
		}
		sentAt, _ := time.Parse(time.RFC3339, parsed.SentAt)
		return &SendMailResult{
			OK:                true,
			MessageID:         parsed.MessageID,
			SentAt:            sentAt,
			IdempotentReplay:  parsed.IdempotentReplay,
			MailAccountIDUsed: parsed.MailAccountIDUsed,
			HTTPStatus:        200,
		}
	}

	return mapNonOKStatus(resp.StatusCode, bodyBytes)
}

// mapNonOKStatus traduit un status HTTP non-200 en SendMailResult typed.
// Si bodyBytes contient un `{error: "..."}` cohérent, on l'utilise pour
// le Reason ; sinon on retombe sur le mapping par defaut du status.
//
// Cas special 429 (v1.1) : discrimination sur le champ `error` du body.
//   - `{"error":"rate_limit_recipient","recipient":...,"retry_after_seconds":...}`
//     -> Reason=ReasonRecipientRateLimited + Recipient + RetryAfterSeconds peuples
//   - `{"error":"rate_limit"}` (ou body vide) -> Reason=ReasonRateLimit (back-compat v6)
//
// Cas special 404 (v1.1) : `{"error":"account_not_found"}` -> Reason=ReasonAccountNotFound
// quand mail_account_id explicitement fourni mais inexistant Hub-side.
func mapNonOKStatus(status int, bodyBytes []byte) *SendMailResult {
	// Body etendu pour capter recipient + retry_after_seconds (429 v1.1).
	// Les champs absents restent zero-value sans erreur Unmarshal.
	var errBody struct {
		Error             string `json:"error"`
		Recipient         string `json:"recipient"`
		RetryAfterSeconds int    `json:"retry_after_seconds"`
	}
	if len(bodyBytes) > 0 {
		_ = json.Unmarshal(bodyBytes, &errBody)
	}

	result := &SendMailResult{
		OK:         false,
		HTTPStatus: status,
	}

	reason := errBody.Error
	if reason == "" {
		// Fallback : mapping par status
		switch {
		case status == http.StatusBadRequest:
			reason = ReasonInvalidPayload
		case status == http.StatusUnauthorized:
			reason = ReasonInvalidHMAC
		case status == http.StatusNotFound:
			reason = ReasonUserNotFound
		case status == http.StatusPreconditionFailed: // 412
			reason = ReasonNeedsReauth
		case status == http.StatusUnprocessableEntity: // 422
			reason = ReasonProviderNotLinked
		case status == http.StatusTooManyRequests: // 429
			reason = ReasonRateLimit
		case status >= 500:
			reason = ReasonUnreachable
		default:
			reason = ReasonUnknown
		}
	}

	result.Reason = reason

	// Per-recipient rate limit (v1.1) : peupler Recipient + RetryAfterSeconds.
	// Discrimination volontairement basee sur Reason==ReasonRecipientRateLimited
	// (champ `error` du body), pas sur le HTTP status seul, pour distinguer
	// du rate limit global user-level qui partage le 429.
	if reason == ReasonRecipientRateLimited {
		result.Recipient = errBody.Recipient
		result.RetryAfterSeconds = errBody.RetryAfterSeconds
	}

	return result
}

func (c *httpClient) warn(msg string, fields map[string]interface{}) {
	if c.logger == nil {
		return
	}
	c.logger.WithFields(fields).Warn(msg)
}
