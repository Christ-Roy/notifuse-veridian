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

// Correctif 2026-09-29 (fusible de réputation) : preuve que le endpoint
// /api/veridian/messages.reputationStatus (POST+GET) expose bien le signal
// "visible dans l'interface ou l'API" exigé par la mission.

func newReputationStatusHandler(ctrl *gomock.Controller) (*VeridianReputationStatusHandler, *mocks.MockVeridianReputationStatusService) {
	svc := mocks.NewMockVeridianReputationStatusService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	h := NewVeridianReputationStatusHandler(svc, func() ([]byte, error) { return []byte("test-secret"), nil }, log)
	return h, svc
}

func TestVeridianReputationStatusHandler_GET(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReputationStatusHandler(ctrl)

	svc.EXPECT().GetReputationStatus(gomock.Any(), &domain.VeridianReputationStatusRequest{WorkspaceID: "ws123"}).
		Return(&domain.VeridianReputationStatusResponse{
			Integrations: []domain.VeridianReputationIntegrationStatus{
				{IntegrationID: "agence", SenderDomain: "agence-veridian.fr", Frozen: true, FrozenReason: "hard_bounce_rate", HardBounceRate: 0.167, Threshold: 0.03},
			},
			AnyFrozen: true,
		}, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.reputationStatus?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()

	h.handleReputationStatus(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domain.VeridianReputationStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.AnyFrozen)
	require.Len(t, resp.Integrations, 1)
	assert.Equal(t, "agence", resp.Integrations[0].IntegrationID)
	assert.True(t, resp.Integrations[0].Frozen)
}

func TestVeridianReputationStatusHandler_POST(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReputationStatusHandler(ctrl)

	svc.EXPECT().GetReputationStatus(gomock.Any(), &domain.VeridianReputationStatusRequest{WorkspaceID: "ws123"}).
		Return(&domain.VeridianReputationStatusResponse{Integrations: []domain.VeridianReputationIntegrationStatus{}}, nil)

	body, _ := json.Marshal(map[string]string{"workspace_id": "ws123"})
	r := httptest.NewRequest(http.MethodPost, "/api/veridian/messages.reputationStatus", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.handleReputationStatus(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianReputationStatusHandler_MissingWorkspaceID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newReputationStatusHandler(ctrl)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.reputationStatus", nil)
	rec := httptest.NewRecorder()

	h.handleReputationStatus(rec, r)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianReputationStatusHandler_PermissionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReputationStatusHandler(ctrl)

	svc.EXPECT().GetReputationStatus(gomock.Any(), gomock.Any()).
		Return(nil, domain.NewPermissionError(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead, "nope"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.reputationStatus?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()

	h.handleReputationStatus(rec, r)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVeridianReputationStatusHandler_ServiceError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReputationStatusHandler(ctrl)

	svc.EXPECT().GetReputationStatus(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("db down"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.reputationStatus?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()

	h.handleReputationStatus(rec, r)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// Mission 2026-10-03 "401/403 partout" : remplace l'ancien isAuthFailure
// (matching par prefixe de message) par la classification partagee
// (WriteAuthAwareError) -- preuve que cle revoquee -> 401, pas 500.
func TestVeridianReputationStatusHandler_AuthFailure_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReputationStatusHandler(ctrl)

	svc.EXPECT().GetReputationStatus(gomock.Any(), gomock.Any()).
		Return(nil, &domain.ErrAuthenticationFailed{Message: "api key revoked"})

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.reputationStatus?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()

	h.handleReputationStatus(rec, r)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVeridianReputationStatusHandler_RegisterRoutes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newReputationStatusHandler(ctrl)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Une requête sans JWT doit être rejetée par RequireAuth (401), preuve que
	// les deux méthodes sont bien routées (Go 1.22+ exige la méthode explicite
	// dans le pattern — piège P0 du catchall SPA sinon).
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "/api/veridian/messages.reputationStatus?workspace_id=ws1", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "method %s should be routed and hit RequireAuth", method)
	}
}
