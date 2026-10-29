package api

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/folders"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func member(perms map[domain.PermissionResource]domain.ResourcePermissions) *domain.UserWorkspace {
	return &domain.UserWorkspace{Role: "member", Permissions: perms}
}

func TestAuthorize_UnDroitParGenre(t *testing.T) {
	readOnlyContacts := member(domain.UserPermissions{domain.PermissionResourceContacts: {Read: true}})
	listsWriter := member(domain.UserPermissions{domain.PermissionResourceLists: {Read: true, Write: true}})
	owner := &domain.UserWorkspace{Role: "owner"}

	assert.NoError(t, Authorize(readOnlyContacts, folders.KindSegments, false))
	assert.Error(t, Authorize(readOnlyContacts, folders.KindSegments, true), "lecture seule : pas d'ecriture")
	assert.Error(t, Authorize(readOnlyContacts, folders.KindLists, false), "le droit des contacts n'ouvre pas les listes")
	assert.NoError(t, Authorize(listsWriter, folders.KindLists, true))
	assert.Error(t, Authorize(listsWriter, folders.KindTemplates, false))
	assert.Error(t, Authorize(listsWriter, folders.KindSegments, false))
	assert.NoError(t, Authorize(owner, folders.KindTemplates, true))
	assert.NoError(t, Authorize(readOnlyContacts, "", false), "genre vide : appartenance seule")
	assert.Error(t, Authorize(nil, folders.KindLists, false))
	assert.Error(t, Authorize(owner, folders.Kind("zzz"), false))

	err := Authorize(readOnlyContacts, folders.KindSegments, true)
	var pe *domain.PermissionError
	require.True(t, errors.As(err, &pe), "erreur de permission typee : le handler repond 403")
}

func TestCapabiliteEnregistree(t *testing.T) {
	found := false
	for _, c := range platform.Default.List() {
		if c.Name == "folders" {
			found = true
			assert.NotNil(t, c.Wire)
		}
	}
	assert.True(t, found, "la capacite folders s'enregistre dans platform.Default")
}

// Assemblage de test : un Host sans AuthService ni dépôt ne fait pas échouer le démarrage.
type bareHost struct{ platform.Host }

func (bareHost) AuthService() *service.AuthService              { return nil }
func (bareHost) WorkspaceRepository() domain.WorkspaceRepository { return nil }

func TestWire_SansServicesNeCableRien(t *testing.T) {
	assert.NoError(t, wire(bareHost{}))
}
