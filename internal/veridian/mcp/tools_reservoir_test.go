package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Une IA alimente et suit ; elle ne relève jamais un plafond, ne touche ni aux profils d'envoi ni aux réglages.
func TestCatalog_AucunOutilNeToucheAuxPlafonds(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)policy\.(set|reset|priority)|stock\.config|emailProfiles\.(setUsage|pause|resume|create|verify)|integrations|settings|workspaces|queue\.(recompute|cancel)|mail\.access|keys`)
	assert.True(t, forbidden.MatchString("/api/veridian/policy.set"), "le garde-fou voit bien un réglage")
	for _, tl := range catalog {
		assert.False(t, forbidden.MatchString(tl.Path), "%s : %s", tl.Name, tl.Path)
	}
}

// Un outil qui peut démarrer une séquence est un envoi.
func TestCatalog_ActivationEstDestructive(t *testing.T) {
	for _, n := range []string{"contacts_import", "contacts_upsert", "segment_rebuild", "reservoir_activate", "automations_activate"} {
		tl, ok := findTool(n)
		require.True(t, ok, n)
		assert.True(t, tl.Destructive, n)
		assert.True(t, tl.Write, n)
	}
}

func TestContactsImport_CorpsEnvoye(t *testing.T) {
	tl, _ := findTool("contacts_import")
	_, body, err := tl.request(map[string]any{"contacts": []any{map[string]any{"email": "a@b.fr"}}, "dry_run": true, "workspace_id": "AUTRE"}, "ws1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"contacts":[{"email":"a@b.fr"}],"dry_run":true,"workspace_id":"ws1"}`, string(body))
	_, _, err = tl.request(map[string]any{}, "ws1")
	assert.Error(t, err)
}

func TestCompact(t *testing.T) {
	out := string(compactAutomations([]byte(`{"total":1,"automations":[{"id":"a","name":"A","status":"live","trigger_sql":"SECRET","nodes":[{"x":1},{"x":2}],"stats":{"enrolled":3}}]}`)))
	assert.JSONEq(t, `{"total":1,"automations":[{"id":"a","name":"A","status":"live","nodes_count":2,"stats":{"enrolled":3}}]}`, out)
	assert.JSONEq(t, `{"segments":[{"id":"s","users_count":4}]}`, string(compactSegments([]byte(`{"segments":[{"id":"s","users_count":4,"tree":{"a":1},"generated_sql":"x"}]}`))))
	q := string(compactQuota([]byte(`{"date":"j","profiles":[{"name":"p","return_inbox":{"host":"h"},"plan":{"daily_cap_today":50,"sent_today":12,"remaining_today":38,"classes":[{"class":"ovh","sent_today":12,"daily_cap":40,"remaining":28,"rate_per_min":1},{"class":"free","sent_today":0}]}},{"name":"sans plan"}]}`)))
	assert.JSONEq(t, `{"date":"j","profiles":[{"name":"p","plan":{"daily_cap_today":50,"sent_today":12,"remaining_today":38,"classes_sent_today":[{"class":"ovh","sent_today":12,"daily_cap":40,"remaining":28}]}},{"name":"sans plan"}]}`, q)
	for _, odd := range []string{`pas du json`, `{"profiles":"x"}`, `[]`} {
		assert.Equal(t, odd, string(compactQuota([]byte(odd))), "forme inattendue : rendue telle quelle")
	}
}

func TestProgramResult(t *testing.T) {
	big := `{"contacts":["` + strings.Repeat("x", maxResultChars*2) + `"]}`
	r := programResult(json.RawMessage("1"), []byte(big))["result"].(map[string]any)
	assert.Equal(t, false, r["isError"])
	assert.Equal(t, json.RawMessage(big), r["structuredContent"], "un programme reçoit le JSON entier")
	r = programResult(json.RawMessage("1"), []byte(`{"x":"`+strings.Repeat("x", maxProgramResult)+`"}`))["result"].(map[string]any)
	assert.Equal(t, true, r["isError"], "trop gros : une erreur franche, jamais un JSON coupé")
	r = programResult(json.RawMessage("1"), []byte(`[1]`))["result"].(map[string]any)
	assert.NotContains(t, r, "structuredContent")
}

// Un appel venu d'un programme du Hub reçoit le JSON entier ; un appel direct reste borné.
func TestHandler_AppelDeProgramme(t *testing.T) {
	r := newRig(t, member("owner", nil))
	r.disp.resp = `{"contacts":["` + strings.Repeat("x", maxResultChars*2) + `"]}`
	call := func(program bool) map[string]any {
		req := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"contacts_list","arguments":{}}}`))
		req.Header.Set("Authorization", "Bearer "+r.key.sign(t, nil))
		if program {
			req.Header.Set(programHeader, programValue)
		}
		w := httptest.NewRecorder()
		r.h.ServeHTTP(w, req)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		return out["result"].(map[string]any)
	}
	text := func(m map[string]any) string { return m["content"].([]any)[0].(map[string]any)["text"].(string) }
	assert.Less(t, len(text(call(false))), maxResultChars+200)
	p := call(true)
	assert.Equal(t, r.disp.resp, text(p))
	assert.Contains(t, p["structuredContent"], "contacts")
}

// Les droits exigés en plus par la route filtrent aussi la liste.
func TestTool_DroitsEnPlus(t *testing.T) {
	act, _ := findTool("reservoir_activate")
	p := domain.UserPermissions{domain.PermissionResourceAutomations: {Read: true, Write: true}}
	assert.False(t, act.allowed(member("member", p)))
	p[domain.PermissionResourceContacts] = domain.ResourcePermissions{Read: true}
	p[domain.PermissionResourceLists] = domain.ResourcePermissions{Write: true}
	p[domain.PermissionResourceMessageHistory] = domain.ResourcePermissions{Read: true}
	assert.True(t, act.allowed(member("member", p)))
	assert.True(t, act.allowed(member("owner", nil)))
}
