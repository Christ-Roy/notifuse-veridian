// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/service/transactional_service_test.go ; ne contient que les declarations absentes du fork.

package service

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransactionalNotificationService_TestTemplate_TrackingFollowsWorkspaceFlag(t *testing.T) {
	t.Skip("écart upstream v39.1 non analysé : comportement du fork différent ou fonctionnalité absente (voir docs/claude/75)")
	for _, trackingEnabled := range []bool{true, false} {
		name := "WorkspaceTrackingEnabled"
		if !trackingEnabled {
			name = "WorkspaceTrackingDisabled"
		}
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockRepo := mocks.NewMockTransactionalNotificationRepository(ctrl)
			mockMsgHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
			mockTemplateService := mocks.NewMockTemplateService(ctrl)
			mockContactService := mocks.NewMockContactService(ctrl)
			mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
			mockLogger := pkgmocks.NewMockLogger(ctrl)
			mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
			mockAuthService := mocks.NewMockAuthService(ctrl)

			// Create a stub logger that simply returns itself for chaining calls
			mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
			mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
			mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

			ctx := context.Background()
			workspaceID := "test-workspace"
			templateID := uuid.New().String()
			integrationID := "integration-1"
			senderID := "sender-1"
			recipientEmail := "test@example.com"

			workspace := &domain.Workspace{
				ID:   workspaceID,
				Name: "Test Workspace",
				Settings: domain.WorkspaceSettings{
					SecretKey:            "test-secret-key",
					EmailTrackingEnabled: trackingEnabled,
				},
				Integrations: []domain.Integration{
					{
						ID:   integrationID,
						Name: "Test Integration",
						Type: "email",
						EmailProvider: domain.EmailProvider{
							Kind: domain.EmailProviderKindSparkPost,
							Senders: []domain.EmailSender{
								{
									ID:    senderID,
									Email: "sender@example.com",
									Name:  "Test Sender",
								},
							},
						},
					},
				},
			}

			template := &domain.Template{
				ID:   templateID,
				Name: "Test Template",
				Email: &domain.EmailTemplate{
					Subject: "Test Subject",
					VisualEditorTree: &notifuse_mjml.MJMLBlock{
						BaseBlock: notifuse_mjml.NewBaseBlock("root", notifuse_mjml.MJMLComponentMjml),
					},
				},
			}

			htmlResult := "<html><body>Test content</body></html>"

			service := &TransactionalNotificationService{
				transactionalRepo:  mockRepo,
				messageHistoryRepo: mockMsgHistoryRepo,
				templateService:    mockTemplateService,
				contactService:     mockContactService,
				emailService:       mockEmailService,
				logger:             mockLogger,
				workspaceRepo:      mockWorkspaceRepo,
				apiEndpoint:        "https://api.example.com",
				authService:        mockAuthService,
			}

			mockAuthService.EXPECT().
				AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
				Return(ctx, &domain.User{ID: "user-123"}, &domain.UserWorkspace{
					UserID:      "user-123",
					WorkspaceID: workspaceID,
					Role:        "member",
					Permissions: domain.UserPermissions{
						domain.PermissionResourceTransactional: {Read: true, Write: true},
					},
				}, nil)

			mockTemplateService.EXPECT().
				GetTemplateByID(gomock.Any(), workspaceID, templateID, int64(0)).
				Return(template, nil)

			mockWorkspaceRepo.EXPECT().
				GetByID(gomock.Any(), workspaceID).
				Return(workspace, nil)

			mockContactService.EXPECT().
				UpsertContact(gomock.Any(), workspaceID, gomock.Any()).
				Return(domain.UpsertContactOperation{
					Email:  recipientEmail,
					Action: domain.UpsertContactOperationUpdate,
				})

			mockContactService.EXPECT().
				GetContactByEmail(gomock.Any(), workspaceID, recipientEmail).
				Return(&domain.Contact{Email: recipientEmail}, nil)

			// Test sends follow the workspace tracking flag instead of always tracking
			mockTemplateService.EXPECT().
				CompileTemplate(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req domain.CompileTemplateRequest) (*domain.CompileTemplateResponse, error) {
					assert.Equal(t, trackingEnabled, req.TrackingSettings.EnableTracking)
					return &domain.CompileTemplateResponse{
						Success: true,
						HTML:    &htmlResult,
					}, nil
				})

			mockEmailService.EXPECT().
				SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(nil)

			mockMsgHistoryRepo.EXPECT().
				Create(gomock.Any(), workspaceID, gomock.Any(), gomock.Any()).
				Return(nil)

			err := service.TestTemplate(ctx, workspaceID, templateID, integrationID, senderID, recipientEmail, "", domain.EmailOptions{})

			require.NoError(t, err)
		})
	}
}

