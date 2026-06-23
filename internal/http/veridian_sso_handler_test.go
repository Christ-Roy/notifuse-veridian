package http

// === Veridian patch — Couche 4 Bounce OAuth Hub (CONTRAT-HUB §6bis.8.5) ===
// Tests colocalises pour veridian_sso_handler.go.
//
// Couvre les tests contractuels bloquants exiges par §6bis.8.5 :
//   1. HMAC invalide → 401 (cf. TestIssueMagicLink_HMACInvalid)
//   2. Drift timestamp > 5min → 401 (cf. TestIssueMagicLink_HMACDrift)
//   3. HMAC valide + user existe + ≥1 workspace → 200 magic_link_url
//   4. HMAC valide + user inconnu → 400 user_not_in_app
//   5. Body invalide / champs manquants → 400 invalid_payload
//   6. Email invalide (sans @) → 400
//   7. Erreur infra (DB) → 500
//
// Rate limit (10/min/user) n'est PAS testee ici — c'est une couche middleware
// future (le ticket le marque "anti-abus si Hub compromis", pas bloquant en v1).

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ssoTestSecret = "test-secret-sso-handler-32chars"

// buildSSOHMACRequest construit une requete POST /api/sso/issue-magic-link signee HMAC.
// timestampOffset permet de simuler un drift (ex: -6 * time.Minute → 401).
func buildSSOHMACRequest(t *testing.T, body string, timestampOffset time.Duration) *http.Request {
	t.Helper()
	ts := time.Now().Add(timestampOffset)
	tsStr := strconv.FormatInt(ts.UnixMilli(), 10)
	payload := []byte(body)

	mac := hmac.New(sha256.New, []byte(ssoTestSecret))
	mac.Write([]byte(tsStr))
	mac.Write([]byte("."))
	mac.Write(payload)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/api/sso/issue-magic-link", bytes.NewReader(payload))
	req.Header.Set("X-Veridian-Hub-Signature", sig)
	req.Header.Set("X-Veridian-Timestamp", tsStr)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// invokeSSO appelle POST /api/sso/issue-magic-link via le mux (avec middleware HMAC).
func invokeSSO(t *testing.T, svc domain.VeridianService, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	h := newHandlerWithService(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, ssoTestSecret)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// === Test 1 : nominal — HMAC + user + workspace → 200 magic_link_url ===

func TestIssueMagicLink_Nominal_Returns200(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	email := "alice@example.com"
	hubUserID := "hub-uuid-1"

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().IssueMagicLinkForHub(gomock.Any(), domain.IssueMagicLinkInput{
		HubUserID: hubUserID,
		Email:     email,
	}).Return(&domain.IssueMagicLinkResponse{
		MagicLinkURL: "https://notifuse.app.veridian.site/veridian/auto-login?token=abc.def",
	}, nil)

	body := fmt.Sprintf(`{"hub_user_id":"%s","email":"%s"}`, hubUserID, email)
	rec := invokeSSO(t, svc, buildSSOHMACRequest(t, body, 0))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domain.IssueMagicLinkResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "https://notifuse.app.veridian.site/veridian/auto-login?token=abc.def", resp.MagicLinkURL)
}

// === Test 2 : user_not_in_app → 400 avec champ `error` litteral ===

func TestIssueMagicLink_UserNotInApp_Returns400WithLiteralError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	email := "ghost@example.com"
	hubUserID := "hub-uuid-ghost"

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().IssueMagicLinkForHub(gomock.Any(), gomock.Any()).
		Return(nil, service.ErrUserNotInApp)

	body := fmt.Sprintf(`{"hub_user_id":"%s","email":"%s"}`, hubUserID, email)
	rec := invokeSSO(t, svc, buildSSOHMACRequest(t, body, 0))

	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Le Hub parse le champ `error` (cf. bounce-apps.ts:228). DOIT etre
	// litteralement "user_not_in_app" (pas le message humain).
	var resp VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "user_not_in_app", resp.Error,
		"champ `error` doit etre litteralement 'user_not_in_app' pour le Hub parser")
	assert.Equal(t, "user_not_in_app", resp.Code)
}

