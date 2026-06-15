package http

import (
	"bytes"
	"context"
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
	"github.com/Notifuse/notifuse/internal/service"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

// newEnrollHandler wires the real VeridianAutomationEnrollService (it takes a
// concrete type, not an interface) on top of mock repo + auth, so handler tests
// exercise the full HTTP→service path.
func newEnrollHandler(ctrl *gomock.Controller) (
	*VeridianAutomationEnrollHandler,
	*mocks.MockAutomationRepository,
	*mocks.MockAuthService,
) {
	repo := mocks.NewMockAutomationRepository(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().WithFields(gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	log.EXPECT().Warn(gomock.Any()).AnyTimes()
	svc := service.NewVeridianAutomationEnrollService(repo, authSvc, log)
	h := NewVeridianAutomationEnrollHandler(svc, func() ([]byte, error) { return []byte("test-secret"), nil }, log)
	return h, repo, authSvc
}

func writeWorkspace() *domain.UserWorkspace {
	return &domain.UserWorkspace{
		Role:        "member",
		Permissions: domain.UserPermissions{domain.PermissionResourceAutomations: {Read: true, Write: true}},
	}
}

func liveAuto() *domain.Automation {
	return &domain.Automation{
		ID:         "auto1",
		Status:     domain.AutomationStatusLive,
		RootNodeID: "root1",
		Trigger:    &domain.TimelineTriggerConfig{EventKind: "contact.created", Frequency: domain.TriggerFrequencyEveryTime},
	}
}

func TestVeridianAutomationEnrollHandler_POST_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, repo, authSvc := newEnrollHandler(ctrl)
	ctx := context.Background()

	authSvc.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").
		Return(ctx, &domain.User{}, writeWorkspace(), nil)
	repo.EXPECT().GetByID(gomock.Any(), "ws1", "auto1").Return(liveAuto(), nil)
	repo.EXPECT().GetContactAutomationByEmail(gomock.Any(), "ws1", "auto1", "a@example.com").
		Return(nil, errors.New("not found"))
	repo.EXPECT().EnrollContact(gomock.Any(), "ws1", "auto1", "root1", "a@example.com", gomock.Any()).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"workspace_id":   "ws1",
		"automation_id":  "auto1",
		"contact_emails": []string{"a@example.com"},
	})
	r := httptest.NewRequest(http.MethodPost, "/api/automations.enroll", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.handleEnroll(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domain.VeridianEnrollContactsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Enrolled)
	require.Len(t, resp.Results, 1)
	assert.Equal(t, "enrolled", resp.Results[0].Status)
}

func TestVeridianAutomationEnrollHandler_GET_405(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h, _, _ := newEnrollHandler(ctrl)

	r := httptest.NewRequest(http.MethodGet, "/api/automations.enroll", nil)
	rec := httptest.NewRecorder()
	h.handleEnroll(rec, r)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestVeridianAutomationEnrollHandler_InvalidJSON_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h, _, _ := newEnrollHandler(ctrl)

	r := httptest.NewRequest(http.MethodPost, "/api/automations.enroll", bytes.NewReader([]byte("{not json")))
	rec := httptest.NewRecorder()
	h.handleEnroll(rec, r)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianAutomationEnrollHandler_ValidationError_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h, _, _ := newEnrollHandler(ctrl)

	// missing automation_id
	body, _ := json.Marshal(map[string]interface{}{
		"workspace_id":   "ws1",
		"contact_emails": []string{"a@example.com"},
	})
	r := httptest.NewRequest(http.MethodPost, "/api/automations.enroll", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleEnroll(rec, r)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianAutomationEnrollHandler_PermissionDenied_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h, _, authSvc := newEnrollHandler(ctrl)
	ctx := context.Background()

	readOnly := &domain.UserWorkspace{
		Role:        "member",
		Permissions: domain.UserPermissions{domain.PermissionResourceAutomations: {Read: true, Write: false}},
	}
	authSvc.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").
		Return(ctx, &domain.User{}, readOnly, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"workspace_id":   "ws1",
		"automation_id":  "auto1",
		"contact_emails": []string{"a@example.com"},
	})
	r := httptest.NewRequest(http.MethodPost, "/api/automations.enroll", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleEnroll(rec, r)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVeridianAutomationEnrollHandler_AuthFailure_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h, _, authSvc := newEnrollHandler(ctrl)
	ctx := context.Background()

	authSvc.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").
		Return(ctx, nil, nil, errors.New("not a member of workspace"))

	body, _ := json.Marshal(map[string]interface{}{
		"workspace_id":   "ws1",
		"automation_id":  "auto1",
		"contact_emails": []string{"a@example.com"},
	})
	r := httptest.NewRequest(http.MethodPost, "/api/automations.enroll", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleEnroll(rec, r)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