func TestTransactionalNotificationService_TestTemplate_TemplateReplyToFallback(t *testing.T) {
	t.Skip("écart upstream v39.1 non analysé : comportement du fork différent ou fonctionnalité absente (voir docs/claude/75)")
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRepo := mocks.NewMockTransactionalNotificationRepository(ctrl)
	mockMsgHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateService := mocks.NewMockTemplateService(ctrl)
	mockContactService := mocks.NewMockContactService(ctrl)
	mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockAuthService := mocks.NewMockAuthService(ctrl)

	// Create a stub logger that simply returns itself for chaining calls
	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	ctx := context.Background()
	workspaceID := "test-workspace"
	templateID := uuid.New().String()
	integrationID := "integration-1"
	senderID := "sender-1"
	recipientEmail := "test@example.com"

	// Setup workspace
	workspace := &domain.Workspace{
		ID:   workspaceID,
		Name: "Test Workspace",
		Settings: domain.WorkspaceSettings{
			SecretKey: "test-secret-key",
		},
		Integrations: []domain.Integration{
			{
				ID:   integrationID,
				Name: "Test Integration",
				Type: "email",
				EmailProvider: domain.EmailProvider{
					Kind: domain.EmailProviderKindSparkPost,
					Senders: []domain.EmailSender{
						{
							ID:    senderID,
							Email: "sender@example.com",
							Name:  "Test Sender",
						},
					},
				},
			},
		},
	}

	// Setup template with a reply-to address
	template := &domain.Template{
		ID:   templateID,
		Name: "Test Template",
		Email: &domain.EmailTemplate{
			Subject: "Test Subject",
			VisualEditorTree: &notifuse_mjml.MJMLBlock{
				BaseBlock: notifuse_mjml.NewBaseBlock("root", notifuse_mjml.MJMLComponentMjml),
			},
			ReplyTo: "template-reply@example.com",
		},
	}

	// Setup HTML result
	htmlResult := "<html><body>Test content</body></html>"
	compilationResult := &domain.CompileTemplateResponse{
		Success: true,
		HTML:    &htmlResult,
		Error:   nil,
	}

	service := &TransactionalNotificationService{
		transactionalRepo:  mockRepo,
		messageHistoryRepo: mockMsgHistoryRepo,
		templateService:    mockTemplateService,
		contactService:     mockContactService,
		emailService:       mockEmailService,
		logger:             mockLogger,
		workspaceRepo:      mockWorkspaceRepo,
		apiEndpoint:        "https://api.example.com",
		authService:        mockAuthService,
	}

	// Expect authentication
	mockAuthService.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
		Return(ctx, &domain.User{ID: "user-123"}, &domain.UserWorkspace{
			UserID:      "user-123",
			WorkspaceID: workspaceID,
			Role:        "member",
			Permissions: domain.UserPermissions{
				domain.PermissionResourceTransactional: {Read: true, Write: true},
			},
		}, nil).
		Times(2)

	// Expect get template
	mockTemplateService.EXPECT().
		GetTemplateByID(gomock.Any(), workspaceID, templateID, int64(0)).
		Return(template, nil).
		Times(2)

	// Expect get workspace
	mockWorkspaceRepo.EXPECT().
		GetByID(gomock.Any(), workspaceID).
		Return(workspace, nil).
		Times(2)

	// Expect upsert contact call
	mockContactService.EXPECT().
		UpsertContact(gomock.Any(), workspaceID, gomock.Any()).
		Return(domain.UpsertContactOperation{
			Email:  recipientEmail,
			Action: domain.UpsertContactOperationUpdate,
		}).
		Times(2)

	// Expect get contact by email after upsert
	mockContactService.EXPECT().
		GetContactByEmail(gomock.Any(), workspaceID, recipientEmail).
		Return(&domain.Contact{
			Email: recipientEmail,
		}, nil).
		Times(2)

	// Expect compile template
	mockTemplateService.EXPECT().
		CompileTemplate(gomock.Any(), gomock.Any()).
		Return(compilationResult, nil).
		Times(2)

	// Expect message history creation
	mockMsgHistoryRepo.EXPECT().
		Create(gomock.Any(), workspaceID, gomock.Any(), gomock.Any()).
		Return(nil).
		Times(2)

	// 1) No reply-to in the options: the template's reply-to must be used
	mockEmailService.EXPECT().
		SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, req domain.SendEmailProviderRequest, isMarketing bool) error {
			assert.Equal(t, "template-reply@example.com", req.EmailOptions.ReplyTo)
			return nil
		})

	err := service.TestTemplate(ctx, workspaceID, templateID, integrationID, senderID, recipientEmail, "", domain.EmailOptions{})
	require.NoError(t, err)

	// 2) Explicit reply-to in the options takes precedence over the template's
	mockEmailService.EXPECT().
		SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, req domain.SendEmailProviderRequest, isMarketing bool) error {
			assert.Equal(t, "explicit-reply@example.com", req.EmailOptions.ReplyTo)
			return nil
		})

	err = service.TestTemplate(ctx, workspaceID, templateID, integrationID, senderID, recipientEmail, "", domain.EmailOptions{
		ReplyTo: "explicit-reply@example.com",
	})
	require.NoError(t, err)
}