// === Test 3 : HMAC invalide → 401 (test contractuel §6bis.8.5 #1) ===

func TestIssueMagicLink_HMACInvalid_Returns401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	// Service ne doit PAS etre appele (middleware HMAC reject avant).

	tsStr := strconv.FormatInt(time.Now().UnixMilli(), 10)
	req := httptest.NewRequest(http.MethodPost, "/api/sso/issue-magic-link",
		bytes.NewReader([]byte(`{"hub_user_id":"x","email":"a@b.test"}`)))
	req.Header.Set("X-Veridian-Hub-Signature", "0000000000000000deadbeef")
	req.Header.Set("X-Veridian-Timestamp", tsStr)

	rec := invokeSSO(t, svc, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// === Test 4 : drift > 5min → 401 (anti-replay) ===

func TestIssueMagicLink_HMACDrift_Returns401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	// Service ne doit PAS etre appele.

	body := `{"hub_user_id":"x","email":"a@b.test"}`
	rec := invokeSSO(t, svc, buildSSOHMACRequest(t, body, -6*time.Minute))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// === Test 5 : champs manquants → 400 invalid_payload ===

func TestIssueMagicLink_MissingHubUserID_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)

	rec := invokeSSO(t, svc, buildSSOHMACRequest(t, `{"email":"a@b.test"}`, 0))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "hub_user_id")
}

func TestIssueMagicLink_MissingEmail_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)

	rec := invokeSSO(t, svc, buildSSOHMACRequest(t, `{"hub_user_id":"x"}`, 0))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "email")
}

func TestIssueMagicLink_BothFieldsEmpty_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)

	rec := invokeSSO(t, svc, buildSSOHMACRequest(t, `{}`, 0))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "hub_user_id")
	assert.Contains(t, body, "email")
}

// === Test 6 : email invalide (sans @) → 400 ===

func TestIssueMagicLink_InvalidEmail_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)

	rec := invokeSSO(t, svc, buildSSOHMACRequest(t, `{"hub_user_id":"x","email":"notanemail"}`, 0))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "email is invalid")
}

// === Test 7 : JSON malforme → 400 ===

func TestIssueMagicLink_InvalidJSON_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)

	rec := invokeSSO(t, svc, buildSSOHMACRequest(t, `not-json`, 0))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// === Test 8 : erreur infra (DB) → 500 ===

func TestIssueMagicLink_InternalError_Returns500(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().IssueMagicLinkForHub(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("db connection lost"))

	body := `{"hub_user_id":"hub-x","email":"alice@example.com"}`
	rec := invokeSSO(t, svc, buildSSOHMACRequest(t, body, 0))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	// Le 500 ne doit pas leak l'erreur interne brute (DB) au client (axe 2).
	assert.NotContains(t, rec.Body.String(), "db connection lost", "le 500 ne doit pas leak l'erreur interne brute")
	assert.Contains(t, rec.Body.String(), "internal_error")
}

// === Bonus : trimming des espaces autour des champs ===

func TestIssueMagicLink_EmailTrimmed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trimmedEmail := "alice@example.com"
	trimmedHubUserID := "hub-uuid"

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().IssueMagicLinkForHub(gomock.Any(), domain.IssueMagicLinkInput{
		HubUserID: trimmedHubUserID,
		Email:     trimmedEmail,
	}).Return(&domain.IssueMagicLinkResponse{
		MagicLinkURL: "https://notifuse.app.veridian.site/veridian/auto-login?token=t",
	}, nil)

	body := fmt.Sprintf(`{"hub_user_id":"  %s  ","email":"  %s  "}`, trimmedHubUserID, trimmedEmail)
	rec := invokeSSO(t, svc, buildSSOHMACRequest(t, body, 0))
	assert.Equal(t, http.StatusOK, rec.Code)
}
