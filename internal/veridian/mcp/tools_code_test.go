package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Le code mode (VER-253) : ce qui reste dehors, par chemin. Une garde qui ne voit rien ne garde rien : elle s'éprouve d'abord.
func TestCatalog_CeQuiResteDehors(t *testing.T) {
	out := regexp.MustCompile(`(?i)apikey|members?|invit|settings|integrations|webhooksubscriptions|webhooks\.|mail\.access|workspaces|tenants|/hub/|policy\.(set|reset|priority)|emailProfiles\.(pause|resume|create|setUsage)|mail\.messages\.(send|reply|raw|attachment)|automations\.(enroll$|resetContact)|broadcasts\.(schedule|resume|sendToIndividual|selectWinner)|email\.test|queue\.cancel`)
	for _, p := range []string{"/api/workspaces.createAPIKey", "/api/webhookSubscriptions.create", "/api/veridian/mail.messages.send", "/api/broadcasts.schedule", "/api/automations.enroll"} {
		require.True(t, out.MatchString(p), p)
	}
	for _, tl := range catalog {
		assert.False(t, out.MatchString(tl.Path), "%s : %s", tl.Name, tl.Path)
	}
}

// readOnlyHint dit vrai : un outil de lecture n'appelle jamais une route qui écrit, et un outil qui écrit ne se dit pas lecture.
func TestCatalog_AnnotationsVraies(t *testing.T) {
	writes := regexp.MustCompile(`(?i)\.(create|update|updateStatus|delete|removeContact|upsert|import|intake|send|activate|pause|rebuild|exitContact)$|reservoir\.activate$`)
	for _, tl := range catalog {
		assert.Equal(t, tl.Write, writes.MatchString(tl.Path), "%s : %s", tl.Name, tl.Path)
		if !tl.Write {
			assert.Nil(t, tl.Guard, tl.Name)
		}
		for _, r := range tl.Required {
			assert.Contains(t, tl.Props, r, "%s exige un argument absent du schéma", tl.Name)
		}
	}
	for _, n := range []string{"segments_update", "templates_update", "contact_unsubscribe", "segments_delete", "templates_delete", "broadcasts_delete", "automations_delete", "lists_delete", "list_remove_contact"} {
		tl, ok := findTool(n)
		require.True(t, ok, n)
		assert.True(t, tl.Destructive, n)
	}
	assert.GreaterOrEqual(t, len(catalog), 70)
}

func TestAutomations_ToujoursEnBrouillonEtSansWebhook(t *testing.T) {
	for _, name := range []string{"automations_create", "automations_update"} {
		tl, _ := findTool(name)
		_, body, err := tl.request(map[string]any{"automation": map[string]any{"id": "a1", "name": "A", "status": "live", "Status": "live", "trigger_sql": "X", "Trigger_SQL": "DROP", "workspace_id": "AUTRE", "nodes": []any{map[string]any{"id": "n", "type": "email"}}}}, "ws1")
		require.NoError(t, err)
		assert.JSONEq(t, `{"workspace_id":"ws1","automation":{"id":"a1","name":"A","status":"draft","nodes":[{"id":"n","type":"email"}]}}`, string(body))
		var got domain.CreateAutomationRequest
		require.NoError(t, json.Unmarshal(body, &got))
		assert.Equal(t, domain.AutomationStatusDraft, got.Automation.Status)
		for _, typ := range []map[string]any{{"type": "webhook"}, {"Type": "webhook"}, {"type": "email", "TYPE": " Webhook "}} {
			_, _, err = tl.request(map[string]any{"automation": map[string]any{"id": "a1", "nodes": []any{typ}}}, "ws1")
			assert.ErrorContains(t, err, "webhook")
		}
		_, _, err = tl.request(map[string]any{"automation": "texte"}, "ws1")
		assert.Error(t, err)
	}
}

