// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/http/contact_list_handler_test.go ; ne contient que les declarations absentes du fork.

package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"

	"github.com/Notifuse/notifuse/internal/domain/mocks"

	"github.com/golang/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContactListHandler_PermissionDenied covers every contactLists route: a
// permission denial from the service must answer 403 with the missing grant named
// in the body, not the generic 500 each handler falls back to. The service wraps
// its errors on the way up, so the denial is wrapped here too.
func TestContactListHandler_PermissionDenied(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	denial := func(resource domain.PermissionResource, permission domain.PermissionType) error {
		return fmt.Errorf("failed to authenticate user: %w",
			domain.NewPermissionError(resource, permission, "Insufficient permissions"))
	}

	tests := []struct {
		name               string
		setupMock          func(*mocks.MockContactListService)
		serve              func(*ContactListHandler, http.ResponseWriter)
		expectedResource   domain.PermissionResource
		expectedPermission domain.PermissionType
	}{
		{
			name: "GetByIDs",
			setupMock: func(m *mocks.MockContactListService) {
				m.EXPECT().GetContactListByIDs(gomock.Any(), "workspace123", "test@example.com", "list123").
					Return(nil, denial(domain.PermissionResourceLists, domain.PermissionTypeRead))
			},
			serve: func(h *ContactListHandler, w http.ResponseWriter) {
				req := httptest.NewRequest(http.MethodGet, "/api/contactLists.getByIDs?workspace_id=workspace123&email=test@example.com&list_id=list123", nil)
				h.handleGetByIDs(w, req)
			},
			expectedResource:   domain.PermissionResourceLists,
			expectedPermission: domain.PermissionTypeRead,
		},
		{
			name: "GetContactsByList",
			setupMock: func(m *mocks.MockContactListService) {
				m.EXPECT().GetContactsByListID(gomock.Any(), "workspace123", "list123").
					Return(nil, denial(domain.PermissionResourceContacts, domain.PermissionTypeRead))
			},
			serve: func(h *ContactListHandler, w http.ResponseWriter) {
				req := httptest.NewRequest(http.MethodGet, "/api/contactLists.getContactsByList?workspace_id=workspace123&list_id=list123", nil)
				h.handleGetContactsByList(w, req)
			},
			expectedResource:   domain.PermissionResourceContacts,
			expectedPermission: domain.PermissionTypeRead,
		},
		{
			name: "GetListsByContact",
			setupMock: func(m *mocks.MockContactListService) {
				m.EXPECT().GetListsByEmail(gomock.Any(), "workspace123", "test@example.com").
					Return(nil, denial(domain.PermissionResourceContacts, domain.PermissionTypeRead))
			},
			serve: func(h *ContactListHandler, w http.ResponseWriter) {
				req := httptest.NewRequest(http.MethodGet, "/api/contactLists.getListsByContact?workspace_id=workspace123&email=test@example.com", nil)
				h.handleGetListsByContact(w, req)
			},
			expectedResource:   domain.PermissionResourceContacts,
			expectedPermission: domain.PermissionTypeRead,
		},
		{
			name: "UpdateStatus",
			setupMock: func(m *mocks.MockContactListService) {
				m.EXPECT().UpdateContactListStatus(gomock.Any(), "workspace123", "test@example.com", "list123", domain.ContactListStatusUnsubscribed).
					Return(nil, denial(domain.PermissionResourceLists, domain.PermissionTypeWrite))
			},
			serve: func(h *ContactListHandler, w http.ResponseWriter) {
				body, _ := json.Marshal(domain.UpdateContactListStatusRequest{
					WorkspaceID: "workspace123",
					Email:       "test@example.com",
					ListID:      "list123",
					Status:      "unsubscribed",
				})
				req := httptest.NewRequest(http.MethodPost, "/api/contactLists.updateStatus", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				h.handleUpdateStatus(w, req)
			},
			expectedResource:   domain.PermissionResourceLists,
			expectedPermission: domain.PermissionTypeWrite,
		},
		{
			name: "RemoveContact",
			setupMock: func(m *mocks.MockContactListService) {
				m.EXPECT().RemoveContactFromList(gomock.Any(), "workspace123", "test@example.com", "list123").
					Return(denial(domain.PermissionResourceContacts, domain.PermissionTypeWrite))
			},
			serve: func(h *ContactListHandler, w http.ResponseWriter) {
				body, _ := json.Marshal(domain.RemoveContactFromListRequest{
					WorkspaceID: "workspace123",
					Email:       "test@example.com",
					ListID:      "list123",
				})
				req := httptest.NewRequest(http.MethodPost, "/api/contactLists.removeContact", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				h.handleRemoveContact(w, req)
			},
			expectedResource:   domain.PermissionResourceContacts,
			expectedPermission: domain.PermissionTypeWrite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService, _, handler := setupContactListHandlerTest(t)
			tt.setupMock(mockService)

			rr := httptest.NewRecorder()
			tt.serve(handler, rr)

			assert.Equal(t, http.StatusForbidden, rr.Code)

			var response map[string]interface{}
			require.NoError(t, json.NewDecoder(rr.Body).Decode(&response))
			assert.Equal(t, string(tt.expectedResource), response["resource"])
			assert.Equal(t, string(tt.expectedPermission), response["permission"])
		})
	}
}
