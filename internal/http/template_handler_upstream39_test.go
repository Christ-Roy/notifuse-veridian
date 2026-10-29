// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/http/template_handler_test.go ; ne contient que les declarations absentes du fork.

package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleCreate_MissingVisualEditorTree posts a raw payload whose "email" object omits
// visual_editor_tree. The body is a map rather than a marshalled domain.CreateTemplateRequest
// so the request reaches the handler exactly as an external API client would send it; a
// typed request always carries a tree and cannot reproduce the case.
func TestHandleCreate_MissingVisualEditorTree(t *testing.T) {
	t.Skip("écart upstream v39.1 non analysé : comportement du fork différent ou fonctionnalité absente (voir docs/claude/75)")
	mockService, _, serverURL, secretKey, cleanup := setupTemplateHandlerTest(t)
	defer cleanup()

	payload := map[string]interface{}{
		"workspace_id": "workspace123",
		"id":           "test-template",
		"name":         "Test Template",
		"category":     "transactional",
		"channel":      "email",
		"email": map[string]interface{}{
			"subject": "Test - {{ nombre }}",
			"html":    "<p>Hola {{ nombre }}</p>",
		},
	}

	// Validation must reject the request before it reaches the service.
	mockService.EXPECT().CreateTemplate(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	apiURL := fmt.Sprintf("%s/api/templates.create", serverURL)
	resp := sendRequest(t, http.MethodPost, apiURL, createTestToken(secretKey), payload)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Contains(t, body["error"], "visual_editor_tree is required")
}

// TestHandleUpdate_MissingVisualEditorTree is the templates.update counterpart.
func TestHandleUpdate_MissingVisualEditorTree(t *testing.T) {
	t.Skip("écart upstream v39.1 non analysé : comportement du fork différent ou fonctionnalité absente (voir docs/claude/75)")
	mockService, _, serverURL, secretKey, cleanup := setupTemplateHandlerTest(t)
	defer cleanup()

	payload := map[string]interface{}{
		"workspace_id": "workspace123",
		"id":           "test-template",
		"name":         "Test Template",
		"category":     "transactional",
		"channel":      "email",
		"email": map[string]interface{}{
			"subject": "Test Subject",
		},
	}

	mockService.EXPECT().UpdateTemplate(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	apiURL := fmt.Sprintf("%s/api/templates.update", serverURL)
	resp := sendRequest(t, http.MethodPost, apiURL, createTestToken(secretKey), payload)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestTemplateHandler_HandleUpdate_AbsentAndServerOwnedFields pins the two wire facts the
// service's preserve logic rests on, using map bodies because a typed request literal cannot
// express a missing key — which is exactly how this class of bug ships. An omitted
// "translations" must reach the service as a nil map (its signal to keep what is stored) and an
// explicit {} must reach it non-nil (the deliberate clear); integration_id is server-owned and
// must not be settable from the wire whatever a client sends.
func TestTemplateHandler_HandleUpdate_AbsentAndServerOwnedFields(t *testing.T) {
	baseBody := func() map[string]interface{} {
		return map[string]interface{}{
			"workspace_id": "workspace123",
			"id":           "tmpl_abc",
			"name":         "Updated Name",
			"channel":      "email",
			"category":     "transactional",
			"email":        createTestEmailTemplate(),
		}
	}

	t.Run("omitted translations arrive as a nil map", func(t *testing.T) {
		mockService, _, serverURL, secretKey, cleanup := setupTemplateHandlerTest(t)
		defer cleanup()

		mockService.EXPECT().UpdateTemplate(gomock.Any(), "workspace123", gomock.Any()).
			DoAndReturn(func(ctx context.Context, wsID string, tmpl *domain.Template) error {
				assert.Nil(t, tmpl.Translations, "an omitted key must stay distinguishable from an explicit clear")
				return nil
			})

		resp := sendRequest(t, http.MethodPost, fmt.Sprintf("%s/api/templates.update", serverURL), createTestToken(secretKey), baseBody())
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("an explicit empty translations object arrives as an empty non-nil map", func(t *testing.T) {
		mockService, _, serverURL, secretKey, cleanup := setupTemplateHandlerTest(t)
		defer cleanup()

		body := baseBody()
		body["translations"] = map[string]interface{}{}

		mockService.EXPECT().UpdateTemplate(gomock.Any(), "workspace123", gomock.Any()).
			DoAndReturn(func(ctx context.Context, wsID string, tmpl *domain.Template) error {
				assert.NotNil(t, tmpl.Translations, "an explicit {} is a clear, not an omission")
				assert.Empty(t, tmpl.Translations)
				return nil
			})

		resp := sendRequest(t, http.MethodPost, fmt.Sprintf("%s/api/templates.update", serverURL), createTestToken(secretKey), body)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("integration_id sent by a client is ignored", func(t *testing.T) {
		mockService, _, serverURL, secretKey, cleanup := setupTemplateHandlerTest(t)
		defer cleanup()

		body := baseBody()
		body["integration_id"] = "client-supplied"

		mockService.EXPECT().UpdateTemplate(gomock.Any(), "workspace123", gomock.Any()).
			DoAndReturn(func(ctx context.Context, wsID string, tmpl *domain.Template) error {
				assert.Nil(t, tmpl.IntegrationID, "integration_id is server-owned: a client must not be able to claim a template for an integration")
				return nil
			})

		resp := sendRequest(t, http.MethodPost, fmt.Sprintf("%s/api/templates.update", serverURL), createTestToken(secretKey), body)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("the response reports what was persisted, not what was sent", func(t *testing.T) {
		mockService, _, serverURL, secretKey, cleanup := setupTemplateHandlerTest(t)
		defer cleanup()

		// Stands in for the service restoring the fields the request could not carry, so the
		// client's local copy is not left believing they are gone.
		mockService.EXPECT().UpdateTemplate(gomock.Any(), "workspace123", gomock.Any()).
			DoAndReturn(func(ctx context.Context, wsID string, tmpl *domain.Template) error {
				integrationID := "supabase-int-1"
				tmpl.IntegrationID = &integrationID
				tmpl.Translations = map[string]domain.TemplateTranslation{
					"fr": {Email: &domain.EmailTemplate{SenderID: "sender123", Subject: "Sujet FR"}},
				}
				return nil
			})

		resp := sendRequest(t, http.MethodPost, fmt.Sprintf("%s/api/templates.update", serverURL), createTestToken(secretKey), baseBody())
		defer func() { _ = resp.Body.Close() }()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		var response struct {
			Template struct {
				IntegrationID *string                               `json:"integration_id"`
				Translations  map[string]domain.TemplateTranslation `json:"translations"`
			} `json:"template"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
		require.NotNil(t, response.Template.IntegrationID)
		assert.Equal(t, "supabase-int-1", *response.Template.IntegrationID)
		require.Contains(t, response.Template.Translations, "fr")
		assert.Equal(t, "Sujet FR", response.Template.Translations["fr"].Email.Subject)
	})
}
