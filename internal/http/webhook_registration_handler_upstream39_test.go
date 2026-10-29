// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/http/webhook_registration_handler_test.go ; ne contient que les declarations absentes du fork.

package http

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

// Registering with the ESP is owner-only, so a member's denial is the caller's
// answer — 403, not the 500 the generic branch below it would produce.
func TestWebhookRegistrationHandler_NonOwnerIsForbidden(t *testing.T) {
	mockService, _, handler, _, _, _, cleanup := setupWebhookRegistrationHandlerTest(t)
	defer cleanup()

	denial := &domain.ErrUnauthorized{Message: "user is not an owner of the workspace"}

	t.Run("register", func(t *testing.T) {
		mockService.EXPECT().
			RegisterWebhooks(gomock.Any(), "workspace-123", gomock.Any()).
			Return(nil, denial)

		body := `{"workspace_id":"workspace-123","integration_id":"integration-456","event_types":["delivered"]}`
		req := httptest.NewRequest(http.MethodPost, "/api/webhooks.register", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		handler.handleRegister(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)

		var response map[string]string
		_ = json.NewDecoder(rr.Body).Decode(&response)
		assert.Equal(t, denial.Message, response["error"])
	})

	t.Run("status", func(t *testing.T) {
		mockService.EXPECT().
			GetWebhookStatus(gomock.Any(), "workspace-123", "integration-456").
			Return(nil, denial)

		req := httptest.NewRequest(http.MethodGet,
			"/api/webhooks.status?workspace_id=workspace-123&integration_id=integration-456", nil)
		rr := httptest.NewRecorder()

		handler.handleStatus(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)

		var response map[string]string
		_ = json.NewDecoder(rr.Body).Decode(&response)
		assert.Equal(t, denial.Message, response["error"])
	})
}
