package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianWorkspaceRole_Predicats(t *testing.T) {
	for _, c := range []struct {
		name                  string
		uw                    *UserWorkspace
		administers, owns bool
	}{
		{"owner", &UserWorkspace{Role: VeridianRoleOwner}, true, true},
		{"admin", &UserWorkspace{Role: VeridianRoleAdmin}, true, false},
		{"member", &UserWorkspace{Role: VeridianRoleMember}, false, false},
		{"member a tous droits", &UserWorkspace{Role: VeridianRoleMember, Permissions: FullPermissions}, false, false},
		{"rôle vide", &UserWorkspace{}, false, false},
		{"rôle inconnu", &UserWorkspace{Role: "superadmin"}, false, false},
		{"casse différente", &UserWorkspace{Role: "Admin"}, false, false},
		{"nil", nil, false, false},
	} {
		assert.Equal(t, c.administers, c.uw.VeridianAdministers(), c.name+" administers")
		assert.Equal(t, c.owns, c.uw.VeridianOwns(), c.name+" owns")
	}
}

func TestVeridianWorkspaceRole_Constantes(t *testing.T) {
	assert.Equal(t, "owner", VeridianRoleOwner)
	assert.Equal(t, "admin", VeridianRoleAdmin)
	assert.Equal(t, "member", VeridianRoleMember)
}

func TestUserWorkspace_HasPermission_AdminCommeProprietaire(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		uw := &UserWorkspace{Role: role} // aucune permission déclarée
		for _, res := range []PermissionResource{PermissionResourceContacts, PermissionResourceLists, PermissionResourceTemplates, PermissionResourceBroadcasts} {
			assert.True(t, uw.HasPermission(res, PermissionTypeRead), role+" read "+string(res))
			assert.True(t, uw.HasPermission(res, PermissionTypeWrite), role+" write "+string(res))
		}
	}
	member := &UserWorkspace{Role: "member"}
	assert.False(t, member.HasPermission(PermissionResourceContacts, PermissionTypeRead), "un membre sans permission n'a rien")
	partial := &UserWorkspace{Role: "member", Permissions: UserPermissions{PermissionResourceContacts: {Read: true}}}
	assert.True(t, partial.HasPermission(PermissionResourceContacts, PermissionTypeRead))
	assert.False(t, partial.HasPermission(PermissionResourceContacts, PermissionTypeWrite))
	assert.False(t, partial.HasPermission(PermissionResourceLists, PermissionTypeRead))
	unknown := &UserWorkspace{Role: "superadmin"}
	assert.False(t, unknown.HasPermission(PermissionResourceContacts, PermissionTypeRead), "un rôle inconnu ne gouverne rien")
}

// Validate ne laisse jamais l'application créer un admin : seule la route signée du Hub le pose.
func TestUserWorkspace_Validate_RefuseToujoursAdmin(t *testing.T) {
	admin := &UserWorkspace{UserID: "u", WorkspaceID: "w", Role: "admin"}
	err := admin.Validate()
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "'owner' or 'member'")
	}
	assert.NoError(t, (&UserWorkspace{UserID: "u", WorkspaceID: "w", Role: "owner"}).Validate())
	assert.NoError(t, (&UserWorkspace{UserID: "u", WorkspaceID: "w", Role: "member"}).Validate())
	assert.Error(t, (&UserWorkspace{UserID: "u", WorkspaceID: "w", Role: "Admin"}).Validate())
}
