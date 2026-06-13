package service

// === Veridian patch — Hub invitation client (2026-05-23) ===
// Client HTTP sortant Notifuse -> Hub pour `POST /api/invitations/create`.
//
// Trigger : owner Notifuse clique "Inviter membre" dans la UI Team Settings.
// Au lieu de creer une invitation locale (notifuse_invitations), on delegue
// au Hub qui :
//   1. Cree une invitation cross-app dans `hub_app.cross_app_invitations`
//   2. Genere un magic-link auto-loggue
//   3. Envoie l'email au destinataire avec le template Hub
//   4. Apres acceptation, appelle Notifuse `POST /api/veridian/workspaces/{id}/attach-member`
//      (handler deja existant cote Notifuse, lot B 2026-05-21)
//
// Contrat HMAC (cf. veridian-hub/lib/invitations/hmac.ts) :
//   - Header `x-veridian-app: notifuse`
//   - Header `x-veridian-timestamp: <unix_ms>`
//   - Header `x-veridian-invitation-signature: hex(hmac_sha256(secret, ts + '.' + rawBody))`
//   - Drift max 5 minutes
//   - Secret : env HUB_INVITATION_SECRET_NOTIFUSE
//
// CONTRAT-HUB §1.4 (resilience apps) :
//   - Si Hub down/timeout : le handler retournera 502 vers le frontend.
//     L'owner peut retry — pas de retention locale (le Hub est la source de
//     verite des invitations cross-app).
//   - Pas de fallback "invitation locale" — eviterait l'unicite Hub.

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
	"strings"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// DefaultHubBaseURL est l'URL racine du Hub Veridian en prod. Le host public
// Veridian est `app.veridian.site` (PAS `hub.veridian.site` qui n'existe pas
// en DNS public — bug corrigé 2026-06-13). Override via env HUB_BASE_URL
// (ex: staging "https://hub.staging.veridian.site").
const DefaultHubBaseURL = "https://app.veridian.site"

// DefaultHubInvitationTimeout : 10s. Le Hub fait surface DB + send email
// (best-effort), donc p99 ~ 2-3s. 10s tolere un pic sans bloquer l'UX
// admin (l'owner peut etre cliquer et attendre).
const DefaultHubInvitationTimeout = 10 * time.Second

// HubInvitationInput parametres POSTes au Hub. Mapping 1:1 avec le schema
// Zod cote Hub (cf. veridian-hub/app/api/invitations/create/route.ts).
//
// Convention : `inviter_user_id` doit etre le `hub_user_id` du caller
// (resolu via users.hub_user_id, backfille V46). `inviter_email` sert
// uniquement a l'affichage email cote Hub.
type HubInvitationInput struct {
	InviterUserID     string  `json:"inviter_user_id"`
	InviterEmail      string  `json:"inviter_email"`
	InviteeEmail      string  `json:"invitee_email"`
	TargetApp         string  `json:"target_app"`
	TargetWorkspaceID string  `json:"target_workspace_id"`
	TargetRole        string  `json:"target_role,omitempty"`
	Message           string  `json:"message,omitempty"`
}

// HubInvitationResult mappe la reponse 201/200 du Hub. `Reused=true` quand
// le Hub a retourne une invitation pending preexistante (idempotence par
// couple invitee_email + target_workspace_id).
type HubInvitationResult struct {
	InvitationID string    `json:"invitation_id"`
	Token        string    `json:"token"`
	MagicLinkURL string    `json:"magic_link_url"`
	ExpiresAt    time.Time `json:"expires_at"`
	TargetRole   string    `json:"target_role"`
	Reused       bool      `json:"reused"`
}

// HubInvitationError est l'erreur typee retournee par Create() pour
// permettre au handler HTTP de mapper en codes HTTP propres.
type HubInvitationError struct {
	// Status HTTP renvoye par le Hub (404 inviter_not_found, 401 hmac, etc.).
	// 0 = erreur reseau / timeout / parse JSON.
	HubStatus int
	// Code court extrait du body Hub (ex: "inviter_not_found", "self_invitation").
	Code string
	// Message lisible (pour log et front).
	Message string
}

func (e *HubInvitationError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("hub invitation error: %s (status=%d): %s", e.Code, e.HubStatus, e.Message)
	}
	return fmt.Sprintf("hub invitation error (status=%d): %s", e.HubStatus, e.Message)
}

// ErrHubInvitationDisabled est retournee par Create() si le client est
// instancie sans secret HMAC (mode self-hosted). Le handler doit renvoyer
// 503 vers le front pour signaler que le mode managed n'est pas configure.
var ErrHubInvitationDisabled = errors.New("hub invitation client disabled (HUB_INVITATION_SECRET_NOTIFUSE not configured)")

