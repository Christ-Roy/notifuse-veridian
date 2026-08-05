package service

import (
	"context"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emailProfileUsageRepoStub struct {
	rows  []domain.VeridianEmailProfileUsageRow
	since time.Time
}

func (s *emailProfileUsageRepoStub) GetEmailProfileUsage(_ context.Context, _ string, since time.Time) ([]domain.VeridianEmailProfileUsageRow, error) {
	s.since = since
	return s.rows, nil
}

func TestVeridianEmailProfileUsageService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	auth := mocks.NewMockAuthService(ctrl)
	workspaces := mocks.NewMockWorkspaceRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	membership := &domain.UserWorkspace{Permissions: domain.UserPermissions{
		domain.PermissionResourceMessageHistory: {Read: true},
	}}
	auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, membership, nil)
	workspace := &domain.Workspace{
		ID: "ws1", Settings: domain.WorkspaceSettings{MarketingEmailProviderID: "p1"},
		Integrations: []domain.Integration{{ID: "p1", Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, SMTP: &domain.SMTPSettings{Host: "smtp.gmail.com"}}}},
	}
	workspaces.EXPECT().GetByID(gomock.Any(), "ws1").Return(workspace, nil)
	usageRepo := &emailProfileUsageRepoStub{rows: []domain.VeridianEmailProfileUsageRow{
		{ProfileID: "p1", ReservedUsed: 5},
		{ProfileID: "p1", ProviderClass: "google", AcceptedUsed: 4},
	}}
	svc := NewVeridianEmailProfileUsageService(usageRepo, workspaces, auth, log)
	result, err := svc.GetEmailProfilesUsage(context.Background(), "ws1")
	require.NoError(t, err)
	require.Len(t, result.Profiles, 1)
	assert.Equal(t, 5, result.Profiles[0].Used)
	assert.Equal(t, 4, result.Profiles[0].AcceptedUsed)
	assert.Equal(t, 25, result.Profiles[0].Remaining)
	assert.Equal(t, time.UTC, usageRepo.since.Location())
	assert.Equal(t, 0, usageRepo.since.Hour(), "quota policy day starts at midnight UTC")
	assert.Equal(t, result.Date, usageRepo.since.Format("2006-01-02"))
}
