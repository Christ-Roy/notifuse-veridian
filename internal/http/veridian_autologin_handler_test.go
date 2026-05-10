package http

// === Veridian patch ===
// Tests unitaires du VeridianAutoLoginHandler — endpoint
// GET /veridian/auto-login?token=<HMAC>.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeAutologinTokenForTest(t *testing.T, hubSecret, ws, email string) string {
	t.Helper()
	url, _, err := domain.BuildAutoLoginURL("https://api.example.com", hubSecret, ws, email)
	require.NoError(t, err)
	return url[strings.Index(url, "token=")+len("token="):]
}

func newAutologinHandler(svc domain.UserServiceInterface, hubSecret string) *VeridianAutoLoginHandler {
	return NewVeridianAutoLoginHandler(svc, hubSecret, "https://api.example.com", logger.NewLogger())
}

func TestVeridianAutoLogin_OK_ReturnsHTMLWithToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockUserServiceInterface(ctrl)
	svc.EXPECT().CreateAutoLoginSession(gomock.Any(), "u@x.test").Return(&domain.AuthResponse{
		Token:     "fake-jwt-token-1234567890",
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil)

	h := newAutologinHandler(svc, "secret-1")
	token := makeAutologinTokenForTest(t, "secret-1", "ws-1", "u@x.test")

	req := httptest.NewRequest(http.MethodGet, "/veridian/auto-login?token="+token, nil)
	rec := httptest.NewRecorder()
	h.handleAutoLogin(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "fake-jwt-token-1234567890")
	assert.Contains(t, body, "/console")
	assert.Contains(t, body, "localStorage.setItem")
	// Anti-XSS : le redirect URL doit etre encode (ici /console → safe)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "noindex, nofollow", rec.Header().Get("X-Robots-Tag"))
}

func TestVeridianAutoLogin_MissingToken_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockUserServiceInterface(ctrl)
	h := newAutologinHandler(svc, "secret-1")

	req := httptest.NewRequest(http.MethodGet, "/veridian/auto-login", nil)
	rec := httptest.NewRecorder()
	h.handleAutoLogin(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "Missing token")
}

func TestVeridianAutoLogin_NoHubSecret_503(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockUserServiceInterface(ctrl)
	h := newAutologinHandler(svc, "") // secret vide

	req := httptest.NewRequest(http.MethodGet, "/veridian/auto-login?token=anything", nil)
	rec := httptest.NewRecorder()
	h.handleAutoLogin(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "Service misconfigured")
}

func TestVeridianAutoLogin_InvalidToken_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockUserServiceInterface(ctrl)
	h := newAutologinHandler(svc, "secret-1")

	req := httptest.NewRequest(http.MethodGet, "/veridian/auto-login?token=garbage.0000", nil)
	rec := httptest.NewRecorder()
	h.handleAutoLogin(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "Invalid or expired sign-in link")
}

func TestVeridianAutoLogin_TokenSignedWithDifferentSecret_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockUserServiceInterface(ctrl)
	h := newAutologinHandler(svc, "secret-deployed")

	// Token signe avec un autre secret
	token := makeAutologinTokenForTest(t, "wrong-secret", "ws-1", "u@x.test")
	req := httptest.NewRequest(http.MethodGet, "/veridian/auto-login?token="+token, nil)
	rec := httptest.NewRecorder()
	h.handleAutoLogin(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVeridianAutoLogin_UserServiceError_401(t *testing.T) {
	// Token valide MAIS le user n'existe pas (provision n'a pas cree l'user
	// pour une raison donnee). Service retourne erreur.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockUserServiceInterface(ctrl)
	svc.EXPECT().CreateAutoLoginSession(gomock.Any(), "ghost@x.test").Return(nil, errors.New("user not found"))

	h := newAutologinHandler(svc, "secret-1")
	token := makeAutologinTokenForTest(t, "secret-1", "ws-1", "ghost@x.test")
	req := httptest.NewRequest(http.MethodGet, "/veridian/auto-login?token="+token, nil)
	rec := httptest.NewRecorder()
	h.handleAutoLogin(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "user not found")
}

func TestVeridianAutoLogin_HTMLEscapesError(t *testing.T) {
	// L'erreur respondError doit HTML-escape le message pour eviter XSS.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockUserServiceInterface(ctrl)
	h := newAutologinHandler(svc, "secret-1")

	// On utilise respondError directement pour tester le path
	rec := httptest.NewRecorder()
	h.respondError(rec, "<script>alert(1)</script>", http.StatusBadRequest)

	body := rec.Body.String()
	assert.NotContains(t, body, "<script>alert(1)</script>")
	assert.Contains(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;")
}

func TestVeridianAutoLogin_RegisterRoutes_GetMounted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockUserServiceInterface(ctrl)
	h := newAutologinHandler(svc, "secret-1")

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/veridian/auto-login?token=", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "GET /veridian/auto-login should be mounted")
}

// Re-export delegation : verifie que les helpers exportes par le package http
// (BuildAutoLoginURL, VerifyAutoLoginToken, AutoLoginPayload alias) renvoient
// les memes valeurs que ceux de domain/.
func TestVeridianAutoLogin_BuildAutoLoginURL_DelegatesToDomain(t *testing.T) {
	urlPkg, _, errPkg := BuildAutoLoginURL("https://api.example.com", "secret-1", "ws-1", "u@x.test")
	require.NoError(t, errPkg)
	urlDom, _, errDom := domain.BuildAutoLoginURL("https://api.example.com", "secret-1", "ws-1", "u@x.test")
	require.NoError(t, errDom)
	// URLs different sur le payload (timestamp), mais structure identique
	assert.Contains(t, urlPkg, "https://api.example.com/veridian/auto-login?token=")
	assert.Contains(t, urlDom, "https://api.example.com/veridian/auto-login?token=")
}

func TestVeridianAutoLogin_VerifyAutoLoginToken_DelegatesToDomain(t *testing.T) {
	token := makeAutologinTokenForTest(t, "secret-1", "ws-1", "u@x.test")
	pPkg, errPkg := VerifyAutoLoginToken(token, "secret-1")
	pDom, errDom := domain.VerifyAutoLoginToken(token, "secret-1")
	require.NoError(t, errPkg)
	require.NoError(t, errDom)
	assert.Equal(t, pPkg, pDom)
	assert.Equal(t, "ws-1", pPkg.WorkspaceID)
}
