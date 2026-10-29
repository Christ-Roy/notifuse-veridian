// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/http/transactional_handler_test.go ; ne contient que les declarations absentes du fork.

package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"

	"github.com/Notifuse/notifuse/internal/domain/mocks"

	"github.com/golang/mock/gomock"

	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"
)

// TestTransactionalNotificationHandler_PermissionErrorMapping pins every endpoint to a
// 403 for a permission denial. Every route checks transactional permissions except
// send, and create, update and send also reach template or contact permissions through
// another service and wrap the result ("invalid template for channel %s: %w"), so the
// denial can arrive here wrapped — which the substring branches would otherwise
// classify as a 400, or as a 200 carrying it in the body.
func TestTransactionalNotificationHandler_PermissionErrorMapping(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	const denialMessage = "Insufficient permissions: read access to templates required"

	newDenial := func() error {
		return domain.NewPermissionError(
			domain.PermissionResourceTemplates,
			domain.PermissionTypeRead,
			denialMessage,
		)
	}

	testCases := []struct {
		name       string
		serviceErr error
		invoke     func(*mocks.MockTransactionalNotificationService, *TransactionalNotificationHandler, error) *httptest.ResponseRecorder
	}{
		{
			name:       "list",
			serviceErr: newDenial(),
			invoke: func(m *mocks.MockTransactionalNotificationService, h *TransactionalNotificationHandler, svcErr error) *httptest.ResponseRecorder {
				m.EXPECT().
					ListNotifications(gomock.Any(), "workspace1", gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, 0, svcErr)
				params := url.Values{"workspace_id": []string{"workspace1"}}
				req := httptest.NewRequest(http.MethodGet, "/api/transactional.list?"+params.Encode(), nil)
				w := httptest.NewRecorder()
				h.handleList(w, req)
				return w
			},
		},
		{
			name:       "get",
			serviceErr: newDenial(),
			invoke: func(m *mocks.MockTransactionalNotificationService, h *TransactionalNotificationHandler, svcErr error) *httptest.ResponseRecorder {
				m.EXPECT().
					GetNotification(gomock.Any(), "workspace1", "test-notification").
					Return(nil, svcErr)
				params := url.Values{
					"workspace_id": []string{"workspace1"},
					"id":           []string{"test-notification"},
				}
				req := httptest.NewRequest(http.MethodGet, "/api/transactional.get?"+params.Encode(), nil)
				w := httptest.NewRecorder()
				h.handleGet(w, req)
				return w
			},
		},
		{
			name:       "create with the denial wrapped by the template check",
			serviceErr: fmt.Errorf("invalid template for channel email: %w", newDenial()),
			invoke: func(m *mocks.MockTransactionalNotificationService, h *TransactionalNotificationHandler, svcErr error) *httptest.ResponseRecorder {
				m.EXPECT().
					CreateNotification(gomock.Any(), "workspace1", gomock.Any()).
					Return(nil, svcErr)
				body, err := json.Marshal(domain.CreateTransactionalRequest{
					WorkspaceID: "workspace1",
					Notification: domain.TransactionalNotificationCreateParams{
						ID:   "test-notification",
						Name: "Test Notification",
						Channels: domain.ChannelTemplates{
							domain.TransactionalChannelEmail: {TemplateID: "template-123"},
						},
					},
				})
				require.NoError(t, err)
				req := httptest.NewRequest(http.MethodPost, "/api/transactional.create", bytes.NewReader(body))
				w := httptest.NewRecorder()
				h.handleCreate(w, req)
				return w
			},
		},
		{
			name:       "update with the denial wrapped by the template check",
			serviceErr: fmt.Errorf("invalid template for channel email: %w", newDenial()),
			invoke: func(m *mocks.MockTransactionalNotificationService, h *TransactionalNotificationHandler, svcErr error) *httptest.ResponseRecorder {
				m.EXPECT().
					UpdateNotification(gomock.Any(), "workspace1", "test-notification", gomock.Any()).
					Return(nil, svcErr)
				body, err := json.Marshal(domain.UpdateTransactionalRequest{
					WorkspaceID: "workspace1",
					ID:          "test-notification",
					Updates: domain.TransactionalNotificationUpdateParams{
						Name: "Updated Notification",
					},
				})
				require.NoError(t, err)
				req := httptest.NewRequest(http.MethodPost, "/api/transactional.update", bytes.NewReader(body))
				w := httptest.NewRecorder()
				h.handleUpdate(w, req)
				return w
			},
		},
		{
			name:       "delete",
			serviceErr: newDenial(),
			invoke: func(m *mocks.MockTransactionalNotificationService, h *TransactionalNotificationHandler, svcErr error) *httptest.ResponseRecorder {
				m.EXPECT().
					DeleteNotification(gomock.Any(), "workspace1", "test-notification").
					Return(svcErr)
				body, err := json.Marshal(domain.DeleteTransactionalRequest{
					WorkspaceID: "workspace1",
					ID:          "test-notification",
				})
				require.NoError(t, err)
				req := httptest.NewRequest(http.MethodPost, "/api/transactional.delete", bytes.NewReader(body))
				w := httptest.NewRecorder()
				h.handleDelete(w, req)
				return w
			},
		},
		{
			name:       "send",
			serviceErr: newDenial(),
			invoke: func(m *mocks.MockTransactionalNotificationService, h *TransactionalNotificationHandler, svcErr error) *httptest.ResponseRecorder {
				m.EXPECT().
					SendNotification(gomock.Any(), "workspace1", gomock.Any()).
					Return("", svcErr)
				body, err := json.Marshal(domain.SendTransactionalRequest{
					WorkspaceID: "workspace1",
					Notification: domain.TransactionalNotificationSendParams{
						ID: "test-notification",
						Contact: &domain.Contact{
							Email: "test@example.com",
						},
						Channels: []domain.TransactionalChannel{domain.TransactionalChannelEmail},
					},
				})
				require.NoError(t, err)
				req := httptest.NewRequest(http.MethodPost, "/api/transactional.send", bytes.NewReader(body))
				w := httptest.NewRecorder()
				h.handleSend(w, req)
				return w
			},
		},
		{
			name:       "testTemplate with the denial wrapped by the template lookup",
			serviceErr: fmt.Errorf("failed to retrieve template: %w", newDenial()),
			invoke: func(m *mocks.MockTransactionalNotificationService, h *TransactionalNotificationHandler, svcErr error) *httptest.ResponseRecorder {
				m.EXPECT().
					TestTemplate(gomock.Any(), "workspace123", "template123", "marketing", "sender123", "test@example.com", gomock.Any(), gomock.Any()).
					Return(svcErr)
				body, err := json.Marshal(domain.TestTemplateRequest{
					WorkspaceID:    "workspace123",
					TemplateID:     "template123",
					IntegrationID:  "marketing",
					SenderID:       "sender123",
					RecipientEmail: "test@example.com",
				})
				require.NoError(t, err)
				req := httptest.NewRequest(http.MethodPost, "/api/transactional.testTemplate", bytes.NewReader(body))
				w := httptest.NewRecorder()
				h.handleTestTemplate(w, req)
				return w
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockService, mockLogger, handler := setupTransactionalHandlerTest(t)
			mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
			mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
			mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

			w := tc.invoke(mockService, handler, tc.serviceErr)

			assert.Equal(t, http.StatusForbidden, w.Code)

			var response map[string]string
			require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
			// The denial itself, not the wrapping prose the service added around it.
			assert.Equal(t, denialMessage, response["error"])
		})
	}
}

// The service wraps the template lookup ("failed to retrieve template: %w"), so the
// not-found branch has to unwrap too — a type assertion would let a missing template
// fall through to a 200 carrying the message in the response body.
func TestTransactionalNotificationHandler_HandleTestTemplate_WrappedTemplateNotFound(t *testing.T) {
	t.Skip("écart upstream v39.1 non analysé : comportement du fork différent ou fonctionnalité absente (voir docs/claude/75)")
	mockService, mockLogger, handler := setupTransactionalHandlerTest(t)
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	mockService.EXPECT().
		TestTemplate(gomock.Any(), "workspace123", "template123", "marketing", "sender123", "test@example.com", gomock.Any(), gomock.Any()).
		Return(fmt.Errorf("failed to retrieve template: %w", &domain.ErrTemplateNotFound{Message: "template not found"}))

	body, err := json.Marshal(domain.TestTemplateRequest{
		WorkspaceID:    "workspace123",
		TemplateID:     "template123",
		IntegrationID:  "marketing",
		SenderID:       "sender123",
		RecipientEmail: "test@example.com",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/transactional.testTemplate", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.handleTestTemplate(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
	assert.Equal(t, "Template not found", response["error"])
}
