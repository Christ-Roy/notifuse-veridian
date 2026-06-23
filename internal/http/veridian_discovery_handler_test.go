package http

// === Veridian patch === Tests colocalises pour veridian_discovery_handler.go.
//
// Couvre les 8 cas spec :
//  1. HMAC valide + user existant + 2 workspaces → 200 found:true + 2 workspaces
//  2. HMAC valide + user inconnu → 200 found:false workspaces:[]
//  3. HMAC invalide → 401
//  4. Drift > 5min → 401
//  5. Email manquant → 400
//  6. Email invalide (sans @) → 400
//  7. User existe mais 0 workspace → 200 found:true workspaces:[]
//  8. Erreur DB → 500
//
// Tests 3 et 4 (HMAC) sont couverts egalement par middleware/veridian_hmac_test.go,
// mais inclus ici pour garantir l integration handler+middleware via le mux reel.

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
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const discoveryTestSecret = "test-secret-discovery-handler"

// buildDiscoveryHMACRequest construit une requete POST /api/users/by-email signee HMAC.
// timestampOffset permet de simuler un drift (ex: -6 * time.Minute → 401).
func buildDiscoveryHMACRequest(t *testing.T, body string, timestampOffset time.Duration) *http.Request {
	t.Helper()
	ts := time.Now().Add(timestampOffset)
	tsStr := strconv.FormatInt(ts.UnixMilli(), 10)
	payload := []byte(body)

	mac := hmac.New(sha256.New, []byte(discoveryTestSecret))
	mac.Write([]byte(tsStr))
	mac.Write([]byte("."))
	mac.Write(payload)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/api/users/by-email", bytes.NewReader(payload))
	req.Header.Set("X-Veridian-Hub-Signature", sig)
	req.Header.Set("X-Veridian-Timestamp", tsStr)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// invokeDiscovery appelle POST /api/users/by-email via le mux (avec middleware HMAC).
func invokeDiscovery(t *testing.T, svc domain.VeridianService, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	h := newHandlerWithService(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, discoveryTestSecret)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// === Test 1 : user connu + 2 workspaces → 200 found:true ===

func TestDiscovery_FoundTwoWorkspaces(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	email := "alice@example.com"
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().LookupByEmail(gomock.Any(), email).Return(&domain.DiscoveryResponse{
		Found:     true,
		UserEmail: email,
		Workspaces: []domain.DiscoveryWorkspace{
			{
				WorkspaceID:      "ws-alice",
				WorkspaceName:    "Alice Corp",
				Role:             "owner",
				Plan:             "pro",
				MagicLinkCapable: true,
				FallbackURL:      "https://notifuse.app.veridian.site/console/signin",
			},
			{
				WorkspaceID:      "ws-shared",
				WorkspaceName:    "Shared Project",
				Role:             "member",
				Plan:             "free",
				MagicLinkCapable: true,
				FallbackURL:      "https://notifuse.app.veridian.site/console/signin",
			},
		},
	}, nil)

	body := fmt.Sprintf(`{"email":"%s"}`, email)
	rec := invokeDiscovery(t, svc, buildDiscoveryHMACRequest(t, body, 0))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domain.DiscoveryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Found)
	assert.Equal(t, email, resp.UserEmail)
	require.Len(t, resp.Workspaces, 2)
	assert.Equal(t, "ws-alice", resp.Workspaces[0].WorkspaceID)
	assert.Equal(t, "owner", resp.Workspaces[0].Role)
	assert.True(t, resp.Workspaces[0].MagicLinkCapable)
	assert.Equal(t, "ws-shared", resp.Workspaces[1].WorkspaceID)
	assert.Equal(t, "member", resp.Workspaces[1].Role)
}

// === Test 2 : user inconnu → 200 found:false workspaces:[] ===

func TestDiscovery_UserNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	email := "ghost@example.com"
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().LookupByEmail(gomock.Any(), email).Return(&domain.DiscoveryResponse{
		Found:      false,
		UserEmail:  email,
		Workspaces: []domain.DiscoveryWorkspace{},
	}, nil)

	body := fmt.Sprintf(`{"email":"%s"}`, email)
	rec := invokeDiscovery(t, svc, buildDiscoveryHMACRequest(t, body, 0))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domain.DiscoveryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Found)
	assert.Equal(t, email, resp.UserEmail)
	assert.Empty(t, resp.Workspaces)
}

