package service

import (
	"bytes"
	"context"
	"errors"
	"net/mail"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/ratelimiter"
	"github.com/Notifuse/notifuse/pkg/smtp_bridge"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/golang-jwt/jwt/v5"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

func TestSMTPBridgeHandlerService_Authenticate_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create a test JWT secret
	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")
	apiEmail := "api@example.com"

	// Create a valid API key token
	claims := UserClaims{
		UserID: "api-user-123",
		Email:  apiEmail,
		Type:   string(domain.UserTypeAPIKey),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	apiKey, err := token.SignedString(jwtSecret)
	if err != nil {
		t.Fatalf("Failed to create test token: %v", err)
	}

	log := logger.NewLogger()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()
	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	service := NewSMTPBridgeHandlerService(nil, nil, mockRepo, log, jwtSecret, rl)

	// Une cle valide a au moins un workspace : c'est ce que l'AUTH verifie (cle non revoquee).
	mockRepo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-123").
		Return([]*domain.UserWorkspace{{UserID: "api-user-123", WorkspaceID: "ws1"}}, nil).Times(1)

	userID, err := service.Authenticate(apiEmail, apiKey)

	assert.NoError(t, err)
	assert.Equal(t, "api-user-123", userID)
}

func TestSMTPBridgeHandlerService_Authenticate_InvalidToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")

	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	log := logger.NewLogger()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()
	service := NewSMTPBridgeHandlerService(nil, nil, mockRepo, log, jwtSecret, rl)

	_, err := service.Authenticate("workspace123", "invalid-token")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid API key")
}

func TestSMTPBridgeHandlerService_Authenticate_WrongUserType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")
	apiEmail := "user@example.com"

	// Create a token with wrong user type
	claims := UserClaims{
		UserID: "regular-user-123",
		Email:  apiEmail,
		Type:   string(domain.UserTypeUser), // Not an API key
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	apiKey, _ := token.SignedString(jwtSecret)

	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	log := logger.NewLogger()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()
	service := NewSMTPBridgeHandlerService(nil, nil, mockRepo, log, jwtSecret, rl)

	_, err := service.Authenticate(apiEmail, apiKey)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must be an API key")
}

func TestSMTPBridgeHandlerService_Authenticate_NoWorkspaceAccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")
	apiEmail := "api@example.com"

	claims := UserClaims{
		UserID: "api-user-123",
		Email:  apiEmail,
		Type:   string(domain.UserTypeAPIKey),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	apiKey, _ := token.SignedString(jwtSecret)

	log := logger.NewLogger()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()
	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	service := NewSMTPBridgeHandlerService(nil, nil, mockRepo, log, jwtSecret, rl)

	// Cle REVOQUEE (08/11/2026) : sa signature est toujours valide, mais elle n'a plus aucun workspace
	// (removeMember). L'AUTH la refuse (535 5.7.8) au lieu de la laisser passer jusqu'a l'envoi (550).
	mockRepo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-123").Return(nil, nil).Times(1)

	userID, err := service.Authenticate(apiEmail, apiKey)

	assert.Error(t, err)
	assert.Empty(t, userID)
	assert.False(t, errors.Is(err, smtp_bridge.ErrTemporaryAuth), "une cle revoquee est un refus permanent")
}

// A travers le bridge reel : cle revoquee => 535 5.7.8 des l'AUTH ; panne de lecture => 454 temporaire.
func TestSMTPBridgeHandlerService_Authenticate_RevokedKeyRefusedAt535(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")
	apiEmail := "api@example.com"
	claims := UserClaims{
		UserID: "api-user-revoked", Email: apiEmail, Type: string(domain.UserTypeAPIKey),
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())},
	}
	apiKey, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)

	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 50, 1*time.Minute)
	defer rl.Stop()
	service := NewSMTPBridgeHandlerService(nil, nil, mockRepo, logger.NewLogger(), jwtSecret, rl)
	backend := smtp_bridge.NewBackend(service.Authenticate,
		func(string, string, []string, []byte) error { return nil }, logger.NewLogger())
	session, err := backend.NewSession(nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	s := session.(*smtp_bridge.Session)

	auth := func() *smtp.SMTPError {
		srv, aerr := s.Auth(sasl.Plain)
		if aerr != nil {
			t.Fatalf("Auth: %v", aerr)
		}
		_, _, nerr := srv.Next([]byte("\x00" + apiEmail + "\x00" + apiKey))
		if nerr == nil {
			return nil
		}
		var se *smtp.SMTPError
		if !errors.As(nerr, &se) {
			t.Fatalf("erreur SMTP attendue, got %T %v", nerr, nerr)
		}
		return se
	}

	mockRepo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-revoked").Return(nil, nil).Times(1)
	se := auth()
	if assert.NotNil(t, se, "une cle revoquee ne doit pas passer l'AUTH") {
		assert.Equal(t, 535, se.Code)
		assert.Equal(t, smtp.EnhancedCode{5, 7, 8}, se.EnhancedCode)
	}

	mockRepo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-revoked").Return(nil, errors.New("db down")).Times(1)
	se = auth()
	if assert.NotNil(t, se) {
		assert.Equal(t, 454, se.Code, "une panne de lecture est un refus temporaire, pas une revocation")
	}
}

