package domain

// === Veridian patch ===
// Logique de signature/verification du token auto_login self-contained
// utilise par /veridian/auto-login. Place dans le package domain pour etre
// importable a la fois par le service (qui genere le token dans Provision)
// et par le handler HTTP (qui le verifie a la reception). Avant cette
// extraction, le code etait duplique dans les deux fichiers — fragile.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AutoLoginTokenTTL est la fenetre temporelle pendant laquelle un token
// auto-login est valide. Court (60s) pour limiter les replays si l'URL
// avec token fuit (ex: log proxy, history navigateur).
const AutoLoginTokenTTL = 60 * time.Second

// AutoLoginPayload est le contenu signe HMAC contenu dans le token URL.
// Field tags courts pour minimiser la longueur du token URL-encode.
type AutoLoginPayload struct {
	WorkspaceID string `json:"w"`
	Email       string `json:"e"`
	IssuedAt    int64  `json:"i"` // unix ms
	ExpiresAt   int64  `json:"x"` // unix ms
}

// BuildAutoLoginURL genere une URL self-contained
// `<API_ENDPOINT>/veridian/auto-login?token=<base64(payload).<hex(hmac)>`.
// Utilise par VeridianService.Provision et GenerateMagicLink.
func BuildAutoLoginURL(apiEndpoint, hubSecret, workspaceID, email string) (string, time.Time, error) {
	if hubSecret == "" {
		return "", time.Time{}, fmt.Errorf("HUB_API_SECRET not configured")
	}
	now := time.Now()
	expiresAt := now.Add(AutoLoginTokenTTL)
	payload := AutoLoginPayload{
		WorkspaceID: workspaceID,
		Email:       email,
		IssuedAt:    now.UnixMilli(),
		ExpiresAt:   expiresAt.UnixMilli(),
	}
	rawJSON, err := json.Marshal(payload)
	if err != nil {
		return "", time.Time{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(rawJSON)
	mac := hmac.New(sha256.New, []byte(hubSecret))
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := encoded + "." + sig

	url := strings.TrimRight(apiEndpoint, "/") + "/veridian/auto-login?token=" + token
	return url, expiresAt, nil
}

// VerifyAutoLoginToken parse + verifie le token. Retourne le payload si valide.
func VerifyAutoLoginToken(token, hubSecret string) (*AutoLoginPayload, error) {
	if token == "" {
		return nil, fmt.Errorf("token required")
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed token")
	}
	encoded, sigHex := parts[0], parts[1]

	// Verify HMAC
	expectedMAC := hmac.New(sha256.New, []byte(hubSecret))
	expectedMAC.Write([]byte(encoded))
	expectedSig := expectedMAC.Sum(nil)
	providedSig, err := hex.DecodeString(sigHex)
	if err != nil {
		return nil, fmt.Errorf("invalid signature encoding")
	}
	if !hmac.Equal(expectedSig, providedSig) {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode payload
	rawJSON, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid payload encoding")
	}
	var payload AutoLoginPayload
	if err := json.Unmarshal(rawJSON, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload JSON")
	}

	// Verify expiration
	now := time.Now().UnixMilli()
	if now > payload.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}
	if now < payload.IssuedAt-int64(AutoLoginTokenTTL.Milliseconds()) {
		// Defense en profondeur : token avec issued_at dans le futur (clock skew > TTL)
		return nil, fmt.Errorf("token issued in the future")
	}
	if payload.WorkspaceID == "" || payload.Email == "" {
		return nil, fmt.Errorf("token missing workspace_id or email")
	}
	return &payload, nil
}
