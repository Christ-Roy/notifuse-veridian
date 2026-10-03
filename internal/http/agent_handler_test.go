package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Veridian patch — mission "API & agents" (2026-10-03) ===

func setupAgentHandlerTest(t *testing.T, cliBinaryPath string) (*AgentHandler, *mocks.MockWorkspaceServiceInterface, *http.ServeMux) {
	ctrl := gomock.NewController(t)
	workspaceSvc := mocks.NewMockWorkspaceServiceInterface(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	handler := NewAgentHandler(workspaceSvc, mockLogger, cliBinaryPath, "notifuse.app.veridian.site")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	return handler, workspaceSvc, mux
}

func TestNewAgentHandler(t *testing.T) {
	_, _, mux := setupAgentHandlerTest(t, "")
	require.NotNil(t, mux)
}

// A path the ServeMux has no pattern for falls through to its built-in
// NotFoundHandler, whose body is this exact literal string — distinct from
// any JSON body our own handlers write (including a legitimate business
// 404 like "install token not found" on /api/agent.exchangeToken).
const muxDefaultNotFoundBody = "404 page not found\n"

func TestAgentHandler_RegisterRoutes_AllRoutesRespond(t *testing.T) {
	_, workspaceSvc, mux := setupAgentHandlerTest(t, "")
	workspaceSvc.EXPECT().ExchangeAgentInstallToken(gomock.Any(), "x").Return(nil, domain.ErrAgentInstallTokenNotFound).AnyTimes()

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/agent/install.sh"},
		{http.MethodGet, "/agent/notifuse"},
		{http.MethodGet, "/agent/skill.tar.gz"},
		{http.MethodPost, "/api/agent.exchangeToken"},
	}

	for _, r := range routes {
		var req *http.Request
		if r.method == http.MethodPost {
			req = httptest.NewRequest(r.method, r.path, strings.NewReader(`{"token":"x"}`))
		} else {
			req = httptest.NewRequest(r.method, r.path, nil)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		// The route must be REGISTERED: the mux's own catch-all 404 must
		// never fire. /api/agent.exchangeToken legitimately answers 404
		// itself (unknown token, per the mock above) — that's a handled
		// business response, not a routing miss, so we check the BODY
		// (never the mux's default text) instead of the status code.
		assert.NotEqual(t, muxDefaultNotFoundBody, w.Body.String(), "route %s %s should be registered", r.method, r.path)
	}
}

func TestAgentHandler_HandleInstallScript(t *testing.T) {
	_, _, mux := setupAgentHandlerTest(t, "")

	req := httptest.NewRequest(http.MethodGet, "/agent/install.sh", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "#!/bin/sh")
	assert.Contains(t, body, "notifuse.app.veridian.site")
	assert.NotContains(t, body, "__NOTIFUSE_API_HOST__", "the host placeholder must be substituted, never served literally")
	assert.Contains(t, body, "NOTIFUSE_API_KEY", "the script must install the key into the env file, never print it")
}

func TestAgentHandler_HandleInstallScript_MethodNotAllowed(t *testing.T) {
	handler, _, _ := setupAgentHandlerTest(t, "")

	req := httptest.NewRequest(http.MethodPost, "/agent/install.sh", nil)
	w := httptest.NewRecorder()
	handler.handleInstallScript(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestAgentHandler_HandleCLIBinary_NotConfigured503(t *testing.T) {
	_, _, mux := setupAgentHandlerTest(t, "")

	req := httptest.NewRequest(http.MethodGet, "/agent/notifuse", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestAgentHandler_HandleCLIBinary_ConfiguredButMissingFile503(t *testing.T) {
	_, _, mux := setupAgentHandlerTest(t, "/does/not/exist/notifuse")

	req := httptest.NewRequest(http.MethodGet, "/agent/notifuse", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestAgentHandler_HandleCLIBinary_Served(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notifuse")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho fake-cli\n"), 0755))

	_, _, mux := setupAgentHandlerTest(t, path)

	req := httptest.NewRequest(http.MethodGet, "/agent/notifuse", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "fake-cli")
}

func TestAgentHandler_HandleSkillTarball(t *testing.T) {
	_, _, mux := setupAgentHandlerTest(t, "")

	req := httptest.NewRequest(http.MethodGet, "/agent/skill.tar.gz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/gzip", w.Header().Get("Content-Type"))
	assert.NotEmpty(t, w.Body.Bytes())
}

func TestAgentHandler_HandleExchangeToken_Success(t *testing.T) {
	_, workspaceSvc, mux := setupAgentHandlerTest(t, "")

	workspaceSvc.EXPECT().
		ExchangeAgentInstallToken(gomock.Any(), "valid-raw-token").
		Return(&domain.AgentInstallCredentials{
			APIKey:      "jwt-token-value",
			APIURL:      "https://notifuse.app.veridian.site",
			WorkspaceID: "ws-1",
		}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/agent.exchangeToken", strings.NewReader(`{"token":"valid-raw-token"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "NOTIFUSE_API_KEY=jwt-token-value")
	assert.Contains(t, body, "NOTIFUSE_WORKSPACE=ws-1")
}

func TestAgentHandler_HandleExchangeToken_ReusedTokenRefused(t *testing.T) {
	_, workspaceSvc, mux := setupAgentHandlerTest(t, "")

	workspaceSvc.EXPECT().
		ExchangeAgentInstallToken(gomock.Any(), "already-used").
		Return(nil, domain.ErrAgentInstallTokenUsed)

	req := httptest.NewRequest(http.MethodPost, "/api/agent.exchangeToken", strings.NewReader(`{"token":"already-used"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
	var response map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
	assert.Equal(t, "install token already used", response["error"])
}

func TestAgentHandler_HandleExchangeToken_ExpiredTokenRefused(t *testing.T) {
	_, workspaceSvc, mux := setupAgentHandlerTest(t, "")

	workspaceSvc.EXPECT().
		ExchangeAgentInstallToken(gomock.Any(), "expired-one").
		Return(nil, domain.ErrAgentInstallTokenExpired)

	req := httptest.NewRequest(http.MethodPost, "/api/agent.exchangeToken", strings.NewReader(`{"token":"expired-one"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
	var response map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
	assert.Equal(t, "install token expired", response["error"])
}

func TestAgentHandler_HandleExchangeToken_UnknownTokenRefused(t *testing.T) {
	_, workspaceSvc, mux := setupAgentHandlerTest(t, "")

	workspaceSvc.EXPECT().
		ExchangeAgentInstallToken(gomock.Any(), "never-existed").
		Return(nil, domain.ErrAgentInstallTokenNotFound)

	req := httptest.NewRequest(http.MethodPost, "/api/agent.exchangeToken", strings.NewReader(`{"token":"never-existed"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAgentHandler_HandleExchangeToken_MissingToken(t *testing.T) {
	_, _, mux := setupAgentHandlerTest(t, "")

	req := httptest.NewRequest(http.MethodPost, "/api/agent.exchangeToken", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAgentHandler_HandleExchangeToken_MethodNotAllowed(t *testing.T) {
	handler, _, _ := setupAgentHandlerTest(t, "")

	req := httptest.NewRequest(http.MethodGet, "/api/agent.exchangeToken", nil)
	w := httptest.NewRecorder()
	handler.handleExchangeToken(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestAgentHandler_HandleExchangeToken_ServiceErrorStays500(t *testing.T) {
	_, workspaceSvc, mux := setupAgentHandlerTest(t, "")

	workspaceSvc.EXPECT().
		ExchangeAgentInstallToken(gomock.Any(), "x").
		Return(nil, assert.AnError)

	req := httptest.NewRequest(http.MethodPost, "/api/agent.exchangeToken", strings.NewReader(`{"token":"x"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