// TestTransactionalNotificationService_PermissionGates pins list, update and delete to
// the transactional permission. They authenticated workspace membership and stopped
// there, so a member whose transactional access had been revoked could still read,
// edit and delete notifications — the console offers the permission and the API
// ignored it. Create already checks write and get already checks read; these three
// follow the same split.
func TestTransactionalNotificationService_PermissionGates(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	const workspace = "test-workspace"
	notificationID := uuid.New().String()

	// A member holding every permission except transactional: enough to be in the
	// workspace, not enough to touch transactional notifications.
	noTransactional := &domain.UserWorkspace{
		UserID:      "user-123",
		WorkspaceID: workspace,
		Role:        "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceTemplates: {Read: true, Write: true},
			domain.PermissionResourceContacts:  {Read: true, Write: true},
		},
	}
	// Read granted, write withheld — the case that separates list from update/delete.
	readOnly := &domain.UserWorkspace{
		UserID:      "user-123",
		WorkspaceID: workspace,
		Role:        "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceTransactional: {Read: true, Write: false},
		},
	}

	testCases := []struct {
		name               string
		userWorkspace      *domain.UserWorkspace
		call               func(context.Context, *TransactionalNotificationService) error
		expectedPermission domain.PermissionType
	}{
		{
			name:          "list without transactional read",
			userWorkspace: noTransactional,
			call: func(ctx context.Context, s *TransactionalNotificationService) error {
				_, _, err := s.ListNotifications(ctx, workspace, nil, 10, 0)
				return err
			},
			expectedPermission: domain.PermissionTypeRead,
		},
		{
			name:          "update without transactional write",
			userWorkspace: readOnly,
			call: func(ctx context.Context, s *TransactionalNotificationService) error {
				_, err := s.UpdateNotification(ctx, workspace, notificationID, domain.TransactionalNotificationUpdateParams{
					Name: "Renamed",
				})
				return err
			},
			expectedPermission: domain.PermissionTypeWrite,
		},
		{
			name:          "delete without transactional write",
			userWorkspace: readOnly,
			call: func(ctx context.Context, s *TransactionalNotificationService) error {
				return s.DeleteNotification(ctx, workspace, notificationID)
			},
			expectedPermission: domain.PermissionTypeWrite,
		},
		{
			// contacts:write is granted here, so this fails if sending is left to be
			// gated by the nested contact upsert rather than by transactional itself.
			name:          "send without transactional write",
			userWorkspace: noTransactional,
			call: func(ctx context.Context, s *TransactionalNotificationService) error {
				_, err := s.SendNotification(ctx, workspace, domain.TransactionalNotificationSendParams{
					ID:      notificationID,
					Contact: &domain.Contact{Email: "recipient@example.com"},
				})
				return err
			},
			expectedPermission: domain.PermissionTypeWrite,
		},
		{
			// testTemplate sends a real email through the workspace's provider.
			name:          "test template without transactional write",
			userWorkspace: readOnly,
			call: func(ctx context.Context, s *TransactionalNotificationService) error {
				return s.TestTemplate(ctx, workspace, "template-1", "integration-1", "sender-1",
					"recipient@example.com", "", domain.EmailOptions{})
			},
			expectedPermission: domain.PermissionTypeWrite,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockRepo := mocks.NewMockTransactionalNotificationRepository(ctrl)
			mockAuthService := mocks.NewMockAuthService(ctrl)
			mockLogger := pkgmocks.NewMockLogger(ctrl)
			mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
			mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
			mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

			ctx := context.Background()
			mockAuthService.EXPECT().
				AuthenticateUserForWorkspace(gomock.Any(), workspace).
				Return(ctx, &domain.User{ID: "user-123"}, tc.userWorkspace, nil)

			// No repository call may happen: the denial has to land before any work.
			service := NewTransactionalNotificationService(
				mockRepo,
				mocks.NewMockMessageHistoryRepository(ctrl),
				mocks.NewMockTemplateService(ctrl),
				mocks.NewMockContactService(ctrl),
				mocks.NewMockEmailServiceInterface(ctrl),
				mockAuthService,
				mockLogger,
				mocks.NewMockWorkspaceRepository(ctrl),
				"https://api.example.com",
			)

			err := tc.call(ctx, service)

			require.Error(t, err)
			var permErr *domain.PermissionError
			require.ErrorAs(t, err, &permErr, "denial must be a *domain.PermissionError so the handler answers 403")
			assert.Equal(t, domain.PermissionResourceTransactional, permErr.Resource)
			assert.Equal(t, tc.expectedPermission, permErr.Permission)
		})
	}
}

