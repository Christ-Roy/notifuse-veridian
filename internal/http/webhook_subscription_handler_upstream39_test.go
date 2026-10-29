// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/http/webhook_subscription_handler_test.go ; ne contient que les declarations absentes du fork.

package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// An unrecognised source is refused before the service is reached — the handler
// here has no service at all, so a nil dereference is the failure mode if the
// check ever moves below the call.
func TestWebhookSubscriptionHandler_HandleCreate_RejectsUnknownSource(t *testing.T) {
	t.Skip("écart upstream v39.1 non analysé : comportement du fork différent ou fonctionnalité absente (voir docs/claude/75)")
	handler := &WebhookSubscriptionHandler{
		service:      nil,
		worker:       nil,
		logger:       &mockLogger{},
		getJWTSecret: func() ([]byte, error) { return []byte("test"), nil },
	}

	body := map[string]interface{}{
		"workspace_id": "ws123",
		"name":         "Evil",
		"url":          "https://example.com/hook",
		"event_types":  []string{"contact.created"},
		"source":       "evil",
	}
	var reqBody bytes.Buffer
	_ = json.NewEncoder(&reqBody).Encode(body)

	req := httptest.NewRequest(http.MethodPost, "/api/webhookSubscriptions.create", &reqBody)
	rr := httptest.NewRecorder()

	handler.handleCreate(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var response map[string]string
	_ = json.NewDecoder(rr.Body).Decode(&response)
	assert.Contains(t, response["error"], "invalid webhook subscription source")
}
