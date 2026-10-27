package service

import (
	"context"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

type fakeEnrollmentReader struct {
	calls    int
	gotStat  []domain.ContactAutomationStatus
	gotLimit int
	page     *domain.VeridianEnrollmentPage
	err      error
}

func (f *fakeEnrollmentReader) ListEnrollmentsPage(_ context.Context, fl domain.VeridianAutomationEnrollmentFilter, st []domain.ContactAutomationStatus) (*domain.VeridianEnrollmentPage, error) {
	f.calls++
	f.gotStat = st
	f.gotLimit = fl.Limit
	if f.page == nil {
		f.page = &domain.VeridianEnrollmentPage{}
	}
	return f.page, f.err
}

type enrollmentHarness struct {
	svc    domain.VeridianAutomationEnrollmentService
	reader *fakeEnrollmentReader
	auth   *mocks.MockAuthService
}

func newEnrollmentHarness(t *testing.T) *enrollmentHarness {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	h := &enrollmentHarness{reader: &fakeEnrollmentReader{}, auth: mocks.NewMockAuthService(ctrl)}
	h.svc = NewVeridianAutomationEnrollmentService(h.reader, h.auth, log)
	return h
}

func (h *enrollmentHarness) authAs(m *domain.UserWorkspace) {
	h.auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").
		Return(context.Background(), &domain.User{}, m, nil).AnyTimes()
}

func TestVeridianAutomationEnrollmentsService_RequiresAutomationsRead(t *testing.T) {
	h := newEnrollmentHarness(t)
	h.authAs(&domain.UserWorkspace{Role: "member", Permissions: domain.UserPermissions{
		domain.PermissionResourceContacts: {Read: true, Write: true},
	}})
	_, err := h.svc.ListEnrollments(context.Background(), domain.VeridianAutomationEnrollmentFilter{WorkspaceID: "ws1", AutomationID: "a1"})
	require.Error(t, err, "sans automations:read, refus")
	assert.Equal(t, 0, h.reader.calls, "le repository n'est jamais interroge sans droit")
}

func TestVeridianAutomationEnrollmentsService_AuthFailureStopsBeforeRead(t *testing.T) {
	h := newEnrollmentHarness(t)
	h.auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").
		Return(context.Background(), nil, nil, errors.New("cle revoquee"))
	_, err := h.svc.ListEnrollments(context.Background(), domain.VeridianAutomationEnrollmentFilter{WorkspaceID: "ws1", AutomationID: "a1"})
	require.Error(t, err)
	assert.Equal(t, 0, h.reader.calls)
}

func TestVeridianAutomationEnrollmentsService_InvalidFilterRejectedBeforeRead(t *testing.T) {
	h := newEnrollmentHarness(t)
	h.authAs(&domain.UserWorkspace{Role: "member", Permissions: domain.UserPermissions{
		domain.PermissionResourceAutomations: {Read: true},
	}})
	_, err := h.svc.ListEnrollments(context.Background(), domain.VeridianAutomationEnrollmentFilter{WorkspaceID: "ws1", AutomationID: "a1", Status: "bogus"})
	require.Error(t, err, "statut inconnu refuse")
	assert.Equal(t, 0, h.reader.calls)
}

func TestVeridianAutomationEnrollmentsService_PassesStatusesAndDefaultLimit(t *testing.T) {
	h := newEnrollmentHarness(t)
	h.authAs(&domain.UserWorkspace{Role: "member", Permissions: domain.UserPermissions{
		domain.PermissionResourceAutomations: {Read: true},
	}})
	_, err := h.svc.ListEnrollments(context.Background(), domain.VeridianAutomationEnrollmentFilter{WorkspaceID: "ws1", AutomationID: "a1", Status: "active"})
	require.NoError(t, err)
	assert.Equal(t, 1, h.reader.calls)
	assert.Equal(t, []domain.ContactAutomationStatus{domain.ContactAutomationStatusActive, domain.ContactAutomationStatusSending}, h.reader.gotStat)
	assert.Equal(t, domain.VeridianEnrollmentDefaultLimit, h.reader.gotLimit)
}