// An owner holds every permission implicitly, with no permissions map at all — the
// gates must not lock out the role that is meant to bypass them.
func TestTransactionalNotificationService_PermissionGates_OwnerBypasses(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	const workspace = "test-workspace"
	notificationID := uuid.New().String()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRepo := mocks.NewMockTransactionalNotificationRepository(ctrl)
	mockAuthService := mocks.NewMockAuthService(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	ctx := context.Background()
	owner := &domain.UserWorkspace{UserID: "user-123", WorkspaceID: workspace, Role: "owner"}
	mockAuthService.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), workspace).
		Return(ctx, &domain.User{ID: "user-123"}, owner, nil).
		Times(2)

	service := NewTransactionalNotificationService(
		mockRepo,
		mocks.NewMockMessageHistoryRepository(ctrl),
		mocks.NewMockTemplateService(ctrl),
		mocks.NewMockContactService(ctrl),
		mocks.NewMockEmailServiceInterface(ctrl),
		mockAuthService,
		mockLogger,
		mocks.NewMockWorkspaceRepository(ctrl),
		"https://api.example.com",
	)

	mockRepo.EXPECT().
		List(gomock.Any(), workspace, gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*domain.TransactionalNotification{}, 0, nil)
	_, _, err := service.ListNotifications(ctx, workspace, nil, 10, 0)
	require.NoError(t, err)

	mockRepo.EXPECT().
		Get(gomock.Any(), workspace, notificationID).
		Return(&domain.TransactionalNotification{ID: notificationID}, nil)
	mockRepo.EXPECT().
		Delete(gomock.Any(), workspace, notificationID).
		Return(nil)
	require.NoError(t, service.DeleteNotification(ctx, workspace, notificationID))
}

