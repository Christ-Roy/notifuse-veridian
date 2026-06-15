package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	deliverability "github.com/Notifuse/notifuse/pkg/veridian_deliverability"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

func newScoreHandler(ctrl *gomock.Controller) (*VeridianDeliverabilityScoreHandler, *mocks.MockVeridianDeliverabilityScoreService) {
	svc := mocks.NewMockVeridianDeliverabilityScoreService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	h := NewVeridianDeliverabilityScoreHandler(svc, func() ([]byte, error) { return []byte("test-secret"), nil }, log)
	return h, svc
}

func sampleResult() *deliverability.Result {
	return &deliverability.Result{
		Score:   2.5,
		IsRisky: false,
		Mode:    "strict",
		Rules: []deliverability.Rule{
			{Name: "LINK_IN_STRICT_MODE", Weight: 1.2, Message: "des liens"},
		},
		Summary: "Profil correct",
	}
}

func TestVeridianDeliverabilityScoreHandler_POST(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newScoreHandler(ctrl)

	svc.EXPECT().Score(gomock.Any(), &domain.VeridianDeliverabilityScoreRequest{
		WorkspaceID:   "ws123",
		Subject:       "Bonjour",
		Body:          "Salut Marie",
		IsHTML:        true,
		ProviderClass: "google",
	}).Return(sampleResult(), nil)

	body, _ := json.Marshal(map[string]interface{}{
		"workspace_id":   "ws123",
		"subject":        "Bonjour",
		"body":           "Salut Marie",
		"is_html":        true,
		"provider_class": "google",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/veridian/templates.deliverabilityScore", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.handleScore(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp deliverability.Result
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 2.5, resp.Score)
	assert.Equal(t, "strict", resp.Mode)
	require.Len(t, resp.Rules, 1)
	assert.Equal(t, "LINK_IN_STRICT_MODE", resp.Rules[0].Name)
}

func TestVeridianDeliverabilityScoreHandler_GET(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newScoreHandler(ctrl)

	svc.EXPECT().Score(gomock.Any(), &domain.VeridianDeliverabilityScoreRequest{
		WorkspaceID:   "ws123",
		Subject:       "Hello",
		Body:          "world",
		IsHTML:        true,
		ProviderClass: "microsoft",
		Mode:          "strict",
	}).Return(sampleResult(), nil)

	r := httptest.NewRequest(http.MethodGet,
		"/api/veridian/templates.deliverabilityScore?workspace_id=ws123&subject=Hello&body=world&is_html=true&provider_class=microsoft&mode=strict", nil)
	rec := httptest.NewRecorder()

	h.handleScore(rec, r)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianDeliverabilityScoreHandler_QueryOverridesPostBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newScoreHandler(ctrl)
	// POST sans body, workspace_id en query (tolérance).
	svc.EXPECT().Score(gomock.Any(), &domain.VeridianDeliverabilityScoreRequest{
		WorkspaceID: "wsQuery",
	}).Return(sampleResult(), nil)

	r := httptest.NewRequest(http.MethodPost, "/api/veridian/templates.deliverabilityScore?workspace_id=wsQuery", nil)
	rec := httptest.NewRecorder()
	h.handleScore(rec, r)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianDeliverabilityScoreHandler_MissingWorkspaceID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newScoreHandler(ctrl)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/templates.deliverabilityScore", nil)
	rec := httptest.NewRecorder()
	h.handleScore(rec, r)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianDeliverabilityScoreHandler_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newScoreHandler(ctrl)

	r := httptest.NewRequest(http.MethodPost, "/api/veridian/templates.deliverabilityScore", bytes.NewReader([]byte("{bad json")))
	rec := httptest.NewRecorder()
	h.handleScore(rec, r)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianDeliverabilityScoreHandler_PermissionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newScoreHandler(ctrl)
	svc.EXPECT().Score(gomock.Any(), gomock.Any()).
		Return(nil, domain.NewPermissionError(domain.PermissionResourceTemplates, domain.PermissionTypeRead, "no read"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/templates.deliverabilityScore?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleScore(rec, r)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVeridianDeliverabilityScoreHandler_AuthFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newScoreHandler(ctrl)
	svc.EXPECT().Score(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("failed to authenticate user: not a member"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/templates.deliverabilityScore?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleScore(rec, r)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVeridianDeliverabilityScoreHandler_InternalError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newScoreHandler(ctrl)
	svc.EXPECT().Score(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("boom"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/templates.deliverabilityScore?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleScore(rec, r)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVeridianDeliverabilityScoreHandler_RegisterRoutes(t *testing.T) {
	// Vérifie que POST ET GET sont routés explicitement (piège catchall
	// root_handler.go). Sans Authorization header, RequireAuth renvoie 401 — donc
	// 401 (et NON 404/405) prouve que les DEUX méthodes atteignent le middleware.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newScoreHandler(ctrl)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "/api/veridian/templates.deliverabilityScore?workspace_id=ws123", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "method %s should reach RequireAuth (401), not catchall", method)
		assert.NotEqual(t, http.StatusNotFound, rec.Code, "method %s not routed (catchall trap)", method)
		assert.NotEqual(t, http.StatusMethodNotAllowed, rec.Code, "method %s rejected by mux", method)
	}
}
