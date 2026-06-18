package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/analytics"
	"github.com/Notifuse/notifuse/pkg/logger"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const veridianAnalyticsTestJWT = "test-jwt-secret-key-for-testing-32bytes"

func newVeridianAnalyticsHandlerForTest(t *testing.T) (*VeridianAnalyticsHandler, *mocks.MockAnalyticsService) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockService := mocks.NewMockAnalyticsService(ctrl)
	h := NewVeridianAnalyticsHandler(
		mockService,
		func() ([]byte, error) { return []byte(veridianAnalyticsTestJWT), nil },
		logger.NewLogger(),
	)
	return h, mockService
}

func TestNewVeridianAnalyticsHandler(t *testing.T) {
	h, _ := newVeridianAnalyticsHandlerForTest(t)
	assert.NotNil(t, h)
	assert.IsType(t, &VeridianAnalyticsHandler{}, h)
}

func TestVeridianAnalyticsHandler_RegisterRoutes(t *testing.T) {
	h, _ := newVeridianAnalyticsHandlerForTest(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Les routes POST doivent exister : une requête sans JWT atteint le
	// middleware auth → 401 (PAS 404 = route absente).
	for _, path := range []string{"/api/analytics.query", "/api/analytics.schemas"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "route %s should be registered (401 not 404)", path)
	}
}

// assertStandardErrorShape vérifie le coeur du ticket : sur erreur, le corps est
// {"error": "<string lisible>"} et PAS {"error": true} (booléen upstream).
func assertStandardErrorShape(t *testing.T, body []byte, wantSubstring string) {
	t.Helper()
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &raw))

	rawError, ok := raw["error"]
	require.True(t, ok, "réponse d'erreur doit contenir le champ 'error'")

	// Le champ error DOIT être une string, pas un booléen.
	errStr, isString := rawError.(string)
	require.Truef(t, isString, "le champ 'error' doit être une string, pas %T (=%v) — forme upstream non-standard interdite", rawError, rawError)
	assert.NotEqual(t, "true", errStr, "le message d'erreur ne doit pas être la chaîne 'true' (régression de la forme booléenne)")
	if wantSubstring != "" {
		assert.Contains(t, errStr, wantSubstring)
	}

	// La forme standard {"error": "<string>"} de WriteJSONError n'émet PAS de
	// champ "message" séparé (contrairement à l'upstream {"error":true,"message":...}).
	_, hasMessage := raw["message"]
	assert.False(t, hasMessage, "la forme standard ne doit pas avoir de champ 'message' séparé")
}