// === Test 3 : HMAC invalide → 401 ===

func TestDiscovery_HMACInvalid(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	// Service ne doit PAS etre appele.

	tsStr := strconv.FormatInt(time.Now().UnixMilli(), 10)
	req := httptest.NewRequest(http.MethodPost, "/api/users/by-email",
		bytes.NewReader([]byte(`{"email":"alice@example.com"}`)))
	req.Header.Set("X-Veridian-Hub-Signature", "0000000000000000deadbeef")
	req.Header.Set("X-Veridian-Timestamp", tsStr)

	rec := invokeDiscovery(t, svc, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// === Test 4 : drift > 5min → 401 ===

func TestDiscovery_HMACDrift(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	// Service ne doit PAS etre appele.

	body := `{"email":"alice@example.com"}`
	rec := invokeDiscovery(t, svc, buildDiscoveryHMACRequest(t, body, -6*time.Minute))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// === Test 5 : email manquant → 400 ===

func TestDiscovery_MissingEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)

	rec := invokeDiscovery(t, svc, buildDiscoveryHMACRequest(t, `{}`, 0))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "email is required")
}

// === Test 6 : email invalide (pas de @) → 400 ===

func TestDiscovery_InvalidEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)

	rec := invokeDiscovery(t, svc, buildDiscoveryHMACRequest(t, `{"email":"notanemail"}`, 0))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "email is invalid")
}

// === Test 7 : user connu, 0 workspace → 200 found:true workspaces:[] ===

func TestDiscovery_UserFoundZeroWorkspaces(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	email := "solo@example.com"
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().LookupByEmail(gomock.Any(), email).Return(&domain.DiscoveryResponse{
		Found:      true,
		UserEmail:  email,
		Workspaces: []domain.DiscoveryWorkspace{},
	}, nil)

	body := fmt.Sprintf(`{"email":"%s"}`, email)
	rec := invokeDiscovery(t, svc, buildDiscoveryHMACRequest(t, body, 0))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domain.DiscoveryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Found)
	assert.Empty(t, resp.Workspaces)
}

// === Test 8 : erreur DB → 500 ===

func TestDiscovery_InternalError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	email := "bob@example.com"
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().LookupByEmail(gomock.Any(), email).Return(nil, errors.New("db connection lost"))

	body := fmt.Sprintf(`{"email":"%s"}`, email)
	rec := invokeDiscovery(t, svc, buildDiscoveryHMACRequest(t, body, 0))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	// Le 500 ne doit pas leak l'erreur interne brute (DB) au client (axe 2) :
	// message générique, erreur confinée aux logs ; le code machine reste exposé.
	assert.NotContains(t, rec.Body.String(), "db connection lost", "le 500 ne doit pas leak l'erreur interne brute")
	assert.Contains(t, rec.Body.String(), "internal_error")
}

// === Bonus : JSON malformé → 400 ===

func TestDiscovery_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)

	rec := invokeDiscovery(t, svc, buildDiscoveryHMACRequest(t, `not-json`, 0))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// === Bonus : email avec espaces autour → trimme et accepte ===

func TestDiscovery_EmailTrimmed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trimmedEmail := "alice@example.com"
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().LookupByEmail(gomock.Any(), trimmedEmail).Return(&domain.DiscoveryResponse{
		Found:      false,
		UserEmail:  trimmedEmail,
		Workspaces: []domain.DiscoveryWorkspace{},
	}, nil)

	body := fmt.Sprintf(`{"email":"  %s  "}`, trimmedEmail)
	rec := invokeDiscovery(t, svc, buildDiscoveryHMACRequest(t, body, 0))
	assert.Equal(t, http.StatusOK, rec.Code)
}

// === GET variant ===
//
// Le Hub `lib/sync/discovery.ts` appelle en GET avec querystring depuis le
// cron reconcile. Le bug prod 2026-05-25 (200 body vide) venait du fait que
// seule la route POST etait declaree — GET tombait dans le catchall
// root_handler.go qui sort silencieusement sur tout `/api/*` non-matche.

