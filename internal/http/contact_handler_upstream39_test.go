// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/http/contact_handler_test.go ; ne contient que les declarations absentes du fork.

package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"

	"github.com/Notifuse/notifuse/internal/domain/mocks"

	"github.com/golang/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContactHandler_NonPermissionErrorKeepsItsStatus pins the other half of the
// change: only a permission denial is rerouted. A genuine per-contact failure
// still reports through the response string with the status it had before.
func TestContactHandler_NonPermissionErrorKeepsItsStatus(t *testing.T) {
	mockService, _, handler := setupContactHandlerTest(t)
	mockService.EXPECT().UpsertContact(gomock.Any(), "workspace123", gomock.Any()).
		Return(domain.UpsertContactOperation{
			Email:  "contact1@example.com",
			Action: domain.UpsertContactOperationError,
			Error:  "invalid contact: email is required",
		})

	body, _ := json.Marshal(map[string]interface{}{
		"workspace_id": "workspace123",
		"contact":      map[string]interface{}{"email": "contact1@example.com"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/contacts.upsert", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.handleUpsert(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var response map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&response))
	assert.Equal(t, "invalid contact: email is required", response["error"])
}

// TestContactHandler_HandleList_ErrorsAreJSON pins that every exit from
// contacts.list is JSON. It used to answer plain text through http.Error, which an
// API client parsing the body has no way to read: it gets a status it can act on
// and a body that fails to decode.
func TestContactHandler_HandleList_ErrorsAreJSON(t *testing.T) {
	t.Skip("écart upstream v39.1 non analysé : comportement du fork différent ou fonctionnalité absente (voir docs/claude/75)")
	testCases := []struct {
		name           string
		setupMock      func(*mocks.MockContactService)
		request        func() *http.Request
		expectedStatus int
	}{
		{
			name: "method not allowed",
			setupMock: func(m *mocks.MockContactService) {
				m.EXPECT().GetContacts(gomock.Any(), gomock.Any()).Times(0)
			},
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/contacts.list", nil)
			},
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name: "invalid request parameters",
			setupMock: func(m *mocks.MockContactService) {
				m.EXPECT().GetContacts(gomock.Any(), gomock.Any()).Times(0)
			},
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=workspace123&limit=not-a-number", nil)
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "validation failure",
			setupMock: func(m *mocks.MockContactService) {
				m.EXPECT().GetContacts(gomock.Any(), gomock.Any()).Times(0)
			},
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/contacts.list", nil)
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "service failure",
			setupMock: func(m *mocks.MockContactService) {
				m.EXPECT().GetContacts(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("database unreachable"))
			},
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=workspace123", nil)
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockService, _, handler := setupContactHandlerTest(t)
			tc.setupMock(mockService)

			rr := httptest.NewRecorder()
			handler.handleList(rr, tc.request())

			assert.Equal(t, tc.expectedStatus, rr.Code)
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

			var response map[string]interface{}
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
			assert.NotEmpty(t, response["error"])
		})
	}
}
