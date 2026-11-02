package smtp_bridge

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/emersion/go-smtp"
)

func TestBackend_NewSession(t *testing.T) {
	log := logger.NewLogger()

	authHandler := func(username, password string) (string, error) {
		return "user123", nil
	}

	messageHandler := func(userID string, from string, to []string, data []byte) error {
		return nil
	}

	backend := NewBackend(authHandler, messageHandler, log)

	session, err := backend.NewSession(nil)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	if session == nil {
		t.Fatal("Expected session to be created, got nil")
	}
}

func TestSession_AuthPlain(t *testing.T) {
	log := logger.NewLogger()

	tests := []struct {
		name     string
		username string
		password string
		authFunc AuthHandler
		wantErr  bool
	}{
		{
			name:     "successful authentication",
			username: "api@example.com",
			password: "api_key_token",
			authFunc: func(username, password string) (string, error) {
				if username == "api@example.com" && password == "api_key_token" {
					return "user123", nil
				}
				return "", smtp.ErrAuthFailed
			},
			wantErr: false,
		},
		{
			name:     "failed authentication",
			username: "api@example.com",
			password: "wrong_token",
			authFunc: func(username, password string) (string, error) {
				return "", smtp.ErrAuthFailed
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messageHandler := func(userID string, from string, to []string, data []byte) error {
				return nil
			}

			backend := NewBackend(tt.authFunc, messageHandler, log)
			session, _ := backend.NewSession(nil)

			err := session.(*Session).AuthPlain(tt.username, tt.password)
			if (err != nil) != tt.wantErr {
				t.Errorf("AuthPlain() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSession_Mail(t *testing.T) {
	log := logger.NewLogger()

	authHandler := func(username, password string) (string, error) {
		return "user123", nil
	}

	messageHandler := func(userID string, from string, to []string, data []byte) error {
		return nil
	}

	backend := NewBackend(authHandler, messageHandler, log)
	session, _ := backend.NewSession(nil)
	s := session.(*Session)

	// Should fail without authentication
	err := s.Mail("sender@example.com", nil)
	if err == nil {
		t.Error("Expected error when not authenticated, got nil")
	}

	// Authenticate
	_ = s.AuthPlain("api@example.com", "token")

	// Should succeed after authentication
	err = s.Mail("sender@example.com", nil)
	if err != nil {
		t.Errorf("Mail() failed after authentication: %v", err)
	}

	if s.from != "sender@example.com" {
		t.Errorf("Expected from to be 'sender@example.com', got '%s'", s.from)
	}
}

func TestSession_Rcpt(t *testing.T) {
	log := logger.NewLogger()

	authHandler := func(username, password string) (string, error) {
		return "user123", nil
	}

	messageHandler := func(userID string, from string, to []string, data []byte) error {
		return nil
	}

	backend := NewBackend(authHandler, messageHandler, log)
	session, _ := backend.NewSession(nil)
	s := session.(*Session)

	// Should fail without authentication
	err := s.Rcpt("recipient@example.com", nil)
	if err == nil {
		t.Error("Expected error when not authenticated, got nil")
	}

	// Authenticate
	_ = s.AuthPlain("api@example.com", "token")

	// Should succeed after authentication
	err = s.Rcpt("recipient1@example.com", nil)
	if err != nil {
		t.Errorf("Rcpt() failed after authentication: %v", err)
	}

	err = s.Rcpt("recipient2@example.com", nil)
	if err != nil {
		t.Errorf("Rcpt() failed for second recipient: %v", err)
	}

	if len(s.to) != 2 {
		t.Errorf("Expected 2 recipients, got %d", len(s.to))
	}
}

func TestSession_Data(t *testing.T) {
	log := logger.NewLogger()

	authHandler := func(username, password string) (string, error) {
		return "user123", nil
	}

	var capturedUserID string
	var capturedFrom string
	var capturedTo []string
	var capturedData []byte

	messageHandler := func(userID string, from string, to []string, data []byte) error {
		capturedUserID = userID
		capturedFrom = from
		capturedTo = to
		capturedData = data
		return nil
	}

	backend := NewBackend(authHandler, messageHandler, log)
	session, _ := backend.NewSession(nil)
	s := session.(*Session)

	// Authenticate and set up message
	_ = s.AuthPlain("api@example.com", "token")
	_ = s.Mail("sender@example.com", nil)
	_ = s.Rcpt("recipient@example.com", nil)

	// Send data
	messageData := []byte(`{"workspace_id": "workspace123", "notification": {"id": "test"}}`)
	reader := io.NopCloser(bytes.NewReader(messageData))

	err := s.Data(reader)
	if err != nil {
		t.Errorf("Data() failed: %v", err)
	}

	// Verify handler was called with correct parameters
	if capturedUserID != "user123" {
		t.Errorf("Expected user_id 'user123', got '%s'", capturedUserID)
	}

	if capturedFrom != "sender@example.com" {
		t.Errorf("Expected from 'sender@example.com', got '%s'", capturedFrom)
	}

	if len(capturedTo) != 1 || capturedTo[0] != "recipient@example.com" {
		t.Errorf("Expected to contain ['recipient@example.com'], got %v", capturedTo)
	}

	if !bytes.Equal(capturedData, messageData) {
		t.Errorf("Expected data to match, got different data")
	}
}

func TestSession_AuthMechanisms(t *testing.T) {
	// Test Session.AuthMechanisms - this was at 0% coverage
	log := logger.NewLogger()

	authHandler := func(username, password string) (string, error) {
		return "user123", nil
	}

	messageHandler := func(userID string, from string, to []string, data []byte) error {
		return nil
	}

	backend := NewBackend(authHandler, messageHandler, log)
	session, _ := backend.NewSession(nil)
	s := session.(*Session)

	mechanisms := s.AuthMechanisms()
	if len(mechanisms) != 1 {
		t.Errorf("Expected 1 auth mechanism, got %d", len(mechanisms))
	}
	if mechanisms[0] != "PLAIN" {
		t.Errorf("Expected 'PLAIN' mechanism, got '%s'", mechanisms[0])
	}
}

func TestSession_Auth(t *testing.T) {
	// Test Session.Auth - this was at 0% coverage
	log := logger.NewLogger()

	tests := []struct {
		name     string
		mech     string
		username string
		password string
		authFunc AuthHandler
		wantErr  bool
	}{
		{
			name:     "successful authentication with PLAIN",
			mech:     "PLAIN",
			username: "api@example.com",
			password: "api_key_token",
			authFunc: func(username, password string) (string, error) {
				if username == "api@example.com" && password == "api_key_token" {
					return "user123", nil
				}
				return "", smtp.ErrAuthFailed
			},
			wantErr: false,
		},
		{
			name:     "failed authentication",
			mech:     "PLAIN",
			username: "api@example.com",
			password: "wrong_token",
			authFunc: func(username, password string) (string, error) {
				return "", smtp.ErrAuthFailed
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messageHandler := func(userID string, from string, to []string, data []byte) error {
				return nil
			}

			backend := NewBackend(tt.authFunc, messageHandler, log)
			session, _ := backend.NewSession(nil)
			s := session.(*Session)

			saslServer, err := s.Auth(tt.mech)
			if err != nil {
				t.Fatalf("Auth() returned error: %v", err)
			}

			// Test the SASL server with credentials
			// Next returns (more bool, resp []byte, err error)
			_, _, authErr := saslServer.Next([]byte("\x00" + tt.username + "\x00" + tt.password))
			if (authErr != nil) != tt.wantErr {
				t.Errorf("SASL authentication error = %v, wantErr %v", authErr, tt.wantErr)
			}

			if !tt.wantErr && s.userID == "" {
				t.Error("Expected userID to be set after successful authentication")
			}
		})
	}
}

func TestSession_Reset(t *testing.T) {
	log := logger.NewLogger()

	authHandler := func(username, password string) (string, error) {
		return "user123", nil
	}

	messageHandler := func(userID string, from string, to []string, data []byte) error {
		return nil
	}

	backend := NewBackend(authHandler, messageHandler, log)
	session, _ := backend.NewSession(nil)
	s := session.(*Session)

	// Set up some state
	_ = s.AuthPlain("api@example.com", "token")
	_ = s.Mail("sender@example.com", nil)
	_ = s.Rcpt("recipient@example.com", nil)

	// Reset
	s.Reset()

	// Verify state was cleared
	if s.from != "" {
		t.Errorf("Expected from to be empty after Reset, got '%s'", s.from)
	}

	if len(s.to) != 0 {
		t.Errorf("Expected to to be empty after Reset, got %v", s.to)
	}

	// userID should still be set
	if s.userID != "user123" {
		t.Errorf("Expected userID to remain 'user123', got '%s'", s.userID)
	}
}

func TestSession_Logout(t *testing.T) {
	log := logger.NewLogger()

	authHandler := func(username, password string) (string, error) {
		return "user123", nil
	}

	messageHandler := func(userID string, from string, to []string, data []byte) error {
		return nil
	}

	backend := NewBackend(authHandler, messageHandler, log)
	session, _ := backend.NewSession(nil)

	err := session.Logout()
	if err != nil {
		t.Errorf("Logout() returned error: %v", err)
	}
}

func TestSession_DataWithoutAuth(t *testing.T) {
	log := logger.NewLogger()

	authHandler := func(username, password string) (string, error) {
		return "user123", nil
	}

	messageHandler := func(userID string, from string, to []string, data []byte) error {
		return nil
	}

	backend := NewBackend(authHandler, messageHandler, log)
	session, _ := backend.NewSession(nil)
	s := session.(*Session)

	// Try to send data without authentication
	messageData := []byte(`test`)
	reader := io.NopCloser(strings.NewReader(string(messageData)))

	err := s.Data(reader)
	if err == nil {
		t.Error("Expected error when calling Data() without authentication, got nil")
	}

	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("Expected 'not authenticated' error, got: %v", err)
	}
}

// TestBackend_SetRcptChecker : deux RCPT dont un refusé au RCPT TO. Le refusé
// reçoit l'erreur du garde, l'autre passe ; sans garde, tout passe (legacy).
func TestBackend_SetRcptChecker(t *testing.T) {
	log := logger.NewLogger()
	auth := func(username, password string) (string, error) { return "user123", nil }
	msg := func(userID string, from string, to []string, data []byte) error { return nil }

	t.Run("refuse le destinataire supprime, accepte l'autre", func(t *testing.T) {
		backend := NewBackend(auth, msg, log)
		backend.SetRcptChecker(func(userID, to string) error {
			if to == "bounced@example.com" {
				return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "suppressed"}
			}
			return nil
		})
		session, err := backend.NewSession(nil)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		s := session.(*Session)
		if err := s.AuthPlain("api@example.com", "key"); err != nil {
			t.Fatalf("AuthPlain: %v", err)
		}
		if err := s.Mail("from@example.com", nil); err != nil {
			t.Fatalf("Mail: %v", err)
		}
		if err := s.Rcpt("bounced@example.com", nil); err == nil {
			t.Fatal("Rcpt(bounced) accepte : le garde n'a pas refusé")
		} else if smtpErr, ok := err.(*smtp.SMTPError); !ok || smtpErr.Code != 550 {
			t.Fatalf("Rcpt(bounced) doit rendre un SMTPError 550, got %v", err)
		}
		if err := s.Rcpt("ok@example.com", nil); err != nil {
			t.Fatalf("Rcpt(ok) refusé à tort: %v", err)
		}
		if len(s.to) != 1 || s.to[0] != "ok@example.com" {
			t.Fatalf("seul ok@example.com doit être retenu, got %v", s.to)
		}
	})

	t.Run("sans garde, tout destinataire passe", func(t *testing.T) {
		backend := NewBackend(auth, msg, log)
		session, _ := backend.NewSession(nil)
		s := session.(*Session)
		_ = s.AuthPlain("api@example.com", "key")
		if err := s.Rcpt("bounced@example.com", nil); err != nil {
			t.Fatalf("sans garde, Rcpt doit passer: %v", err)
		}
	})
}
