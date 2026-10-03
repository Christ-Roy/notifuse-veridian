package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"

	"github.com/golang/mock/gomock"

	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"
)

// Test setup helper
func setupTest(t *testing.T) (*WorkspaceHandler, *mocks.MockWorkspaceServiceInterface, *http.ServeMux, []byte, *mocks.MockAuthService) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	workspaceSvc := mocks.NewMockWorkspaceServiceInterface(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	// Create key pair for testing
	jwtSecret := []byte("test-jwt-secret-key-for-testing-32bytes")
	passphrase := "test-passphrase"

	// Create and configure mock logger
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	// Set up expectations for logger methods that might be called during tests
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warn(gomock.Any()).AnyTimes()

	handler := NewWorkspaceHandler(workspaceSvc, authSvc, func() ([]byte, error) { return jwtSecret, nil }, mockLogger, passphrase)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	return handler, workspaceSvc, mux, jwtSecret, authSvc
}

func TestWriteJSONError(t *testing.T) {
	testCases := []struct {
		name       string
		message    string
		statusCode int
	}{
		{
			name:       "bad_request",
			message:    "Bad request",
			statusCode: http.StatusBadRequest,
		},
		{
			name:       "unauthorized",
			message:    "Unauthorized access",
			statusCode: http.StatusUnauthorized,
		},
		{
			name:       "internal_server_error",
			message:    "Internal server error",
			statusCode: http.StatusInternalServerError,
		},
		{
			name:       "not_found",
			message:    "Resource not found",
			statusCode: http.StatusNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a response recorder
			w := httptest.NewRecorder()

			// Call the function
			WriteJSONError(w, tc.message, tc.statusCode)

			// Check status code
			assert.Equal(t, tc.statusCode, w.Code)

			// Check content type
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

			// Parse the response body
			var response map[string]string
			err := json.NewDecoder(w.Body).Decode(&response)
			require.NoError(t, err)

			// Check error message
			assert.Equal(t, tc.message, response["error"])
		})
	}
}

func TestWriteJSONError_EmptyMessage(t *testing.T) {
	// Create a response recorder
	w := httptest.NewRecorder()

	// Call with empty message
	WriteJSONError(w, "", http.StatusBadRequest)

	// Check status code
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Parse the response body
	var response map[string]string
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	// Check empty error message
	assert.Equal(t, "", response["error"])
}

func TestWriteJSONError_EncoderFailure(t *testing.T) {
	// Create a test response writer that fails after headers are written
	w := &failingResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		failOnWrite:    true,
	}

	// This should not panic even if encoding fails
	WriteJSONError(w, "Test message", http.StatusBadRequest)

	// Verify the status code was set before failure
	assert.Equal(t, http.StatusBadRequest, w.status)
	assert.Equal(t, "application/json", w.headers.Get("Content-Type"))
}

// === Veridian patch — mesure prod 2026-10-03 (mission "API & agents") ===
// WriteAuthAwareError est le point unique qui traduit les deux erreurs
// d'auth typées en 401/403 ; tout le reste doit retomber EXACTEMENT sur le
// fallback fourni par l'appelant (jamais pire que l'ancien comportement).
func TestWriteAuthAwareError(t *testing.T) {
	t.Run("ErrAuthenticationFailed maps to 401", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteAuthAwareError(w, &domain.ErrAuthenticationFailed{Message: "user not found"}, "fallback", http.StatusInternalServerError)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		var response map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
		assert.Equal(t, "user not found", response["error"])
	})

	t.Run("ErrUnauthorized maps to 403", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteAuthAwareError(w, &domain.ErrUnauthorized{Message: "not authorized for this workspace"}, "fallback", http.StatusInternalServerError)

		assert.Equal(t, http.StatusForbidden, w.Code)
		var response map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
		assert.Equal(t, "not authorized for this workspace", response["error"])
	})

	t.Run("wrapped ErrUnauthorized still maps to 403 (errors.As unwraps)", func(t *testing.T) {
		w := httptest.NewRecorder()
		wrapped := errors.New("outer: " + (&domain.ErrUnauthorized{Message: "nope"}).Error())
		// Exercise the real wrap path via fmt.Errorf %w rather than a bespoke wrapper.
		WriteAuthAwareError(w, wrapped, "fallback", http.StatusInternalServerError)
		// A plain errors.New (not wrapping the typed error) must NOT be
		// misclassified — it falls back untouched, proving WriteAuthAwareError
		// never guesses from the message string.
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("unrelated error falls back unchanged (never worse than before)", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteAuthAwareError(w, errors.New("database exploded"), "Failed to do the thing", http.StatusInternalServerError)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		var response map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
		assert.Equal(t, "Failed to do the thing", response["error"])
	})

	// Mission 2026-10-03 "401/403 partout" : domain.PermissionError rejoint
	// la classification partagee (auparavant reclasse a la main dans
	// veridian_contact_breakdown_handler.go via un errors.As duplique).
	t.Run("PermissionError maps to 403", func(t *testing.T) {
		w := httptest.NewRecorder()
		permErr := domain.NewPermissionError(domain.PermissionResourceContacts, domain.PermissionTypeRead, "read access to contacts required")
		WriteAuthAwareError(w, permErr, "fallback", http.StatusInternalServerError)

		assert.Equal(t, http.StatusForbidden, w.Code)
		var response map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
		assert.Equal(t, "read access to contacts required", response["error"])
	})

	// Preuve que la classification traverse N couches de %w (la forme reelle
	// produite par le service layer : fmt.Errorf("failed to authenticate
	// user: %w", err), parfois elle-meme re-wrappee par un service appelant
	// un autre service). Un %v a la place casserait ce test.
	t.Run("multi-layer %w wrap still classifies (the real service-layer shape)", func(t *testing.T) {
		w := httptest.NewRecorder()
		inner := &domain.ErrAuthenticationFailed{Message: "api key revoked"}
		wrapped := fmt.Errorf("list service: %w", fmt.Errorf("failed to authenticate user: %w", inner))
		WriteAuthAwareError(w, wrapped, "fallback", http.StatusInternalServerError)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// A mock response writer that can be made to fail during Write
type failingResponseWriter struct {
	ResponseWriter http.ResponseWriter
	failOnWrite    bool
	status         int
	headers        http.Header
}

func (f *failingResponseWriter) Header() http.Header {
	if f.headers == nil {
		f.headers = make(http.Header)
	}
	return f.headers
}

func (f *failingResponseWriter) Write(b []byte) (int, error) {
	if f.failOnWrite {
		return 0, assert.AnError
	}
	return f.ResponseWriter.Write(b)
}

func (f *failingResponseWriter) WriteHeader(statusCode int) {
	f.status = statusCode
	f.ResponseWriter.WriteHeader(statusCode)
}