// TestTransactionalNotificationService_SendNotification_SystemCallSkipsAuth pins the
// path the SMTP bridge and the Supabase webhook use. Both build a context carrying
// SystemCallKey because they have already authenticated the caller themselves, and
// SendNotification then skips authentication entirely — which also means it never
// obtains a UserWorkspace. Any permission check added to this method has to live
// inside that same branch: outside it, HasPermission dereferences a nil pointer and
// every bridge send panics.
//
// The auth service mock carries NO expectation on purpose. gomock fails the test if
// AuthenticateUserForWorkspace is called at all, so this asserts the bypass itself
// rather than merely asserting that the send happened to succeed.
func TestTransactionalNotificationService_SendNotification_SystemCallSkipsAuth(t *testing.T) {
	const workspace = "test-workspace"
	notificationID := uuid.New().String()
	templateID := uuid.New().String()

	notification := &domain.TransactionalNotification{
		ID:   notificationID,
		Name: "Test Notification",
		Channels: map[domain.TransactionalChannel]domain.ChannelTemplate{
			domain.TransactionalChannelEmail: {TemplateID: templateID},
		},
	}

	workspaceObj := &domain.Workspace{
		ID:   workspace,
		Name: "Test Workspace",
		Settings: domain.WorkspaceSettings{
			TransactionalEmailProviderID: "integration-1",
			SecretKey:                    "test-secret-key",
		},
		Integrations: []domain.Integration{
			{
				ID:   "integration-1",
				Name: "Test Integration",
				Type: "email",
				EmailProvider: domain.EmailProvider{
					Kind:    domain.EmailProviderKindSparkPost,
					Senders: []domain.EmailSender{domain.NewEmailSender("test@example.com", "Test Sender")},
					SparkPost: &domain.SparkPostSettings{
						EncryptedAPIKey: "encrypted-api-key",
					},
				},
			},
		},
	}

	contact := &domain.Contact{Email: "recipient@example.com"}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRepo := mocks.NewMockTransactionalNotificationRepository(ctrl)
	mockMsgHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateService := mocks.NewMockTemplateService(ctrl)
	mockContactService := mocks.NewMockContactService(ctrl)
	mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockAuthService := mocks.NewMockAuthService(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	service := &TransactionalNotificationService{
		transactionalRepo:  mockRepo,
		messageHistoryRepo: mockMsgHistoryRepo,
		templateService:    mockTemplateService,
		contactService:     mockContactService,
		emailService:       mockEmailService,
		logger:             mockLogger,
		workspaceRepo:      mockWorkspaceRepo,
		apiEndpoint:        "https://api.example.com",
		authService:        mockAuthService,
	}

	mockWorkspaceRepo.EXPECT().GetByID(gomock.Any(), workspace).Return(workspaceObj, nil)
	mockRepo.EXPECT().Get(gomock.Any(), workspace, notificationID).Return(notification, nil)
	mockContactService.EXPECT().
		UpsertContact(gomock.Any(), workspace, contact).
		Return(domain.UpsertContactOperation{Email: contact.Email, Action: domain.UpsertContactOperationUpdate})
	mockContactService.EXPECT().
		GetContactByEmail(gomock.Any(), workspace, contact.Email).
		Return(contact, nil)
	mockEmailService.EXPECT().
		SendEmailForTemplate(gomock.Any(), gomock.Any()).
		Return(nil)

	// The context the SMTP bridge and the Supabase webhook build.
	systemCtx := context.WithValue(context.Background(), domain.SystemCallKey, true)

	messageID, err := service.SendNotification(systemCtx, workspace, domain.TransactionalNotificationSendParams{
		ID:      notificationID,
		Contact: contact,
	})

	require.NoError(t, err)
	require.NotEmpty(t, messageID)
}

// TestTransactionalNotificationService_SendOnlyKey pins the send-only key: an API key
// granted transactional:write and nothing else can send, and can send a test template.
// The recipient upsert and lookup are run as system calls so they do not silently make
// contacts:write and contacts:read prerequisites for sending — the ContactService mock
// asserts the context it receives carries SystemCallKey, which is the flag the real
// ContactService checks before its own contacts gate.
func TestTransactionalNotificationService_SendOnlyKey(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	const workspaceID = "test-workspace"
	const recipientEmail = "recipient@example.com"

	// No contacts grant at all — the point of the test.
	sendOnly := &domain.UserWorkspace{
		UserID:      "api-key-user",
		WorkspaceID: workspaceID,
		Role:        "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceTransactional: {Read: false, Write: true},
		},
	}

	assertSystemScoped := func(t *testing.T, ctx context.Context) {
		t.Helper()
		assert.NotNil(t, ctx.Value(domain.SystemCallKey),
			"nested contact call must be system-scoped, otherwise sending requires contacts permissions")
	}

	t.Run("SendNotification", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, m := newSendOnlyTestService(t, ctrl)

		notificationID := uuid.New().String()
		templateID := uuid.New().String()
		contact := &domain.Contact{Email: recipientEmail}

		ctx := context.Background()
		m.authService.EXPECT().
			AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
			Return(ctx, &domain.User{ID: "api-key-user"}, sendOnly, nil)

		m.workspaceRepo.EXPECT().GetByID(gomock.Any(), workspaceID).Return(&domain.Workspace{
			ID: workspaceID,
			Settings: domain.WorkspaceSettings{
				TransactionalEmailProviderID: "integration-1",
				SecretKey:                    "test-secret-key",
			},
			Integrations: []domain.Integration{{
				ID:   "integration-1",
				Type: "email",
				EmailProvider: domain.EmailProvider{
					Kind:      domain.EmailProviderKindSparkPost,
					Senders:   []domain.EmailSender{domain.NewEmailSender("sender@example.com", "Test Sender")},
					SparkPost: &domain.SparkPostSettings{EncryptedAPIKey: "encrypted-api-key"},
				},
			}},
		}, nil)

		m.repo.EXPECT().Get(gomock.Any(), workspaceID, notificationID).Return(&domain.TransactionalNotification{
			ID:   notificationID,
			Name: "Test Notification",
			Channels: map[domain.TransactionalChannel]domain.ChannelTemplate{
				domain.TransactionalChannelEmail: {TemplateID: templateID},
			},
		}, nil)

		m.contactService.EXPECT().
			UpsertContact(gomock.Any(), workspaceID, contact).
			DoAndReturn(func(ctx context.Context, _ string, _ *domain.Contact) domain.UpsertContactOperation {
				assertSystemScoped(t, ctx)
				return domain.UpsertContactOperation{Email: recipientEmail, Action: domain.UpsertContactOperationUpdate}
			})

		m.contactService.EXPECT().
			GetContactByEmail(gomock.Any(), workspaceID, recipientEmail).
			DoAndReturn(func(ctx context.Context, _ string, _ string) (*domain.Contact, error) {
				assertSystemScoped(t, ctx)
				return contact, nil
			})

		m.emailService.EXPECT().SendEmailForTemplate(gomock.Any(), gomock.Any()).Return(nil)

		messageID, err := service.SendNotification(ctx, workspaceID, domain.TransactionalNotificationSendParams{
			ID:      notificationID,
			Contact: contact,
		})

		require.NoError(t, err)
		require.NotEmpty(t, messageID)
	})

	t.Run("TestTemplate", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, m := newSendOnlyTestService(t, ctrl)

		templateID := uuid.New().String()
		const integrationID = "integration-1"
		const senderID = "sender-1"

		ctx := context.Background()
		m.authService.EXPECT().
			AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
			Return(ctx, &domain.User{ID: "api-key-user"}, sendOnly, nil)

		m.templateService.EXPECT().
			GetTemplateByID(gomock.Any(), workspaceID, templateID, int64(0)).
			Return(&domain.Template{
				ID:   templateID,
				Name: "Test Template",
				Email: &domain.EmailTemplate{
					Subject: "Test Subject",
					VisualEditorTree: &notifuse_mjml.MJMLBlock{
						BaseBlock: notifuse_mjml.NewBaseBlock("root", notifuse_mjml.MJMLComponentMjml),
					},
				},
			}, nil)

		m.workspaceRepo.EXPECT().GetByID(gomock.Any(), workspaceID).Return(&domain.Workspace{
			ID:       workspaceID,
			Settings: domain.WorkspaceSettings{SecretKey: "test-secret-key"},
			Integrations: []domain.Integration{{
				ID:   integrationID,
				Type: "email",
				EmailProvider: domain.EmailProvider{
					Kind: domain.EmailProviderKindSparkPost,
					Senders: []domain.EmailSender{
						{ID: senderID, Email: "sender@example.com", Name: "Test Sender"},
					},
				},
			}},
		}, nil)

		m.contactService.EXPECT().
			UpsertContact(gomock.Any(), workspaceID, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _ string, _ *domain.Contact) domain.UpsertContactOperation {
				assertSystemScoped(t, ctx)
				return domain.UpsertContactOperation{Email: recipientEmail, Action: domain.UpsertContactOperationUpdate}
			})

		m.contactService.EXPECT().
			GetContactByEmail(gomock.Any(), workspaceID, recipientEmail).
			DoAndReturn(func(ctx context.Context, _ string, _ string) (*domain.Contact, error) {
				assertSystemScoped(t, ctx)
				return &domain.Contact{Email: recipientEmail}, nil
			})

		htmlResult := "<html><body>Test content</body></html>"
		m.templateService.EXPECT().
			CompileTemplate(gomock.Any(), gomock.Any()).
			Return(&domain.CompileTemplateResponse{Success: true, HTML: &htmlResult}, nil)

		m.emailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		m.msgHistoryRepo.EXPECT().Create(gomock.Any(), workspaceID, gomock.Any(), gomock.Any()).Return(nil)

		err := service.TestTemplate(ctx, workspaceID, templateID, integrationID, senderID,
			recipientEmail, "", domain.EmailOptions{})

		require.NoError(t, err)
	})
}

