package service

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

type fakeQueueEntriesRepo struct {
	listCalls   int
	cancelCalls int
	gotActor    string
	gotReq      domain.VeridianQueueCancelRequest
}

func (f *fakeQueueEntriesRepo) ListEntries(_ context.Context, _ domain.VeridianQueueEntriesFilter) (*domain.VeridianQueueEntriesPage, error) {
	f.listCalls++
	return &domain.VeridianQueueEntriesPage{}, nil
}

func (f *fakeQueueEntriesRepo) CancelEntries(_ context.Context, req domain.VeridianQueueCancelRequest, actor string) (*domain.VeridianQueueCancelResult, error) {
	f.cancelCalls++
	f.gotReq, f.gotActor = req, actor
	return &domain.VeridianQueueCancelResult{}, nil
}

type queueEntriesHarness struct {
	svc  domain.VeridianQueueEntriesService
	repo *fakeQueueEntriesRepo
	auth *mocks.MockAuthService
}

func newQueueEntriesHarness(t *testing.T) *queueEntriesHarness {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	h := &queueEntriesHarness{repo: &fakeQueueEntriesRepo{}, auth: mocks.NewMockAuthService(ctrl)}
	h.svc = NewVeridianQueueEntriesService(h.repo, h.auth, log)
	return h
}

func (h *queueEntriesHarness) authAs(user *domain.User, m *domain.UserWorkspace) {
	h.auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").
		Return(context.Background(), user, m, nil).AnyTimes()
}

func autoPerms(read, write bool) *domain.UserWorkspace {
	return &domain.UserWorkspace{Role: "member", Permissions: domain.UserPermissions{
		domain.PermissionResourceAutomations: {Read: read, Write: write},
	}}
}

func TestVeridianQueueEntriesService_ListNeedsAutomationsRead(t *testing.T) {
	h := newQueueEntriesHarness(t)
	h.authAs(&domain.User{}, autoPerms(false, true))
	_, err := h.svc.ListEntries(context.Background(), domain.VeridianQueueEntriesFilter{WorkspaceID: "ws1", AutomationID: "a1"})
	require.Error(t, err)
	assert.Equal(t, 0, h.repo.listCalls)
}

func TestVeridianQueueEntriesService_CancelNeedsAutomationsWrite(t *testing.T) {
	h := newQueueEntriesHarness(t)
	h.authAs(&domain.User{Email: "lecteur@x.fr"}, autoPerms(true, false))
	_, err := h.svc.CancelEntries(context.Background(), domain.VeridianQueueCancelRequest{WorkspaceID: "ws1", AutomationID: "a1", Reason: "test"})
	require.Error(t, err, "annuler exige l'ecriture : un lecteur ne peut pas retirer de l'envoi")
	assert.Equal(t, 0, h.repo.cancelCalls)
}

func TestVeridianQueueEntriesService_CancelRecordsActorAndRejectsMissingReason(t *testing.T) {
	h := newQueueEntriesHarness(t)
	h.authAs(&domain.User{Email: "robert@x.fr"}, autoPerms(true, true))

	_, err := h.svc.CancelEntries(context.Background(), domain.VeridianQueueCancelRequest{WorkspaceID: "ws1", AutomationID: "a1"})
	require.Error(t, err, "sans motif : refus avant le repository")
	assert.Equal(t, 0, h.repo.cancelCalls)

	_, err = h.svc.CancelEntries(context.Background(), domain.VeridianQueueCancelRequest{WorkspaceID: "ws1", AutomationID: "a1", Reason: "retrait demande"})
	require.NoError(t, err)
	assert.Equal(t, 1, h.repo.cancelCalls)
	assert.Equal(t, "robert@x.fr", h.repo.gotActor, "l'acteur est celui de la session, pas une valeur envoyee par le client")
	assert.Equal(t, domain.VeridianQueueCancelDefaultLimit, h.repo.gotReq.Limit)
}
