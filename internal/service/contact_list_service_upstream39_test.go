// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/service/contact_list_service_test.go ; ne contient que les declarations absentes du fork.

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/require"
)

// TestContactListService_PermissionEnforcement verifies that every contact-list
// operation enforces the permissions it needs. Each case grants everything the
// method requires except one, so the test fails both if a check is missing AND if
// a method is gated on the wrong permission type (a read/write swap). No repo
// expectations are set, so gomock also fails if anything beyond the gate runs.
//
// Each case also pins WHICH resource and verb the refusal names. The fixture
// holds two resources, so a gate on the wrong one still denies and a
// type-only assertion would pass; and both fields reach the client, since
// writePermissionError puts them on the wire.
//
// The two enumerating reads and both writes require a contacts grant on top of
// the lists one: they read or edit subscriber identities, not just list metadata.
func TestContactListService_PermissionEnforcement(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	// role "member" (not "owner") so HasPermission actually consults the grants.
	member := func(lists, contacts domain.ResourcePermissions) *domain.UserWorkspace {
		return &domain.UserWorkspace{
			UserID:      "user1",
			WorkspaceID: "workspace123",
			Role:        "member",
			Permissions: domain.UserPermissions{
				domain.PermissionResourceLists:    lists,
				domain.PermissionResourceContacts: contacts,
			},
		}
	}
	readOnly := domain.ResourcePermissions{Read: true}
	writeOnly := domain.ResourcePermissions{Write: true}
	full := domain.ResourcePermissions{Read: true, Write: true}

	ctx := context.Background()
	workspaceID := "workspace123"
	email := "test@example.com"
	listID := "list123"

	cases := []struct {
		name          string
		userWorkspace *domain.UserWorkspace
		resource      domain.PermissionResource
		permission    domain.PermissionType
		call          func(s *ContactListService, ctx context.Context) error
	}{
		{"GetContactListByIDs without lists read", member(writeOnly, full),
			domain.PermissionResourceLists, domain.PermissionTypeRead,
			func(s *ContactListService, ctx context.Context) error {
				_, err := s.GetContactListByIDs(ctx, workspaceID, email, listID)
				return err
			}},
		{"GetContactsByListID without lists read", member(writeOnly, full),
			domain.PermissionResourceLists, domain.PermissionTypeRead,
			func(s *ContactListService, ctx context.Context) error {
				_, err := s.GetContactsByListID(ctx, workspaceID, listID)
				return err
			}},
		{"GetContactsByListID with lists read but without contacts read", member(readOnly, writeOnly),
			domain.PermissionResourceContacts, domain.PermissionTypeRead,
			func(s *ContactListService, ctx context.Context) error {
				_, err := s.GetContactsByListID(ctx, workspaceID, listID)
				return err
			}},
		{"GetListsByEmail without lists read", member(writeOnly, full),
			domain.PermissionResourceLists, domain.PermissionTypeRead,
			func(s *ContactListService, ctx context.Context) error {
				_, err := s.GetListsByEmail(ctx, workspaceID, email)
				return err
			}},
		{"GetListsByEmail with lists read but without contacts read", member(readOnly, writeOnly),
			domain.PermissionResourceContacts, domain.PermissionTypeRead,
			func(s *ContactListService, ctx context.Context) error {
				_, err := s.GetListsByEmail(ctx, workspaceID, email)
				return err
			}},
		{"UpdateContactListStatus without lists write", member(readOnly, full),
			domain.PermissionResourceLists, domain.PermissionTypeWrite,
			func(s *ContactListService, ctx context.Context) error {
				_, err := s.UpdateContactListStatus(ctx, workspaceID, email, listID, domain.ContactListStatusActive)
				return err
			}},
		{"UpdateContactListStatus with lists write but without contacts write", member(writeOnly, readOnly),
			domain.PermissionResourceContacts, domain.PermissionTypeWrite,
			func(s *ContactListService, ctx context.Context) error {
				_, err := s.UpdateContactListStatus(ctx, workspaceID, email, listID, domain.ContactListStatusActive)
				return err
			}},
		{"RemoveContactFromList without lists write", member(readOnly, full),
			domain.PermissionResourceLists, domain.PermissionTypeWrite,
			func(s *ContactListService, ctx context.Context) error {
				return s.RemoveContactFromList(ctx, workspaceID, email, listID)
			}},
		{"RemoveContactFromList with lists write but without contacts write", member(writeOnly, readOnly),
			domain.PermissionResourceContacts, domain.PermissionTypeWrite,
			func(s *ContactListService, ctx context.Context) error {
				return s.RemoveContactFromList(ctx, workspaceID, email, listID)
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mockAuthService, _, _, service, ctrl := setupTest(t)
			defer ctrl.Finish()

			mockAuthService.EXPECT().
				AuthenticateUserForWorkspace(ctx, workspaceID).
				Return(ctx, &domain.User{ID: "user1"}, tc.userWorkspace, nil)

			err := tc.call(service, ctx)
			require.Error(t, err)

			var permErr *domain.PermissionError
			require.True(t, errors.As(err, &permErr), "expected a *domain.PermissionError, got %T: %v", err, err)
			require.Equal(t, tc.resource, permErr.Resource)
			require.Equal(t, tc.permission, permErr.Permission)
		})
	}
}