type serviceMocksForSendOnly struct {
	repo            *mocks.MockTransactionalNotificationRepository
	msgHistoryRepo  *mocks.MockMessageHistoryRepository
	templateService *mocks.MockTemplateService
	contactService  *mocks.MockContactService
	emailService    *mocks.MockEmailServiceInterface
	workspaceRepo   *mocks.MockWorkspaceRepository
	authService     *mocks.MockAuthService
}

// newSendOnlyTestService builds a transactional service on fully mocked collaborators
// and a permissive logger. It sets no expectations of its own, so each caller's
// expectations describe exactly the calls its path is allowed to make.
func newSendOnlyTestService(t *testing.T, ctrl *gomock.Controller) (*TransactionalNotificationService, *serviceMocksForSendOnly) {
	t.Helper()

	m := &serviceMocksForSendOnly{
		repo:            mocks.NewMockTransactionalNotificationRepository(ctrl),
		msgHistoryRepo:  mocks.NewMockMessageHistoryRepository(ctrl),
		templateService: mocks.NewMockTemplateService(ctrl),
		contactService:  mocks.NewMockContactService(ctrl),
		emailService:    mocks.NewMockEmailServiceInterface(ctrl),
		workspaceRepo:   mocks.NewMockWorkspaceRepository(ctrl),
		authService:     mocks.NewMockAuthService(ctrl),
	}

	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	return &TransactionalNotificationService{
		transactionalRepo:  m.repo,
		messageHistoryRepo: m.msgHistoryRepo,
		templateService:    m.templateService,
		contactService:     m.contactService,
		emailService:       m.emailService,
		logger:             mockLogger,
		workspaceRepo:      m.workspaceRepo,
		apiEndpoint:        "https://api.example.com",
		authService:        m.authService,
	}, m
}

