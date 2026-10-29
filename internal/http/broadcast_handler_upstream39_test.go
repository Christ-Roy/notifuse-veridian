// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/http/broadcast_handler_test.go ; ne contient que les declarations absentes du fork.

package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"


	"github.com/golang/mock/gomock"

	"github.com/stretchr/testify/assert"
)

// TestBroadcastHandlers_PermissionDenied verifies that broadcast handlers translate
// a service-layer domain.PermissionError into HTTP 403. The mapping is identical
// boilerplate across every broadcast handler; these cases cover the distinct request
// shapes (GET read, POST write, POST send). Per-method authorization itself is proven
// exhaustively in TestBroadcastService_PermissionEnforcement.
func TestBroadcastHandlers_PermissionDenied(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	permErr := domain.NewPermissionError(
		domain.PermissionResourceBroadcasts,
		domain.PermissionTypeWrite,
		"Insufficient permissions: write access to broadcasts required",
	)

	t.Run("List (GET, read)", func(t *testing.T) {
		handler, mockService, _, _, ctrl := setupBroadcastHandler(t)
		defer ctrl.Finish()

		mockService.EXPECT().ListBroadcasts(gomock.Any(), gomock.Any()).Return(nil, permErr)

		req := httptest.NewRequest(http.MethodGet, "/api/broadcasts.list?workspace_id=workspace123", nil)
		w := httptest.NewRecorder()
		handler.HandleList(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("Delete (POST, write)", func(t *testing.T) {
		handler, mockService, _, _, ctrl := setupBroadcastHandler(t)
		defer ctrl.Finish()

		mockService.EXPECT().DeleteBroadcast(gomock.Any(), gomock.Any()).Return(permErr)

		body, _ := json.Marshal(&domain.DeleteBroadcastRequest{WorkspaceID: "workspace123", ID: "broadcast123"})
		req := httptest.NewRequest(http.MethodPost, "/api/broadcasts.delete", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.HandleDelete(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("SendToIndividual (POST, send)", func(t *testing.T) {
		handler, mockService, _, _, ctrl := setupBroadcastHandler(t)
		defer ctrl.Finish()

		mockService.EXPECT().SendToIndividual(gomock.Any(), gomock.Any()).Return(permErr)

		body, _ := json.Marshal(&domain.SendToIndividualRequest{
			WorkspaceID:    "workspace123",
			BroadcastID:    "broadcast123",
			RecipientEmail: "user@example.com",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/broadcasts.sendToIndividual", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.HandleSendToIndividual(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}
