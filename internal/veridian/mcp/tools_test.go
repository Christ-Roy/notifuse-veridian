package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

func member(role string, perms domain.UserPermissions) *domain.UserWorkspace {
	return &domain.UserWorkspace{UserID: "u1", WorkspaceID: "ws1", Role: role, Permissions: perms}
}

// Regle de Robert : aucun outil n'envoie un mail sans destructiveHint.
func TestCatalog_TousLesEnvoisEtSuppressionsSontDestructifs(t *testing.T) {
	sends := 0
	for _, tl := range catalog {
		low := strings.ToLower(tl.Path)
		if strings.Contains(low, "send") || strings.Contains(low, "schedule") || strings.Contains(low, ".delete") {
			sends++
			assert.True(t, tl.Destructive, "%s doit porter destructiveHint", tl.Name)
		}
		if tl.Destructive {
			assert.True(t, tl.Write, "%s destructif implique ecriture", tl.Name)
		}
		assert.NotEmpty(t, tl.Description)
		assert.False(t, strings.Contains(tl.Description, "\n"), "description d'une ligne : %s", tl.Name)
		desc := tl.descriptor()
		ann := desc["annotations"].(map[string]any)
		assert.Equal(t, !tl.Write, ann["readOnlyHint"])
		assert.Equal(t, tl.Destructive, ann["destructiveHint"])
	}
	assert.GreaterOrEqual(t, sends, 2, "le garde-fou voit bien l'envoi et la suppression")
}

func TestCatalog_NomsUniquesEtOutilsTrouvables(t *testing.T) {
	seen := map[string]bool{}
	for _, tl := range catalog {
		assert.False(t, seen[tl.Name], "doublon %s", tl.Name)
		seen[tl.Name] = true
		got, ok := findTool(tl.Name)
		require.True(t, ok)
		assert.Equal(t, tl.Path, got.Path)
	}
	_, ok := findTool("nexiste_pas")
	assert.False(t, ok)
}

func TestTool_DroitsFiltrentLaListe(t *testing.T) {
	send, _ := findTool("transactional_send")
	list, _ := findTool("contacts_list")

	assert.True(t, send.allowed(member("owner", nil)), "le proprietaire a tout")
	assert.False(t, send.allowed(member("member", nil)))

	readOnly := member("member", domain.UserPermissions{domain.PermissionResourceTransactional: {Read: true}, domain.PermissionResourceContacts: {Read: true}})
	assert.False(t, send.allowed(readOnly), "lecture seule : pas d'envoi")
	assert.True(t, list.allowed(readOnly))

	writer := member("member", domain.UserPermissions{domain.PermissionResourceTransactional: {Read: true, Write: true}})
	assert.True(t, send.allowed(writer))
	assert.False(t, list.allowed(writer), "pas de droit contacts")
}

func TestTool_RequestPoseL_espaceEtIgnoreCeluiDeLIA(t *testing.T) {
	get, _ := findTool("contacts_get_by_email")
	q, body, err := get.request(map[string]any{"email": "a@b.test", "workspace_id": "AUTRE-ESPACE", "x": 1}, "ws1")
	require.NoError(t, err)
	assert.Nil(t, body)
	assert.Equal(t, "ws1", q.Get("workspace_id"), "l'espace vient de l'utilisateur")
	assert.Equal(t, "a@b.test", q.Get("email"))
	assert.Empty(t, q.Get("x"), "argument hors schema ignore")

	up, _ := findTool("contacts_upsert")
	_, body, err = up.request(map[string]any{"contact": map[string]any{"email": "a@b.test"}, "workspace_id": "AUTRE-ESPACE"}, "ws1")
	require.NoError(t, err)
	var sent map[string]any
	require.NoError(t, json.Unmarshal(body, &sent))
	assert.Equal(t, "ws1", sent["workspace_id"])
	assert.Equal(t, http.MethodPost, up.Method)
}

func TestTool_RequestExigeLesArgumentsRequis(t *testing.T) {
	get, _ := findTool("templates_get")
	_, _, err := get.request(map[string]any{}, "ws1")
	assert.ErrorContains(t, err, "id")
	_, _, err = get.request(map[string]any{"id": ""}, "ws1")
	assert.Error(t, err)
}

func TestTool_PaginationBornee(t *testing.T) {
	list, _ := findTool("contacts_list")
	q, _, _ := list.request(map[string]any{}, "ws1")
	assert.Equal(t, "25", q.Get("limit"))
	q, _, _ = list.request(map[string]any{"limit": float64(100000)}, "ws1")
	assert.Equal(t, "100", q.Get("limit"))
	q, _, _ = list.request(map[string]any{"limit": float64(-3)}, "ws1")
	assert.Equal(t, "25", q.Get("limit"))
	assert.Equal(t, 40, clampLimit(40))
}
