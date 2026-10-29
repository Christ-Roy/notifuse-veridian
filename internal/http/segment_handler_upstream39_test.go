// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/http/segment_handler_test.go ; ne contient que les declarations absentes du fork.

package http

import (
	"net/http"
	"net/http/httptest"
	"testing"



	"github.com/golang/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSegmentHandler_GetContactsLegacyShapeUnchanged pins the response the console
// reads today, byte for byte. The expanded shape is opt-in precisely so this body
// cannot move: a caller that sends no expand parameter must not start receiving
// contact objects, extra keys, or a different key order.
func TestSegmentHandler_GetContactsLegacyShapeUnchanged(t *testing.T) {
	mockService, _, handler := setupSegmentHandlerTest(t)

	mockService.EXPECT().
		GetSegmentContacts(gomock.Any(), "workspace123", "segment1", 50, 0).
		Return([]string{"user1@example.com", "user2@example.com"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/segments.contacts?workspace_id=workspace123&segment_id=segment1", nil)
	rr := httptest.NewRecorder()

	handler.handleGetContacts(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Equal(t, `{"emails":["user1@example.com","user2@example.com"],"limit":50,"offset":0}`+"\n", rr.Body.String())
}
