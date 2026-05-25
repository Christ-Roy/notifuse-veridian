package http

// === Veridian patch ===
// veridian_user_me_response_test.go — Anti-régression sur la shape JSON
// de GET /api/user.me concernant le champ `hub_user_id` (CONTRAT-HUB §3.7).
//
// Pourquoi un test dédié alors que TestUserHandler_GetCurrentUser existe ?
// Le test upstream ne fait aucune assertion sur `hub_user_id` (champ
// Veridian-only ajouté en V46). Une future sync upstream qui introduit
// un DTO de sérialisation pour GetCurrentUser pourrait silencieusement
// supprimer le champ du JSON. Ce test fail dès qu'on perd le tag JSON
// `hub_user_id,omitempty` ou qu'on bypass la sérialisation directe du
// *domain.User.
//
// Garanties couvertes :
//   1. user avec hub_user_id présent → JSON contient "hub_user_id": "<uuid>"
//   2. user avec hub_user_id NULL (legacy pré-V46) → champ absent du JSON
//      (omitempty actif)
//   3. user avec hub_user_id non-UUID stocké (legacy / corruption) → la
//      sérialisation ne crash pas, la valeur est exposée telle quelle
//      (validation UUID est faite à l'écriture par CreateUser, pas en lecture)

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

func TestVeridian_UserMe_ResponseShape_HubUserIDPresent(t *testing.T) {
	handler, mockUserSvc, mockWorkspaceSvc, _ := setupUserHandlerTest(t)

	userID := "notifuse-user-1"
	hubUUID := "550e8400-e29b-41d4-a716-446655440000"
	user := &domain.User{
		ID:        userID,
		Email:     "user@example.com",
		Name:      "Veridian User",
		HubUserID: &hubUUID,
	}

	mockUserSvc.EXPECT().
		GetUserByID(gomock.Any(), userID).
		Return(user, nil)
	mockWorkspaceSvc.EXPECT().
		ListWorkspaces(gomock.Any()).
		Return([]*domain.Workspace{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/user.me", nil)
	req = req.WithContext(context.WithValue(req.Context(), domain.UserIDKey, userID))
	rec := httptest.NewRecorder()

	handler.GetCurrentUser(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	userData, ok := response["user"].(map[string]interface{})
	require.True(t, ok, "response.user must be an object")

	rawHubID, present := userData["hub_user_id"]
	require.True(t, present, "hub_user_id must be present in /api/user.me response when set on the user")

	hubIDStr, ok := rawHubID.(string)
	require.True(t, ok, "hub_user_id must serialize as string, got %T", rawHubID)
	assert.Equal(t, hubUUID, hubIDStr, "hub_user_id must round-trip the stored uuid")
}

func TestVeridian_UserMe_ResponseShape_HubUserIDNullOmitted(t *testing.T) {
	handler, mockUserSvc, mockWorkspaceSvc, _ := setupUserHandlerTest(t)

	userID := "legacy-user-pre-v46"
	user := &domain.User{
		ID:        userID,
		Email:     "legacy@example.com",
		Name:      "Legacy User",
		HubUserID: nil,
	}

	mockUserSvc.EXPECT().
		GetUserByID(gomock.Any(), userID).
		Return(user, nil)
	mockWorkspaceSvc.EXPECT().
		ListWorkspaces(gomock.Any()).
		Return([]*domain.Workspace{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/user.me", nil)
	req = req.WithContext(context.WithValue(req.Context(), domain.UserIDKey, userID))
	rec := httptest.NewRecorder()

	handler.GetCurrentUser(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	userData, ok := response["user"].(map[string]interface{})
	require.True(t, ok)

	_, present := userData["hub_user_id"]
	assert.False(t, present, "hub_user_id must be omitted from JSON when nil (omitempty), got %v", userData["hub_user_id"])

	assert.Equal(t, "legacy@example.com", userData["email"])
	assert.Equal(t, userID, userData["id"])
}

func TestVeridian_UserMe_ResponseShape_HubUserIDLegacyNonUUID(t *testing.T) {
	handler, mockUserSvc, mockWorkspaceSvc, _ := setupUserHandlerTest(t)

	userID := "legacy-corrupted"
	legacyValue := "not-a-uuid-but-still-stored"
	user := &domain.User{
		ID:        userID,
		Email:     "corrupted@example.com",
		HubUserID: &legacyValue,
	}

	mockUserSvc.EXPECT().
		GetUserByID(gomock.Any(), userID).
		Return(user, nil)
	mockWorkspaceSvc.EXPECT().
		ListWorkspaces(gomock.Any()).
		Return([]*domain.Workspace{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/user.me", nil)
	req = req.WithContext(context.WithValue(req.Context(), domain.UserIDKey, userID))
	rec := httptest.NewRecorder()

	handler.GetCurrentUser(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "non-UUID hub_user_id stored in DB must not crash serialization")

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	userData := response["user"].(map[string]interface{})
	assert.Equal(t, legacyValue, userData["hub_user_id"], "non-UUID legacy values must be exposed verbatim; read path does not validate")
}
