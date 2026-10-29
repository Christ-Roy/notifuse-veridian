// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/service/broadcast_service_test.go ; ne contient que les declarations absentes du fork.

package service

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroadcastService_RefreshGlobalFeed_PermissionDenied(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	d := setupBroadcastSvc(t)
	defer d.ctrl.Finish()

	ctx := context.Background()
	req := &domain.RefreshGlobalFeedRequest{WorkspaceID: "w1", BroadcastID: "b1", URL: "https://example.com/feed", Headers: []domain.DataFeedHeader{}}

	// Read-only member: a member of the workspace without broadcasts:write.
	userWorkspace := &domain.UserWorkspace{
		UserID:      "user1",
		WorkspaceID: req.WorkspaceID,
		Role:        "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceBroadcasts: {Read: true, Write: false},
		},
	}
	d.authService.EXPECT().AuthenticateUserForWorkspace(ctx, req.WorkspaceID).Return(ctx, &domain.User{ID: "user1"}, userWorkspace, nil)

	// No repo/fetcher expectations are set: gomock fails the test if the feed is
	// fetched, proving the SSRF primitive is unreachable without write access.
	resp, err := d.svc.RefreshGlobalFeed(ctx, req)
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.IsType(t, &domain.PermissionError{}, err)
}

func TestBroadcastService_TestRecipientFeed_PermissionDenied(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	d := setupBroadcastSvc(t)
	defer d.ctrl.Finish()

	ctx := context.Background()
	req := &domain.TestRecipientFeedRequest{WorkspaceID: "w1", BroadcastID: "b1", URL: "https://example.com/feed", Headers: []domain.DataFeedHeader{}}

	// Read-only member: a member of the workspace without broadcasts:write.
	userWorkspace := &domain.UserWorkspace{
		UserID:      "user1",
		WorkspaceID: req.WorkspaceID,
		Role:        "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceBroadcasts: {Read: true, Write: false},
		},
	}
	d.authService.EXPECT().AuthenticateUserForWorkspace(ctx, req.WorkspaceID).Return(ctx, &domain.User{ID: "user1"}, userWorkspace, nil)

	// No repo/fetcher expectations are set: gomock fails the test if the feed is
	// fetched, proving the SSRF primitive is unreachable without write access.
	resp, err := d.svc.TestRecipientFeed(ctx, req)
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.IsType(t, &domain.PermissionError{}, err)
}

// TestBroadcastService_PermissionEnforcement verifies that every broadcast
// operation enforces the correct broadcasts permission. Each method is exercised
// by a workspace member who has been granted the OPPOSITE permission (e.g. a write
// operation is tested with a read-only member), so the test fails both if a check
// is missing AND if a method is gated on the wrong permission type (read/write
// swap). No repo/fetcher expectations are set, so gomock also fails if anything
// beyond the permission gate runs.
func TestBroadcastService_PermissionEnforcement(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	// role "member" (not "owner") so HasPermission actually consults the grants.
	member := func(read, write bool) *domain.UserWorkspace {
		return &domain.UserWorkspace{
			UserID:      "user1",
			WorkspaceID: "w1",
			Role:        "member",
			Permissions: domain.UserPermissions{
				domain.PermissionResourceBroadcasts: {Read: read, Write: write},
			},
		}
	}

	cases := []struct {
		name string
		perm domain.PermissionType
		call func(d *broadcastSvcDeps, ctx context.Context) error
	}{
		{"UpdateBroadcast", domain.PermissionTypeWrite, func(d *broadcastSvcDeps, ctx context.Context) error {
			_, err := d.svc.UpdateBroadcast(ctx, &domain.UpdateBroadcastRequest{WorkspaceID: "w1", ID: "b1"})
			return err
		}},
		{"ListBroadcasts", domain.PermissionTypeRead, func(d *broadcastSvcDeps, ctx context.Context) error {
			_, err := d.svc.ListBroadcasts(ctx, domain.ListBroadcastsParams{WorkspaceID: "w1"})
			return err
		}},
		{"ScheduleBroadcast", domain.PermissionTypeWrite, func(d *broadcastSvcDeps, ctx context.Context) error {
			return d.svc.ScheduleBroadcast(ctx, &domain.ScheduleBroadcastRequest{WorkspaceID: "w1", ID: "b1"})
		}},
		{"PauseBroadcast", domain.PermissionTypeWrite, func(d *broadcastSvcDeps, ctx context.Context) error {
			return d.svc.PauseBroadcast(ctx, &domain.PauseBroadcastRequest{WorkspaceID: "w1", ID: "b1"})
		}},
		{"ResumeBroadcast", domain.PermissionTypeWrite, func(d *broadcastSvcDeps, ctx context.Context) error {
			return d.svc.ResumeBroadcast(ctx, &domain.ResumeBroadcastRequest{WorkspaceID: "w1", ID: "b1"})
		}},
		{"CancelBroadcast", domain.PermissionTypeWrite, func(d *broadcastSvcDeps, ctx context.Context) error {
			return d.svc.CancelBroadcast(ctx, &domain.CancelBroadcastRequest{WorkspaceID: "w1", ID: "b1"})
		}},
		{"DeleteBroadcast", domain.PermissionTypeWrite, func(d *broadcastSvcDeps, ctx context.Context) error {
			return d.svc.DeleteBroadcast(ctx, &domain.DeleteBroadcastRequest{WorkspaceID: "w1", ID: "b1"})
		}},
		{"SendToIndividual", domain.PermissionTypeWrite, func(d *broadcastSvcDeps, ctx context.Context) error {
			return d.svc.SendToIndividual(ctx, &domain.SendToIndividualRequest{WorkspaceID: "w1", BroadcastID: "b1"})
		}},
		{"GetTestResults", domain.PermissionTypeRead, func(d *broadcastSvcDeps, ctx context.Context) error {
			_, err := d.svc.GetTestResults(ctx, "w1", "b1")
			return err
		}},
		{"SelectWinner", domain.PermissionTypeWrite, func(d *broadcastSvcDeps, ctx context.Context) error {
			return d.svc.SelectWinner(ctx, "w1", "b1", "t1")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := setupBroadcastSvc(t)
			defer d.ctrl.Finish()

			// Grant only the OPPOSITE permission so the test proves the exact
			// permission type is required (catches a read/write swap).
			grant := member(true, false) // has read, lacks write
			if tc.perm == domain.PermissionTypeRead {
				grant = member(false, true) // has write, lacks read
			}

			ctx := context.Background()
			d.authService.EXPECT().
				AuthenticateUserForWorkspace(ctx, "w1").
				Return(ctx, &domain.User{ID: "user1"}, grant, nil)

			err := tc.call(d, ctx)
			require.Error(t, err)
			assert.IsType(t, &domain.PermissionError{}, err)
		})
	}
}