func TestSMTPBridgeHandlerService_HandleMessage_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")
	userID := "api-user-123"
	workspaceID := "workspace123"

	// Create a simple email with JSON body
	emailBody := `From: sender@example.com
To: test@example.com
Subject: Test Email
Content-Type: text/plain

{
  "workspace_id": "workspace123",
  "notification": {
    "id": "password_reset",
    "contact": {
      "email": "user@example.com"
    },
    "data": {
      "reset_token": "abc123"
    }
  }
}`

	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockRepo.EXPECT().
		GetUserWorkspace(gomock.Any(), userID, workspaceID).
		Return(&domain.UserWorkspace{
			UserID:      userID,
			WorkspaceID: workspaceID,
			Role:        "member",
		}, nil)

	mockTransactionalService := mocks.NewMockTransactionalNotificationService(ctrl)
	mockTransactionalService.EXPECT().
		SendNotification(gomock.Any(), workspaceID, gomock.Any()).
		DoAndReturn(func(ctx context.Context, wid string, params domain.TransactionalNotificationSendParams) (string, error) {
			assert.Equal(t, "password_reset", params.ID)
			assert.Equal(t, "user@example.com", params.Contact.Email)
			return "msg-123", nil
		})

	log := logger.NewLogger()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()
	service := NewSMTPBridgeHandlerService(nil, mockTransactionalService, mockRepo, log, jwtSecret, rl)

	err := service.HandleMessage(userID, "sender@example.com", []string{"test@example.com"}, []byte(emailBody))

	assert.NoError(t, err)
}

func TestSMTPBridgeHandlerService_HandleMessage_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")
	userID := "api-user-123"

	emailBody := `From: sender@example.com
To: test@example.com
Subject: Test Email
Content-Type: text/plain

This is not JSON`

	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	log := logger.NewLogger()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()
	service := NewSMTPBridgeHandlerService(nil, nil, mockRepo, log, jwtSecret, rl)

	err := service.HandleMessage(userID, "sender@example.com", []string{"test@example.com"}, []byte(emailBody))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestSMTPBridgeHandlerService_HandleMessage_MissingNotificationID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")
	userID := "api-user-123"
	workspaceID := "workspace123"

	emailBody := `From: sender@example.com
To: test@example.com
Subject: Test Email
Content-Type: text/plain

{
  "workspace_id": "workspace123",
  "notification": {
    "contact": {
      "email": "user@example.com"
    }
  }
}`

	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockRepo.EXPECT().
		GetUserWorkspace(gomock.Any(), userID, workspaceID).
		Return(&domain.UserWorkspace{
			UserID:      userID,
			WorkspaceID: workspaceID,
			Role:        "member",
		}, nil)

	log := logger.NewLogger()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()
	service := NewSMTPBridgeHandlerService(nil, nil, mockRepo, log, jwtSecret, rl)

	err := service.HandleMessage(userID, "sender@example.com", []string{"test@example.com"}, []byte(emailBody))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "notification.id is required")
}

func TestSMTPBridgeHandlerService_HandleMessage_MissingContact(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")
	userID := "api-user-123"
	workspaceID := "workspace123"

	emailBody := `From: sender@example.com
To: test@example.com
Subject: Test Email
Content-Type: text/plain

{
  "workspace_id": "workspace123",
  "notification": {
    "id": "test"
  }
}`

	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockRepo.EXPECT().
		GetUserWorkspace(gomock.Any(), userID, workspaceID).
		Return(&domain.UserWorkspace{
			UserID:      userID,
			WorkspaceID: workspaceID,
			Role:        "member",
		}, nil)

	log := logger.NewLogger()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()
	service := NewSMTPBridgeHandlerService(nil, nil, mockRepo, log, jwtSecret, rl)

	err := service.HandleMessage(userID, "sender@example.com", []string{"test@example.com"}, []byte(emailBody))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "notification.contact is required")
}