// HubInvitationClient est l'interface implementee par
// `*veridianHubInvitationClient`. Permet le mock en tests handler.
type HubInvitationClient interface {
	Create(ctx context.Context, input HubInvitationInput) (*HubInvitationResult, error)
}

type veridianHubInvitationClient struct {
	baseURL string
	secret  string
	httpc   *http.Client
	logger  logger.Logger
}

// NewVeridianHubInvitationClient construit un client. `baseURL` vide =>
// DefaultHubBaseURL. `secret` vide => le client retourne
// ErrHubInvitationDisabled sur tout Create() (mode self-hosted).
// `httpc` nil => client avec timeout DefaultHubInvitationTimeout.
func NewVeridianHubInvitationClient(
	baseURL, secret string,
	httpc *http.Client,
	log logger.Logger,
) HubInvitationClient {
	if baseURL == "" {
		baseURL = DefaultHubBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if httpc == nil {
		httpc = &http.Client{Timeout: DefaultHubInvitationTimeout}
	}
	return &veridianHubInvitationClient{
		baseURL: baseURL,
		secret:  secret,
		httpc:   httpc,
		logger:  log,
	}
}

// Create envoie l'invitation au Hub. Retourne le resultat parse ou
// `*HubInvitationError` si statut HTTP != 2xx.
//
// Headers HMAC :
//   - x-veridian-app: notifuse
//   - x-veridian-timestamp: ms epoch
//   - x-veridian-invitation-signature: hex(hmac_sha256(secret, ts + '.' + body))
//   - Content-Type: application/json
func (c *veridianHubInvitationClient) Create(
	ctx context.Context,
	input HubInvitationInput,
) (*HubInvitationResult, error) {
	if c.secret == "" {
		return nil, ErrHubInvitationDisabled
	}
	// Validation minimale cote client (le Hub re-valide via Zod, mais on
	// epargne un round-trip si payload trivialement invalide).
	if input.InviterUserID == "" || input.InviterEmail == "" || input.InviteeEmail == "" {
		return nil, &HubInvitationError{
			HubStatus: 0,
			Code:      "invalid_input",
			Message:   "inviter_user_id, inviter_email and invitee_email are required",
		}
	}
	if input.TargetApp == "" {
		input.TargetApp = "notifuse"
	}
	if input.TargetWorkspaceID == "" {
		return nil, &HubInvitationError{
			HubStatus: 0,
			Code:      "invalid_input",
			Message:   "target_workspace_id is required",
		}
	}

	rawBody, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal invitation input: %w", err)
	}

	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(ts + "." + string(rawBody)))
	signature := hex.EncodeToString(mac.Sum(nil))

	url := c.baseURL + "/api/invitations/create"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		return nil, fmt.Errorf("build hub request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-veridian-app", "notifuse")
	req.Header.Set("x-veridian-timestamp", ts)
	req.Header.Set("x-veridian-invitation-signature", signature)

	resp, err := c.httpc.Do(req)
	if err != nil {
		// Erreur reseau / timeout. Logger best-effort.
		if c.logger != nil {
			c.logger.WithFields(map[string]interface{}{
				"target_workspace_id": input.TargetWorkspaceID,
				"invitee_email":       input.InviteeEmail,
				"error":               err.Error(),
			}).Warn("VeridianHubInvitationClient: network error calling hub")
		}
		return nil, &HubInvitationError{
			HubStatus: 0,
			Code:      "hub_unreachable",
			Message:   err.Error(),
		}
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, &HubInvitationError{
			HubStatus: resp.StatusCode,
			Code:      "hub_response_unreadable",
			Message:   readErr.Error(),
		}
	}

	// Status 2xx => parse OK.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var out HubInvitationResult
		if err := json.Unmarshal(bodyBytes, &out); err != nil {
			return nil, &HubInvitationError{
				HubStatus: resp.StatusCode,
				Code:      "hub_response_invalid_json",
				Message:   err.Error(),
			}
		}
		return &out, nil
	}

	// Status non-2xx => extraire {error, reason} du body.
	var errBody struct {
		Error  string `json:"error"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(bodyBytes, &errBody)
	code := errBody.Error
	if code == "" {
		code = fmt.Sprintf("hub_status_%d", resp.StatusCode)
	}
	msg := errBody.Reason
	if msg == "" {
		// fallback body brut tronque
		if len(bodyBytes) > 200 {
			msg = string(bodyBytes[:200])
		} else {
			msg = string(bodyBytes)
		}
	}
	if c.logger != nil {
		c.logger.WithFields(map[string]interface{}{
			"hub_status":          resp.StatusCode,
			"hub_error":           code,
			"target_workspace_id": input.TargetWorkspaceID,
			"invitee_email":       input.InviteeEmail,
		}).Warn("VeridianHubInvitationClient: hub returned non-2xx")
	}
	return nil, &HubInvitationError{
		HubStatus: resp.StatusCode,
		Code:      code,
		Message:   msg,
	}
}