// buildDiscoveryHMACGetRequest : signature HMAC body vide (`${ts}.`) comme
// le client Hub `signGet()` (cf. veridian-hub/lib/sync/discovery.ts:110).
func buildDiscoveryHMACGetRequest(t *testing.T, email string, timestampOffset time.Duration) *http.Request {
	t.Helper()
	ts := time.Now().Add(timestampOffset)
	tsStr := strconv.FormatInt(ts.UnixMilli(), 10)

	mac := hmac.New(sha256.New, []byte(discoveryTestSecret))
	mac.Write([]byte(tsStr))
	mac.Write([]byte("."))
	// body vide pour GET — signature = HMAC(secret, "${ts}.")
	sig := hex.EncodeToString(mac.Sum(nil))

	url := "/api/users/by-email"
	if email != "" {
		url = fmt.Sprintf("/api/users/by-email?email=%s", email)
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("X-Veridian-Hub-Signature", sig)
	req.Header.Set("X-Veridian-Timestamp", tsStr)
	return req
}

// === Test GET 1 : user connu + 2 workspaces → 200 found:true ===

func TestDiscoveryGET_FoundTwoWorkspaces(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	email := "alice@example.com"
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().LookupByEmail(gomock.Any(), email).Return(&domain.DiscoveryResponse{
		Found:     true,
		UserEmail: email,
		Workspaces: []domain.DiscoveryWorkspace{
			{
				WorkspaceID:      "ws-alice",
				WorkspaceName:    "Alice Corp",
				Role:             "owner",
				Plan:             "pro",
				MagicLinkCapable: true,
				FallbackURL:      "https://notifuse.app.veridian.site/console/signin",
			},
			{
				WorkspaceID:      "ws-shared",
				WorkspaceName:    "Shared Project",
				Role:             "member",
				Plan:             "free",
				MagicLinkCapable: true,
				FallbackURL:      "https://notifuse.app.veridian.site/console/signin",
			},
		},
	}, nil)

	rec := invokeDiscovery(t, svc, buildDiscoveryHMACGetRequest(t, email, 0))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Body.Bytes(), "GET response body must not be empty (bug prod 2026-05-25)")
	var resp domain.DiscoveryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Found)
	assert.Equal(t, email, resp.UserEmail)
	require.Len(t, resp.Workspaces, 2)
	assert.Equal(t, "ws-alice", resp.Workspaces[0].WorkspaceID)
}

// === Test GET 2 : user inconnu → 200 found:false ===

func TestDiscoveryGET_UserNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	email := "ghost@example.com"
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().LookupByEmail(gomock.Any(), email).Return(&domain.DiscoveryResponse{
		Found:      false,
		UserEmail:  email,
		Workspaces: []domain.DiscoveryWorkspace{},
	}, nil)

	rec := invokeDiscovery(t, svc, buildDiscoveryHMACGetRequest(t, email, 0))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domain.DiscoveryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Found)
	assert.Empty(t, resp.Workspaces)
}

// === Test GET 3 : email manquant en query → 400 ===

func TestDiscoveryGET_MissingEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	// service ne doit pas etre appele

	rec := invokeDiscovery(t, svc, buildDiscoveryHMACGetRequest(t, "", 0))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "email is required")
}

// === Test GET 4 : HMAC absent → 401 ===

func TestDiscoveryGET_NoHMAC(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)

	req := httptest.NewRequest(http.MethodGet, "/api/users/by-email?email=alice@example.com", nil)
	rec := invokeDiscovery(t, svc, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// === Test GET 5 : email URL-encode (cas Hub `encodeURIComponent`) ===

func TestDiscoveryGET_URLEncodedEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	email := "alice+test@example.com"
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().LookupByEmail(gomock.Any(), email).Return(&domain.DiscoveryResponse{
		Found:      false,
		UserEmail:  email,
		Workspaces: []domain.DiscoveryWorkspace{},
	}, nil)

	// `+` doit etre encode en `%2B` pour ne pas etre interprete comme espace
	rec := invokeDiscovery(t, svc, buildDiscoveryHMACGetRequest(t, "alice%2Btest@example.com", 0))
	assert.Equal(t, http.StatusOK, rec.Code)
}