func TestVeridianAnalyticsHandler_handleQuery(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    interface{}
		setupMocks     func(*mocks.MockAnalyticsService)
		expectedStatus int
		expectedErrSub string // si non vide : on attend une erreur standard contenant ce substr
		checkSuccess   func(*testing.T, map[string]interface{})
	}{
		{
			name: "successful query — forme 200 inchangée (Response brut)",
			requestBody: AnalyticsQueryRequest{
				WorkspaceID: "test-workspace",
				Query: analytics.Query{
					Schema:   "message_history",
					Measures: []string{"count"},
				},
			},
			setupMocks: func(m *mocks.MockAnalyticsService) {
				m.EXPECT().Query(gomock.Any(), "test-workspace", gomock.Any()).
					Return(&analytics.Response{
						Data: []map[string]interface{}{{"count": 42}},
						Meta: analytics.Meta{Query: "SELECT ...", Params: []interface{}{}},
					}, nil)
			},
			expectedStatus: http.StatusOK,
			checkSuccess: func(t *testing.T, resp map[string]interface{}) {
				// Le succès doit rester le *analytics.Response brut (data + meta au
				// top-level), EXACTEMENT comme l'upstream — le front lit ça direct.
				assert.Contains(t, resp, "data")
				assert.Contains(t, resp, "meta")
				data := resp["data"].([]interface{})
				require.Len(t, data, 1)
				assert.Equal(t, float64(42), data[0].(map[string]interface{})["count"])
			},
		},
		{
			name:           "invalid JSON body → erreur standard string",
			requestBody:    "{not-json",
			setupMocks:     func(m *mocks.MockAnalyticsService) {},
			expectedStatus: http.StatusBadRequest,
			expectedErrSub: "Invalid request payload",
		},
		{
			name: "missing workspace_id → erreur standard string",
			requestBody: AnalyticsQueryRequest{
				Query: analytics.Query{Schema: "message_history", Measures: []string{"count"}},
			},
			setupMocks:     func(m *mocks.MockAnalyticsService) {},
			expectedStatus: http.StatusBadRequest,
			expectedErrSub: "workspace_id is required",
		},
		{
			name: "service error → erreur standard string (pas {error:true})",
			requestBody: AnalyticsQueryRequest{
				WorkspaceID: "test-workspace",
				Query:       analytics.Query{Schema: "message_history", Measures: []string{"count"}},
			},
			setupMocks: func(m *mocks.MockAnalyticsService) {
				m.EXPECT().Query(gomock.Any(), "test-workspace", gomock.Any()).
					Return((*analytics.Response)(nil), assert.AnError)
			},
			expectedStatus: http.StatusInternalServerError,
			expectedErrSub: "Query failed:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mockService := newVeridianAnalyticsHandlerForTest(t)
			tt.setupMocks(mockService)

			var body []byte
			switch v := tt.requestBody.(type) {
			case string:
				body = []byte(v)
			default:
				b, err := json.Marshal(v)
				require.NoError(t, err)
				body = b
			}

			req := httptest.NewRequest(http.MethodPost, "/api/analytics.query", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.handleQuery(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedErrSub != "" {
				assertStandardErrorShape(t, w.Body.Bytes(), tt.expectedErrSub)
				return
			}
			var resp map[string]interface{}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			if tt.checkSuccess != nil {
				tt.checkSuccess(t, resp)
			}
		})
	}
}

func TestVeridianAnalyticsHandler_handleGetSchemas(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    interface{}
		setupMocks     func(*mocks.MockAnalyticsService)
		expectedStatus int
		expectedErrSub string
		checkSuccess   func(*testing.T, map[string]interface{})
	}{
		{
			name:        "successful schemas — forme 200 inchangée ({schemas:...})",
			requestBody: AnalyticsSchemasRequest{WorkspaceID: "test-workspace"},
			setupMocks: func(m *mocks.MockAnalyticsService) {
				m.EXPECT().GetSchemas(gomock.Any(), "test-workspace").
					Return(map[string]analytics.SchemaDefinition{
						"message_history": {Name: "message_history"},
					}, nil)
			},
			expectedStatus: http.StatusOK,
			checkSuccess: func(t *testing.T, resp map[string]interface{}) {
				assert.Contains(t, resp, "schemas")
				schemas := resp["schemas"].(map[string]interface{})
				assert.Contains(t, schemas, "message_history")
			},
		},
		{
			name:           "missing workspace_id → erreur standard string",
			requestBody:    AnalyticsSchemasRequest{},
			setupMocks:     func(m *mocks.MockAnalyticsService) {},
			expectedStatus: http.StatusBadRequest,
			expectedErrSub: "workspace_id is required",
		},
		{
			name:        "service error → erreur standard string (pas {error:true})",
			requestBody: AnalyticsSchemasRequest{WorkspaceID: "test-workspace"},
			setupMocks: func(m *mocks.MockAnalyticsService) {
				m.EXPECT().GetSchemas(gomock.Any(), "test-workspace").
					Return(nil, assert.AnError)
			},
			expectedStatus: http.StatusInternalServerError,
			expectedErrSub: "Failed to get schemas:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mockService := newVeridianAnalyticsHandlerForTest(t)
			tt.setupMocks(mockService)

			body, err := json.Marshal(tt.requestBody)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/api/analytics.schemas", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.handleGetSchemas(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedErrSub != "" {
				assertStandardErrorShape(t, w.Body.Bytes(), tt.expectedErrSub)
				return
			}
			var resp map[string]interface{}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			if tt.checkSuccess != nil {
				tt.checkSuccess(t, resp)
			}
		})
	}
}
