package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emailProfileUsageServiceStub struct {
	workspaceID string
	result      *domain.VeridianEmailProfilesUsage
}

func (s *emailProfileUsageServiceStub) GetEmailProfilesUsage(_ context.Context, workspaceID string) (*domain.VeridianEmailProfilesUsage, error) {
	s.workspaceID = workspaceID
	return s.result, nil
}

func TestVeridianEmailProfileUsageHandlerGET(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := pkgmocks.NewMockLogger(ctrl)
	svc := &emailProfileUsageServiceStub{result: &domain.VeridianEmailProfilesUsage{Date: "2026-08-05", TotalUsed: 4}}
	h := NewVeridianEmailProfileUsageHandler(svc, func() ([]byte, error) { return []byte("secret"), nil }, log)
	req := httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.usage?workspace_id=ws1", nil)
	rec := httptest.NewRecorder()
	h.handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ws1", svc.workspaceID)
	var body domain.VeridianEmailProfilesUsage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 4, body.TotalUsed)
}

func TestVeridianEmailProfileUsageHandlerRequiresWorkspace(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := pkgmocks.NewMockLogger(ctrl)
	h := NewVeridianEmailProfileUsageHandler(&emailProfileUsageServiceStub{}, func() ([]byte, error) { return []byte("secret"), nil }, log)
	rec := httptest.NewRecorder()
	h.handle(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.usage", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