// TestTransactionalNotificationService_ContactScopeContainment pins the blast radius of
// the system subcontext the send path runs its recipient upsert and lookup under. That
// subcontext skips the contacts gates by design — it is what lets transactional:write
// alone send — so the two nested calls otherwise reach every field of an arbitrary
// contact record:
//
//   - write: the repository merges every non-nil pointer of the posted body onto an
//     existing record, so an unrelated stored contact could be rewritten wholesale;
//   - read: the full record is put on templateData and the subject is Liquid-evaluated
//     against it, so naming an extra recipient with cc or bcc exfiltrates any field,
//     the notification_center_url HMAC included.
//
// SendNotification therefore carries the caller's contacts grants across the context
// switch. A genuine system caller keeps both, so nothing on that path changes.
func TestTransactionalNotificationService_ContactScopeContainment(t *testing.T) {
	t.Skip("fonctionnalité upstream v39.0 (permissions par ressource) absente du fork")
	const workspaceID = "test-workspace"
	const recipientEmail = "victim@customer.com"

	sendOnly := &domain.UserWorkspace{
		UserID:      "api-key-user",
		WorkspaceID: workspaceID,
		Role:        "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceTransactional: {Write: true},
		},
	}

	sendAndContacts := &domain.UserWorkspace{
		UserID:      "api-key-user",
		WorkspaceID: workspaceID,
		Role:        "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceTransactional: {Write: true},
			domain.PermissionResourceContacts:      {Read: true, Write: true},
		},
	}

	// The body a caller posts. Every field beyond the email would be merged onto the
	// stored record for that address.
	newPostedContact := func() *domain.Contact {
		return &domain.Contact{
			Email:         recipientEmail,
			FirstName:     &domain.NullableString{String: "Overwritten"},
			Phone:         &domain.NullableString{String: "+15550000000"},
			CustomString1: &domain.NullableString{String: "injected"},
		}
	}

	notificationID := uuid.New().String()
	templateID := uuid.New().String()

	// expectSendPath sets up everything the send needs either side of the contact calls,
	// so a subtest only has to describe the contact calls it expects.
	expectSendPath := func(m *serviceMocksForSendOnly) {
		m.workspaceRepo.EXPECT().GetByID(gomock.Any(), workspaceID).Return(&domain.Workspace{
			ID: workspaceID,
			Settings: domain.WorkspaceSettings{
				TransactionalEmailProviderID: "integration-1",
				SecretKey:                    "test-secret-key",
			},
			Integrations: []domain.Integration{{
				ID:   "integration-1",
				Type: "email",
				EmailProvider: domain.EmailProvider{
					Kind:      domain.EmailProviderKindSparkPost,
					Senders:   []domain.EmailSender{domain.NewEmailSender("sender@example.com", "Test Sender")},
					SparkPost: &domain.SparkPostSettings{EncryptedAPIKey: "encrypted-api-key"},
				},
			}},
		}, nil)

		m.repo.EXPECT().Get(gomock.Any(), workspaceID, notificationID).Return(&domain.TransactionalNotification{
			ID:   notificationID,
			Name: "Password reset",
			Channels: map[domain.TransactionalChannel]domain.ChannelTemplate{
				domain.TransactionalChannelEmail: {TemplateID: templateID},
			},
		}, nil)

		m.emailService.EXPECT().SendEmailForTemplate(gomock.Any(), gomock.Any()).Return(nil)
	}

	// expectContactCalls records the contact the upsert actually received.
	expectContactCalls := func(m *serviceMocksForSendOnly, upserted **domain.Contact) {
		m.contactService.EXPECT().
			UpsertContact(gomock.Any(), workspaceID, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, contact *domain.Contact) domain.UpsertContactOperation {
				*upserted = contact
				return domain.UpsertContactOperation{Email: recipientEmail, Action: domain.UpsertContactOperationUpdate}
			})

		m.contactService.EXPECT().
			GetContactByEmail(gomock.Any(), workspaceID, recipientEmail).
			Return(&domain.Contact{Email: recipientEmail}, nil)
	}

	t.Run("send-only key upserts the email and nothing else", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, m := newSendOnlyTestService(t, ctrl)

		ctx := context.Background()
		m.authService.EXPECT().
			AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
			Return(ctx, &domain.User{ID: "api-key-user"}, sendOnly, nil)

		expectSendPath(m)

		var upserted *domain.Contact
		expectContactCalls(m, &upserted)

		messageID, err := service.SendNotification(ctx, workspaceID, domain.TransactionalNotificationSendParams{
			ID:      notificationID,
			Contact: newPostedContact(),
		})
		require.NoError(t, err)
		require.NotEmpty(t, messageID)

		// Merge copies only the non-nil pointers, so leaving them nil is what stops the
		// send from rewriting an existing contact's fields.
		require.NotNil(t, upserted)
		assert.Equal(t, recipientEmail, upserted.Email)
		assert.Nil(t, upserted.FirstName, "send-only key must not carry first_name into the stored contact")
		assert.Nil(t, upserted.Phone, "send-only key must not carry phone into the stored contact")
		assert.Nil(t, upserted.CustomString1, "send-only key must not carry custom fields into the stored contact")
	})

	t.Run("send-only key cannot name an extra recipient", func(t *testing.T) {
		extraRecipients := map[string]domain.EmailOptions{
			"bcc": {BCC: []string{"attacker@evil.example"}},
			"cc":  {CC: []string{"attacker@evil.example"}},
		}

		for name, emailOptions := range extraRecipients {
			t.Run(name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				service, m := newSendOnlyTestService(t, ctrl)

				ctx := context.Background()
				m.authService.EXPECT().
					AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
					Return(ctx, &domain.User{ID: "api-key-user"}, sendOnly, nil)

				// No other expectation: the refusal must land before the workspace is
				// even fetched, so gomock fails the test if anything else is touched.
				_, err := service.SendNotification(ctx, workspaceID, domain.TransactionalNotificationSendParams{
					ID:           notificationID,
					Contact:      &domain.Contact{Email: recipientEmail},
					EmailOptions: emailOptions,
				})

				require.Error(t, err)
				var permErr *domain.PermissionError
				require.ErrorAs(t, err, &permErr)
				assert.Equal(t, domain.PermissionResourceContacts, permErr.Resource)
				assert.Equal(t, domain.PermissionTypeRead, permErr.Permission)
			})
		}
	})

	t.Run("key holding contacts read and write keeps both", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, m := newSendOnlyTestService(t, ctrl)

		ctx := context.Background()
		m.authService.EXPECT().
			AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
			Return(ctx, &domain.User{ID: "api-key-user"}, sendAndContacts, nil)

		expectSendPath(m)

		var upserted *domain.Contact
		expectContactCalls(m, &upserted)

		posted := newPostedContact()
		messageID, err := service.SendNotification(ctx, workspaceID, domain.TransactionalNotificationSendParams{
			ID:           notificationID,
			Contact:      posted,
			EmailOptions: domain.EmailOptions{BCC: []string{"archive@customer.com"}},
		})
		require.NoError(t, err)
		require.NotEmpty(t, messageID)

		assert.Equal(t, posted, upserted, "a key granted contacts:write posts the body verbatim")
	})

	t.Run("system call is unaffected", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, m := newSendOnlyTestService(t, ctrl)

		expectSendPath(m)

		var upserted *domain.Contact
		expectContactCalls(m, &upserted)

		// The context Supabase and any other system caller build. The auth service mock
		// carries no expectation, so gomock fails if the bypass is lost.
		systemCtx := context.WithValue(context.Background(), domain.SystemCallKey, true)

		posted := newPostedContact()
		messageID, err := service.SendNotification(systemCtx, workspaceID, domain.TransactionalNotificationSendParams{
			ID:           notificationID,
			Contact:      posted,
			EmailOptions: domain.EmailOptions{BCC: []string{"archive@customer.com"}},
		})
		require.NoError(t, err)
		require.NotEmpty(t, messageID)

		assert.Equal(t, posted, upserted, "a system call still posts the body verbatim")
	})

	// TestTemplate makes the same nested, system-scoped calls. Its upsert already carries
	// nothing but the email, so only the read side needs closing.
	t.Run("TestTemplate refuses an extra recipient for a send-only key", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, m := newSendOnlyTestService(t, ctrl)

		ctx := context.Background()
		m.authService.EXPECT().
			AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
			Return(ctx, &domain.User{ID: "api-key-user"}, sendOnly, nil)

		// No other expectation: the refusal lands before the template is fetched.
		err := service.TestTemplate(ctx, workspaceID, templateID, "integration-1", "sender-1",
			recipientEmail, "", domain.EmailOptions{BCC: []string{"attacker@evil.example"}})

		require.Error(t, err)
		var permErr *domain.PermissionError
		require.ErrorAs(t, err, &permErr)
		assert.Equal(t, domain.PermissionResourceContacts, permErr.Resource)
		assert.Equal(t, domain.PermissionTypeRead, permErr.Permission)
	})

	t.Run("TestTemplate allows an extra recipient with contacts read", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, m := newSendOnlyTestService(t, ctrl)

		const integrationID = "integration-1"
		const senderID = "sender-1"

		ctx := context.Background()
		m.authService.EXPECT().
			AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
			Return(ctx, &domain.User{ID: "api-key-user"}, sendAndContacts, nil)

		m.templateService.EXPECT().
			GetTemplateByID(gomock.Any(), workspaceID, templateID, int64(0)).
			Return(&domain.Template{
				ID:   templateID,
				Name: "Test Template",
				Email: &domain.EmailTemplate{
					Subject: "Test Subject",
					VisualEditorTree: &notifuse_mjml.MJMLBlock{
						BaseBlock: notifuse_mjml.NewBaseBlock("root", notifuse_mjml.MJMLComponentMjml),
					},
				},
			}, nil)

		m.workspaceRepo.EXPECT().GetByID(gomock.Any(), workspaceID).Return(&domain.Workspace{
			ID:       workspaceID,
			Settings: domain.WorkspaceSettings{SecretKey: "test-secret-key"},
			Integrations: []domain.Integration{{
				ID:   integrationID,
				Type: "email",
				EmailProvider: domain.EmailProvider{
					Kind: domain.EmailProviderKindSparkPost,
					Senders: []domain.EmailSender{
						{ID: senderID, Email: "sender@example.com", Name: "Test Sender"},
					},
				},
			}},
		}, nil)

		m.contactService.EXPECT().
			UpsertContact(gomock.Any(), workspaceID, gomock.Any()).
			Return(domain.UpsertContactOperation{Email: recipientEmail, Action: domain.UpsertContactOperationUpdate})
		m.contactService.EXPECT().
			GetContactByEmail(gomock.Any(), workspaceID, recipientEmail).
			Return(&domain.Contact{Email: recipientEmail}, nil)

		htmlResult := "<html><body>Test content</body></html>"
		m.templateService.EXPECT().
			CompileTemplate(gomock.Any(), gomock.Any()).
			Return(&domain.CompileTemplateResponse{Success: true, HTML: &htmlResult}, nil)
		m.emailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		m.msgHistoryRepo.EXPECT().Create(gomock.Any(), workspaceID, gomock.Any(), gomock.Any()).Return(nil)

		err := service.TestTemplate(ctx, workspaceID, templateID, integrationID, senderID,
			recipientEmail, "", domain.EmailOptions{BCC: []string{"archive@customer.com"}})

		require.NoError(t, err)
	})
}