func TestSMTPBridgeHandlerService_HandleMessage_WithEmailHeaders(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	userID := "api-user-123"
	workspaceID := "workspace123"

	// Setup mocks
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockWorkspaceRepo.EXPECT().
		GetUserWorkspace(gomock.Any(), userID, workspaceID).
		Return(&domain.UserWorkspace{
			UserID:      userID,
			WorkspaceID: workspaceID,
			Role:        "member",
		}, nil)

	mockTransactionalService := mocks.NewMockTransactionalNotificationService(ctrl)

	var capturedParams domain.TransactionalNotificationSendParams
	mockTransactionalService.EXPECT().
		SendNotification(gomock.Any(), workspaceID, gomock.Any()).
		DoAndReturn(func(ctx context.Context, wid string, params domain.TransactionalNotificationSendParams) (string, error) {
			capturedParams = params
			return "msg-123", nil
		})

	mockAuth := &AuthService{}
	log := logger.NewLogger()
	jwtSecret := []byte("test-secret")
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()

	service := NewSMTPBridgeHandlerService(
		mockAuth,
		mockTransactionalService,
		mockWorkspaceRepo,
		log,
		jwtSecret,
		rl,
	)

	// Create test email with CC, BCC, and Reply-To headers
	emailData := `From: sender@example.com
To: recipient@example.com
Cc: cc1@example.com, CC User <cc2@example.com>
Bcc: bcc@example.com
Reply-To: replyto@example.com
Subject: Test
Content-Type: text/plain

{
  "workspace_id": "workspace123",
  "notification": {
    "id": "password_reset",
    "contact": {
      "email": "test@example.com"
    }
  }
}`

	// Handle message
	err := service.HandleMessage(userID, "sender@example.com", []string{"recipient@example.com"}, []byte(emailData))
	assert.NoError(t, err)

	// Verify CC was extracted
	assert.Len(t, capturedParams.EmailOptions.CC, 2)
	assert.Equal(t, "cc1@example.com", capturedParams.EmailOptions.CC[0])
	assert.Equal(t, "cc2@example.com", capturedParams.EmailOptions.CC[1])

	// Verify BCC was extracted
	assert.Len(t, capturedParams.EmailOptions.BCC, 1)
	assert.Equal(t, "bcc@example.com", capturedParams.EmailOptions.BCC[0])

	// Verify Reply-To was extracted
	assert.Equal(t, "replyto@example.com", capturedParams.EmailOptions.ReplyTo)
}

func TestSMTPBridgeHandlerService_HandleMessage_JSONOverridesHeaders(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	userID := "api-user-123"
	workspaceID := "workspace123"

	// Setup mocks
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockWorkspaceRepo.EXPECT().
		GetUserWorkspace(gomock.Any(), userID, workspaceID).
		Return(&domain.UserWorkspace{
			UserID:      userID,
			WorkspaceID: workspaceID,
			Role:        "member",
		}, nil)

	mockTransactionalService := mocks.NewMockTransactionalNotificationService(ctrl)

	var capturedParams domain.TransactionalNotificationSendParams
	mockTransactionalService.EXPECT().
		SendNotification(gomock.Any(), workspaceID, gomock.Any()).
		DoAndReturn(func(ctx context.Context, wid string, params domain.TransactionalNotificationSendParams) (string, error) {
			capturedParams = params
			return "msg-123", nil
		})

	mockAuth := &AuthService{}
	log := logger.NewLogger()
	jwtSecret := []byte("test-secret")
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()

	service := NewSMTPBridgeHandlerService(
		mockAuth,
		mockTransactionalService,
		mockWorkspaceRepo,
		log,
		jwtSecret,
		rl,
	)

	// Create test email with headers AND JSON payload specifying email options
	emailData := `From: sender@example.com
To: recipient@example.com
Cc: header-cc@example.com
Reply-To: header-reply@example.com
Subject: Test
Content-Type: text/plain

{
  "workspace_id": "workspace123",
  "notification": {
    "id": "password_reset",
    "contact": {
      "email": "test@example.com"
    },
    "email_options": {
      "cc": ["json-cc@example.com"],
      "reply_to": "json-reply@example.com"
    }
  }
}`

	// Handle message
	err := service.HandleMessage(userID, "sender@example.com", []string{"recipient@example.com"}, []byte(emailData))
	assert.NoError(t, err)

	// Verify JSON payload took precedence
	assert.Len(t, capturedParams.EmailOptions.CC, 1)
	assert.Equal(t, "json-cc@example.com", capturedParams.EmailOptions.CC[0])
	assert.Equal(t, "json-reply@example.com", capturedParams.EmailOptions.ReplyTo)
}

