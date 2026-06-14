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
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

func newBreakdownHandler(ctrl *gomock.Controller) (*VeridianContactBreakdownHandler, *mocks.MockVeridianContactProviderBreakdownService) {
	svc := mocks.NewMockVeridianContactProviderBreakdownService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	h := NewVeridianContactBreakdownHandler(svc, func() ([]byte, error) { return []byte("test-secret"), nil }, log)
	return h, svc
}

func sampleBreakdown() *domain.VeridianProviderBreakdown {
	return &domain.VeridianProviderBreakdown{
		Breakdown: map[string]int{
			domain.ProviderClassGoogle:     5,
			domain.ProviderClassMicrosoft:  3,
			domain.ProviderClassYahooAol:   1,
			domain.ProviderClassFreemailFR: 2,
			domain.ProviderClassCorporate:  4,
		},
		Total: 15,
	}
}

func TestVeridianContactBreakdownHandler_POST(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newBreakdownHandler(ctrl)

	svc.EXPECT().GetProviderBreakdown(gomock.Any(), &domain.VeridianProviderBreakdownRequest{
		WorkspaceID: "ws123",
		ListID:      "list-1",
	}).Return(sampleBreakdown(), nil)

	body, _ := json.Marshal(map[string]string{"workspace_id": "ws123", "list_id": "list-1"})
	r := httptest.NewRequest(http.MethodPost, "/api/veridian/contacts.providerBreakdown", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.handleProviderBreakdown(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domain.VeridianProviderBreakdown
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 15, resp.Total)
	assert.Equal(t, 5, resp.Breakdown[domain.ProviderClassGoogle])
}

func TestVeridianContactBreakdownHandler_GET(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newBreakdownHandler(ctrl)

	svc.EXPECT().GetProviderBreakdown(gomock.Any(), &domain.VeridianProviderBreakdownRequest{
		WorkspaceID: "ws123",
	}).Return(sampleBreakdown(), nil)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/contacts.providerBreakdown?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()

	h.handleProviderBreakdown(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianContactBreakdownHandler_QueryParamsOnPost(t *testing.T) {
	// POST sans body mais workspace_id en query (tolérance) -> doit marcher.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newBreakdownHandler(ctrl)
	svc.EXPECT().GetProviderBreakdown(gomock.Any(), &domain.VeridianProviderBreakdownRequest{
		WorkspaceID: "wsQuery",
	}).Return(sampleBreakdown(), nil)

	r := httptest.NewRequest(http.MethodPost, "/api/veridian/contacts.providerBreakdown?workspace_id=wsQuery", nil)
	rec := httptest.NewRecorder()
	h.handleProviderBreakdown(rec, r)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianContactBreakdownHandler_MissingWorkspaceID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newBreakdownHandler(ctrl)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/contacts.providerBreakdown", nil)
	rec := httptest.NewRecorder()
	h.handleProviderBreakdown(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianContactBreakdownHandler_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newBreakdownHandler(ctrl)

	r := httptest.NewRequest(http.MethodPost, "/api/veridian/contacts.providerBreakdown", bytes.NewReader([]byte("{bad json")))
	rec := httptest.NewRecorder()
	h.handleProviderBreakdown(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianContactBreakdownHandler_PermissionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newBreakdownHandler(ctrl)
	svc.EXPECT().GetProviderBreakdown(gomock.Any(), gomock.Any()).
		Return(nil, domain.NewPermissionError(domain.PermissionResourceContacts, domain.PermissionTypeRead, "no read"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/contacts.providerBreakdown?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleProviderBreakdown(rec, r)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVeridianContactBreakdownHandler_AuthFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newBreakdownHandler(ctrl)
	svc.EXPECT().GetProviderBreakdown(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("failed to authenticate user: not a member"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/contacts.providerBreakdown?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleProviderBreakdown(rec, r)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVeridianContactBreakdownHandler_InternalError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newBreakdownHandler(ctrl)
	svc.EXPECT().GetProviderBreakdown(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("db down"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/contacts.providerBreakdown?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleProviderBreakdown(rec, r)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVeridianContactBreakdownHandler_RegisterRoutes(t *testing.T) {
	// Vérifie que POST ET GET sont routés explicitement (piège catchall
	// root_handler.go). Sans Authorization header, RequireAuth renvoie 401 —
	// donc le service n'est jamais appelé (pas d'attente sur le mock). Le point
	// clé : 401 (et NON 404/405) prouve que les DEUX méthodes atteignent bien le
	// middleware via le mux, donc qu'aucune ne tombe dans le catchall SPA.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newBreakdownHandler(ctrl)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "/api/veridian/contacts.providerBreakdown?workspace_id=ws123", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "method %s should reach RequireAuth (401), not the catchall", method)
		assert.NotEqual(t, http.StatusNotFound, rec.Code, "method %s not routed (catchall trap)", method)
		assert.NotEqual(t, http.StatusMethodNotAllowed, rec.Code, "method %s rejected by mux", method)
	}
}
