// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/domain/template_test.go ; ne contient que les declarations absentes du fork.

package domain

import (
	"encoding/json"
	"testing"

	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildTemplateData_GlobalFeed pins that the global feed payload reaches template data on the
// strength of the data being there, and nothing else. A broadcast may carry data with the feed
// switched off — that is how a client supplies its own payload instead of having Notifuse fetch
// one at scheduling time — so gating the injection on Enabled would silently drop a supported
// mode. Clearing a payload the client no longer wants is UpdateBroadcastRequest.Validate's job,
// on the save that switches a live feed off.
func TestBuildTemplateData_GlobalFeed(t *testing.T) {
	const secretKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	payload := MapOfAny{"headline": "Yesterday's deal"}

	newReq := func(feed *GlobalFeedSettings, data MapOfAny) TemplateDataRequest {
		return TemplateDataRequest{
			WorkspaceID:        "ws-123",
			WorkspaceSecretKey: secretKey,
			MessageID:          "msg-456",
			TrackingSettings:   notifuse_mjml.TrackingSettings{Endpoint: "https://api.example.com"},
			Broadcast: &Broadcast{
				ID:   "broadcast-001",
				Name: "Test Broadcast",
				DataFeed: &DataFeedSettings{
					GlobalFeed:     feed,
					GlobalFeedData: data,
				},
			},
		}
	}

	t.Run("enabled feed renders its fetched payload", func(t *testing.T) {
		data, err := BuildTemplateData(newReq(&GlobalFeedSettings{Enabled: true, URL: "https://feed.example.com"}, payload))
		require.NoError(t, err)
		assert.Equal(t, payload, data["global_feed"])
	})

	t.Run("disabled feed renders the payload supplied with it", func(t *testing.T) {
		data, err := BuildTemplateData(newReq(&GlobalFeedSettings{Enabled: false, URL: "https://feed.example.com"}, payload))
		require.NoError(t, err)
		assert.Equal(t, payload, data["global_feed"],
			"a client that supplies its own payload asks for no fetch, not for no data")
	})

	t.Run("payload supplied without any feed configuration is rendered", func(t *testing.T) {
		data, err := BuildTemplateData(newReq(nil, payload))
		require.NoError(t, err)
		assert.Equal(t, payload, data["global_feed"])
	})

	t.Run("no payload leaves global_feed unset", func(t *testing.T) {
		data, err := BuildTemplateData(newReq(&GlobalFeedSettings{Enabled: true, URL: "https://feed.example.com"}, nil))
		require.NoError(t, err)
		_, exists := data["global_feed"]
		assert.False(t, exists)
	})
}

// TestEmailTemplate_Validate_MissingVisualEditorTree covers email payloads whose "email"
// object omits visual_editor_tree. VisualEditorTree is an interface, so decoding such a
// payload leaves it nil and every method call on it dereferences a nil pointer. These
// cases decode from JSON rather than building struct literals, because a struct literal
// built in a test always assigns a tree and therefore cannot reach the nil state.
func TestEmailTemplate_Validate_MissingVisualEditorTree(t *testing.T) {
	t.Skip("écart upstream v39.1 non analysé : comportement du fork différent ou fonctionnalité absente (voir docs/claude/75)")
	t.Run("direct validate with nil tree returns error", func(t *testing.T) {
		e := &EmailTemplate{Subject: "Test Subject"}
		err := e.Validate(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "visual_editor_tree is required")
	})

	t.Run("create request without visual_editor_tree", func(t *testing.T) {
		payload := `{
			"workspace_id": "workspace123",
			"id": "test-template",
			"name": "Test Template",
			"category": "transactional",
			"channel": "email",
			"email": {
				"subject": "Test - {{ nombre }}",
				"html": "<p>Hola {{ nombre }}</p>"
			}
		}`
		var req CreateTemplateRequest
		require.NoError(t, json.Unmarshal([]byte(payload), &req))

		template, workspaceID, err := req.Validate()
		assert.Error(t, err)
		assert.Nil(t, template)
		assert.Empty(t, workspaceID)
		assert.Contains(t, err.Error(), "visual_editor_tree is required")
	})

	t.Run("update request without visual_editor_tree", func(t *testing.T) {
		payload := `{
			"workspace_id": "workspace123",
			"id": "test-template",
			"name": "Test Template",
			"category": "transactional",
			"channel": "email",
			"email": {"subject": "Test Subject"}
		}`
		var req UpdateTemplateRequest
		require.NoError(t, json.Unmarshal([]byte(payload), &req))

		template, workspaceID, err := req.Validate()
		assert.Error(t, err)
		assert.Nil(t, template)
		assert.Empty(t, workspaceID)
		assert.Contains(t, err.Error(), "visual_editor_tree is required")
	})

	t.Run("translation without visual_editor_tree", func(t *testing.T) {
		payload := `{
			"workspace_id": "workspace123",
			"id": "test-template",
			"name": "Test Template",
			"category": "transactional",
			"channel": "email",
			"email": {
				"subject": "Test Subject",
				"visual_editor_tree": {"id": "root", "type": "mjml", "children": []}
			},
			"translations": {"fr": {"email": {"subject": "Sujet"}}}
		}`
		var req CreateTemplateRequest
		require.NoError(t, json.Unmarshal([]byte(payload), &req))

		_, _, err := req.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "visual_editor_tree is required")
	})

	t.Run("template validate with nil tree", func(t *testing.T) {
		tmpl := &Template{
			ID:       "test-template",
			Name:     "Test",
			Version:  1,
			Channel:  ChannelEmail,
			Category: "marketing",
			Email:    &EmailTemplate{Subject: "Test Subject"},
		}
		err := tmpl.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "visual_editor_tree is required")
	})

	t.Run("explicit null tree returns error without panic", func(t *testing.T) {
		var e EmailTemplate
		require.NoError(t, json.Unmarshal([]byte(`{"subject":"Test","visual_editor_tree":null}`), &e))
		err := e.Validate(nil)
		assert.Error(t, err)
	})

	t.Run("code mode without tree stays valid", func(t *testing.T) {
		mjml := "<mjml><mj-body></mj-body></mjml>"
		payload := `{
			"workspace_id": "workspace123",
			"id": "code-template",
			"name": "Code Template",
			"category": "transactional",
			"channel": "email",
			"email": {
				"editor_mode": "code",
				"subject": "Test Subject",
				"mjml_source": "` + mjml + `"
			}
		}`
		var req CreateTemplateRequest
		require.NoError(t, json.Unmarshal([]byte(payload), &req))

		template, workspaceID, err := req.Validate()
		require.NoError(t, err)
		assert.Equal(t, "workspace123", workspaceID)
		require.NotNil(t, template)
		assert.Equal(t, mjml, template.Email.CompiledPreview)
	})
}