func TestParseEmailAddresses(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Single email",
			input:    "user@example.com",
			expected: []string{"user@example.com"},
		},
		{
			name:     "Multiple emails",
			input:    "user1@example.com, user2@example.com",
			expected: []string{"user1@example.com", "user2@example.com"},
		},
		{
			name:     "Email with name",
			input:    "John Doe <john@example.com>",
			expected: []string{"john@example.com"},
		},
		{
			name:     "Mixed format",
			input:    "user1@example.com, John Doe <john@example.com>, user3@example.com",
			expected: []string{"user1@example.com", "john@example.com", "user3@example.com"},
		},
		{
			name:     "Empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "With extra spaces",
			input:    "  user1@example.com  ,  user2@example.com  ",
			expected: []string{"user1@example.com", "user2@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseEmailAddresses(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSMTPBridgeHandlerService_ExtractJSONPayload_Multipart(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")

	// Create a multipart email
	emailBody := `From: sender@example.com
To: test@example.com
Subject: Test Email
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="boundary123"

--boundary123
Content-Type: text/plain

{"notification": {"id": "test", "contact": {"email": "user@example.com"}}}
--boundary123--
`

	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockTransactionalService := mocks.NewMockTransactionalNotificationService(ctrl)
	log := logger.NewLogger()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 5, 1*time.Minute)
	defer rl.Stop()
	service := NewSMTPBridgeHandlerService(nil, mockTransactionalService, mockRepo, log, jwtSecret, rl)

	// Parse the email
	msg, err := mail.ReadMessage(bytes.NewReader([]byte(emailBody)))
	assert.NoError(t, err)

	jsonPayload, err := service.extractJSONPayload(msg)
	assert.NoError(t, err)
	assert.Contains(t, string(jsonPayload), `"id": "test"`)
}

// TestSMTPBridgeHandlerService_Authenticate_RefusalIsTemporaryOnlyWhenRateLimited : un identifiant
// invalide n'est JAMAIS marqué temporaire ; seule la limite de débit l'est (ErrTemporaryAuth, donc 454).
func TestSMTPBridgeHandlerService_Authenticate_RefusalIsTemporaryOnlyWhenRateLimited(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")
	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 2, 1*time.Minute)
	defer rl.Stop()
	service := NewSMTPBridgeHandlerService(nil, nil, mockRepo, logger.NewLogger(), jwtSecret, rl)

	sawTemporary := false
	for i := 0; i < 6; i++ {
		_, err := service.Authenticate("api@example.com", "invalid-token")
		if err == nil {
			t.Fatalf("tentative %d : identifiant invalide accepté", i)
		}
		if errors.Is(err, smtp_bridge.ErrTemporaryAuth) {
			sawTemporary = true
			assert.Contains(t, err.Error(), "rate limit exceeded")
			break
		}
		assert.NotContains(t, err.Error(), "rate limit")
	}
	assert.True(t, sawTemporary, "la limite de débit doit finir par rendre un échec temporaire")
}

// TestSMTPBridgeHandlerService_Authenticate_SMTPRefusalCodes : à travers le bridge réel, un mauvais
// mot de passe est refusé en 535 5.7.8 (permanent) et la limite de débit en 454 (temporaire).
func TestSMTPBridgeHandlerService_Authenticate_SMTPRefusalCodes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	jwtSecret := []byte("test-secret-key-for-jwt-signing-minimum-32-chars")
	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy("smtp", 2, 1*time.Minute)
	defer rl.Stop()
	service := NewSMTPBridgeHandlerService(nil, nil, mockRepo, logger.NewLogger(), jwtSecret, rl)

	backend := smtp_bridge.NewBackend(service.Authenticate,
		func(string, string, []string, []byte) error { return nil }, logger.NewLogger())
	session, err := backend.NewSession(nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	s := session.(*smtp_bridge.Session)

	codes := []int{}
	for i := 0; i < 6; i++ {
		srv, err := s.Auth(sasl.Plain)
		if err != nil {
			t.Fatalf("Auth: %v", err)
		}
		_, _, err = srv.Next([]byte("\x00api@example.com\x00invalid-token"))
		var se *smtp.SMTPError
		if !errors.As(err, &se) {
			t.Fatalf("erreur SMTP attendue, got %T %v", err, err)
		}
		codes = append(codes, se.Code)
	}
	assert.Equal(t, 535, codes[0], "un mauvais mot de passe doit être refusé en 535 permanent")
	assert.Equal(t, 454, codes[len(codes)-1], "la limite de débit doit être refusée en 454 temporaire")
}