func TestContactUnsubscribe_NeReinscritJamais(t *testing.T) {
	tl, _ := findTool("contact_unsubscribe")
	_, body, err := tl.request(map[string]any{"email": "a@b.fr", "list_id": "l", "status": "active"}, "ws1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"email":"a@b.fr","list_id":"l","status":"unsubscribed","workspace_id":"ws1"}`, string(body))
}

func TestTemplates_JamaisTransactionnel(t *testing.T) {
	args := func(cat string) map[string]any {
		return map[string]any{"id": "m", "name": "M", "channel": "email", "category": cat, "email": map[string]any{}}
	}
	create, _ := findTool("templates_create")
	_, _, err := create.request(args("transactional"), "ws1")
	assert.ErrorContains(t, err, "transactionnel")
	_, _, err = create.request(args("marketing"), "ws1")
	assert.NoError(t, err)
	get := func(cat string) getter {
		return func(string, url.Values) ([]byte, error) {
			return []byte(`{"template":{"category":"` + cat + `"}}`), nil
		}
	}
	assert.Error(t, sameTemplateNature(get("marketing"), args("transactional")))
	assert.Error(t, sameTemplateNature(get("transactional"), args("marketing")))
	assert.NoError(t, sameTemplateNature(get("transactional"), args("transactional")))
	assert.NoError(t, sameTemplateNature(get("marketing"), args("welcome")))
}

// Une vérification illisible ou impossible refuse : jamais d'écriture à l'aveugle.
func TestChecks_EtatReel(t *testing.T) {
	fixed := func(body string) getter { return func(string, url.Values) ([]byte, error) { return []byte(body), nil } }
	down := func(string, url.Values) ([]byte, error) { return nil, errors.New("panne") }
	for name, c := range map[string]struct {
		check    func(getter, map[string]any) error
		args     map[string]any
		ok, stop string
	}{
		"campagne":  {draftBroadcast, map[string]any{"id": "b"}, `{"broadcast":{"status":"draft"}}`, `{"broadcast":{"status":"scheduled"}}`},
		"sequence":  {draftAutomation, map[string]any{"automation": map[string]any{"id": "a"}}, `{"automation":{"status":"draft"}}`, `{"automation":{"status":"live"}}`},
		"supprimer": {stoppedAutomation, map[string]any{"automation_id": "a"}, `{"automation":{"status":"paused"}}`, `{"automation":{"status":"live"}}`},
		"retirer":   {activeInList, map[string]any{"email": "a@b.fr", "list_id": "l"}, `{"contact_list":{"status":"active"}}`, `{"contact_list":{"status":"unsubscribed"}}`},
		"modele":    {notUsedLive("template_id"), map[string]any{"id": "m"}, `{"automations":[{"id":"s","nodes":[{"config":{"template_id":"autre"}}]}]}`, `{"automations":[{"id":"s","nodes":[{"config":{"variants":[{"template_id":"m"}]}}]}]}`},
		"segment":   {notUsedLive("segment_id", "segment_ids", "segments"), map[string]any{"id": "g"}, `{"automations":[]}`, `{"automations":[{"id":"s","trigger":{"segment_ids":["x","g"]}}]}`},
		"liste":     {notUsedLive("list_id", "list_ids"), map[string]any{"id": "l"}, `{"automations":[{"id":"s","list_id":"k"}]}`, `{"automations":[{"id":"s","list_id":"l"}]}`},
	} {
		assert.NoError(t, c.check(fixed(c.ok), c.args), name)
		assert.Error(t, c.check(fixed(c.stop), c.args), name)
		assert.Error(t, c.check(fixed(`pas du json`), c.args), name)
		assert.Error(t, c.check(down, c.args), name)
	}
	assert.Error(t, draftAutomation(fixed(`{"automation":{"status":"draft"}}`), map[string]any{"automation": map[string]any{}}), "sans id")
	// La séquence qui cite l'objet peut être au-delà de la première page.
	full := strings.TrimSuffix(strings.Repeat(`{"id":"z"},`, maxLimit), ",")
	pages := map[string]string{"0": `{"automations":[` + full + `]}`, "100": `{"automations":[{"id":"s","list_id":"l"}]}`}
	assert.Error(t, notUsedLive("list_id")(func(_ string, q url.Values) ([]byte, error) { return []byte(pages[q.Get("offset")]), nil }, map[string]any{"id": "l"}))
}

// Un refus n'appelle jamais la route d'écriture ; la lecture de contrôle porte l'espace de l'appelant.
func TestCall_VerificationAvantEcriture(t *testing.T) {
	r := newRig(t, member("owner", nil))
	r.disp.resp = `{"broadcast":{"status":"processing"}}`
	out := r.rpc(t, "tools/call", `{"name":"broadcasts_update","arguments":{"id":"b","name":"B","audience":{},"test_settings":{}}}`)["result"].(map[string]any)
	assert.Equal(t, true, out["isError"])
	assert.Contains(t, out["content"].([]any)[0].(map[string]any)["text"], "brouillon")
	assert.Equal(t, "/api/broadcasts.get", r.disp.path, "la route d'écriture n'est pas appelée")
	assert.Equal(t, http.MethodGet, r.disp.method)
	r.disp.resp = `{"broadcast":{"status":"draft"}}`
	out = r.rpc(t, "tools/call", `{"name":"broadcasts_update","arguments":{"id":"b","name":"B","audience":{},"test_settings":{}}}`)["result"].(map[string]any)
	assert.Equal(t, false, out["isError"])
	assert.Equal(t, "/api/broadcasts.update", r.disp.path)
}

// Les droits : lire les réponses n'ouvre ni l'écriture ni les contacts.
func TestCodeTools_Droits(t *testing.T) {
	reader := member("member", domain.UserPermissions{domain.PermissionResourceMessageHistory: {Read: true}})
	for name, want := range map[string]bool{"mail_list": true, "mail_thread": true, "mailboxes_list": true, "engagement_by_provider": true, "mail_mark": false, "analytics_query": false, "contact_timeline": false, "segments_create": false, "templates_delete": false} {
		tl, ok := findTool(name)
		require.True(t, ok, name)
		assert.Equal(t, want, tl.allowed(reader), name)
	}
}

func TestDropKeys(t *testing.T) {
	assert.JSONEq(t, `{"message":{"id":"m","body_text":"t"}}`, string(compactMessage([]byte(`{"message":{"id":"m","body_text":"t","body_html_sanitized":"<p>","headers":{"X":["y"]}}}`))))
	assert.JSONEq(t, `{"events":[{"id":"e"}]}`, string(compactDeliveryEvents([]byte(`{"events":[{"id":"e","raw_payload":"gros"}]}`))))
	assert.Equal(t, "odd", string(dropKeys([]byte("odd"), "x")))
}
