package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === TestVeridianObfuscate_TableDriven ===
// Couvre les cas de la spec : vide, 1 char, 3 chars, longue chaîne, unicode.
// La règle : 33% des runes en clair + reste `•`. Strings <=3 runes → 100% `•`.
func TestVeridianObfuscate_TableDriven(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"single char", "a", "•"},
		{"two chars", "ab", "••"},
		{"exactly three chars", "abc", "•••"},
		{"four chars keeps one", "abcd", "a•••"},
		{"six chars keeps two", "abcdef", "ab••••"},
		{"long ascii email", "alice@example.com", "alice••••••••••••"},
		{"long ascii", "abcdefghijklmno", "abcde••••••••••"},
		{"unicode accents", "éléphant", "él••••••"},
		{"unicode chinese", "你好世界你好世界", "你好••••••"},
		{"emoji single", "🚀", "•"},
		{"emoji four", "🚀🚀🚀🚀", "🚀•••"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := veridianObfuscate(tc.input)
			assert.Equal(t, tc.want, got, "input=%q", tc.input)
			// Invariant : la longueur en runes du résultat == celle de l'input.
			assert.Equal(t, len([]rune(tc.input)), len([]rune(got)),
				"obfuscated must preserve rune length")
		})
	}
}

// TestVeridianObfuscate_Keeps33PercentApprox vérifie que pour des inputs
// longs, on garde environ 33% des runes en clair (la division entière
// n/3 peut donner 33.0% pour n=3k, 33.3% pour n=3k+1).
func TestVeridianObfuscate_Keeps33PercentApprox(t *testing.T) {
	for _, n := range []int{12, 30, 33, 100} {
		input := strings.Repeat("x", n)
		got := veridianObfuscate(input)
		keep := strings.Count(got, "x")
		expected := n / 3
		assert.Equal(t, expected, keep,
			"n=%d should keep %d chars in clear, got %d", n, expected, keep)
	}
}

// TestVeridianObfuscateFull masque entièrement (utilisé pour SENSITIVE_FIELDS).
func TestVeridianObfuscateFull(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"a", "•"},
		{"password123", "•••••••••••"},
		{"éléphant", "••••••••"}, // 8 runes
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := veridianObfuscateFull(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestVeridianIsSensitiveField vérifie le match case-insensitive + prefix.
func TestVeridianIsSensitiveField(t *testing.T) {
	cases := []struct {
		key      string
		expected bool
	}{
		// SENSITIVE_FIELDS exacts
		{"password", true},
		{"PASSWORD", true},
		{"Password", true},
		{"api_key", true},
		{"API_KEY", true},
		{"token", true},
		{"client_secret", true},
		{"billing_info", true},
		{"smtp_password", true},
		// Préfixes stripe_
		{"stripe_customer_id", true},
		{"stripe_subscription_id", true},
		{"STRIPE_PAYMENT_INTENT_ID", true},
		// Non-sensitive
		{"email", false},
		{"name", false},
		{"workspace_id", false},
		{"created_at", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			assert.Equal(t, tc.expected, veridianIsSensitiveField(tc.key))
		})
	}
}

// TestVeridianWalkAndObfuscate_NestedJSON parcourt un arbre JSON complet
// avec maps, slices, types mixtes.
func TestVeridianWalkAndObfuscate_NestedJSON(t *testing.T) {
	raw := `{
		"workspace_id": "ws-12345",
		"email": "alice@example.com",
		"password": "supersecret",
		"api_key": "sk_live_abcdef123",
		"stripe_customer_id": "cus_ABC123",
		"count": 42,
		"active": true,
		"deleted_at": null,
		"contacts": [
			{"name": "Alice", "email": "alice@example.com"},
			{"name": "Bob", "password": "hunter2"}
		],
		"nested": {
			"client_secret": "topsecret",
			"label": "production"
		}
	}`

	var parsed interface{}
	require.NoError(t, json.Unmarshal([]byte(raw), &parsed))

	obf := veridianWalkAndObfuscate(parsed, "")
	out, err := json.Marshal(obf)
	require.NoError(t, err)

	// Re-parse pour assertions structurées.
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &got))

	// Numbers + bools + null préservés.
	assert.EqualValues(t, 42, got["count"])
	assert.Equal(t, true, got["active"])
	assert.Nil(t, got["deleted_at"])

	// Strings partiellement obfusquées (champ non-sensitive : workspace_id = 8 chars → 2 en clair).
	assert.Equal(t, "ws••••••", got["workspace_id"])
	// email "alice@example.com" = 17 chars → 5 en clair
	assert.Equal(t, "alice••••••••••••", got["email"])

	// SENSITIVE_FIELDS entièrement obfusqués.
	assert.Equal(t, "•••••••••••", got["password"], "password = 11 chars all bullets")
	assert.Equal(t, "•••••••••••••••••", got["api_key"], "api_key = 17 chars all bullets")
	assert.Equal(t, "••••••••••", got["stripe_customer_id"], "stripe_* = 10 chars all bullets")

	// Nested object : client_secret = 9 chars full
	nested := got["nested"].(map[string]interface{})
	assert.Equal(t, "•••••••••", nested["client_secret"])
	// label = 10 chars → 3 en clair
	assert.Equal(t, "pro•••••••", nested["label"])

	// Slice de contacts : items obfusqués
	contacts := got["contacts"].([]interface{})
	require.Len(t, contacts, 2)
	c0 := contacts[0].(map[string]interface{})
	// "Alice" = 5 chars → 1 en clair
	assert.Equal(t, "A••••", c0["name"])
	c1 := contacts[1].(map[string]interface{})
	// password dans nested object aussi full obfusqué
	assert.Equal(t, "•••••••", c1["password"])
}

// TestVeridianObfuscateJSONBody_RoundTrip teste le helper de plus haut niveau.
func TestVeridianObfuscateJSONBody_RoundTrip(t *testing.T) {
	body := []byte(`{"name":"Alice","email":"alice@example.com","password":"hunter2"}`)
	out, isJSON := veridianObfuscateJSONBody(body)
	assert.True(t, isJSON)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, "A••••", got["name"])
	assert.Equal(t, "alice••••••••••••", got["email"])
	assert.Equal(t, "•••••••", got["password"], "password full obfuscated")
}

// TestVeridianObfuscateJSONBody_NonJSONPassesThrough garantit qu'on
// retourne le body original + isJSON=false sur du non-JSON (binaire, CSV).
func TestVeridianObfuscateJSONBody_NonJSONPassesThrough(t *testing.T) {
	cases := []string{
		"",
		"not json",
		"<html><body>html</body></html>",
		"name,email\nAlice,alice@x.com",
		"plain text without braces",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			out, isJSON := veridianObfuscateJSONBody([]byte(c))
			assert.False(t, isJSON)
			assert.Equal(t, c, string(out))
		})
	}
}

// TestVeridianObfuscateJSONBody_RootArray vérifie que les tableaux racine
// sont aussi obfusqués (cas API list endpoint qui retourne `[{...}, {...}]`).
// Bob = 3 runes → 100% bullets (règle <=3 chars → full).
func TestVeridianObfuscateJSONBody_RootArray(t *testing.T) {
	body := []byte(`[{"name":"Alice"},{"name":"Bob"}]`)
	out, isJSON := veridianObfuscateJSONBody(body)
	assert.True(t, isJSON)
	assert.Contains(t, string(out), "A••••", "Alice (5 chars) keeps 1 char in clear")
	assert.Contains(t, string(out), "•••", "Bob (3 chars) fully obfuscated")
}

// TestVeridianResponseCapturer_BasicCapture vérifie que le capturer
// reproduit fidèlement headers + status + body.
func TestVeridianResponseCapturer_BasicCapture(t *testing.T) {
	c := newVeridianResponseCapturer()
	c.Header().Set("X-Custom", "foo")
	c.WriteHeader(http.StatusCreated)
	_, _ = c.Write([]byte("hello"))

	assert.Equal(t, http.StatusCreated, c.statusCode)
	assert.Equal(t, "foo", c.Header().Get("X-Custom"))
	assert.Equal(t, "hello", c.body.String())

	// Flush vers un vrai writer
	rec := httptest.NewRecorder()
	c.flushTo(rec, nil)
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "foo", rec.Header().Get("X-Custom"))
	assert.Equal(t, "hello", rec.Body.String())
}

// TestVeridianResponseCapturer_BodyOverride vérifie que flushTo accepte
// un override (cas : on a obfusqué le body et on flush la version modifiée).
func TestVeridianResponseCapturer_BodyOverride(t *testing.T) {
	c := newVeridianResponseCapturer()
	c.WriteHeader(http.StatusOK)
	_, _ = c.Write([]byte(`{"name":"original"}`))

	override := []byte(`{"name":"o•••••••"}`)
	rec := httptest.NewRecorder()
	c.flushTo(rec, override)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, string(override), rec.Body.String())
	assert.Equal(t, itoaPositive(len(override)), rec.Header().Get("Content-Length"))
}

// TestVeridianResponseCapturer_DefaultStatusIsOK vérifie qu'un handler
// qui n'appelle jamais WriteHeader voit son status défauté à 200 (Go std lib comportement).
func TestVeridianResponseCapturer_DefaultStatusIsOK(t *testing.T) {
	c := newVeridianResponseCapturer()
	_, _ = c.Write([]byte("body without explicit WriteHeader"))
	assert.Equal(t, http.StatusOK, c.statusCode)
}

// TestVeridianResponseCapturer_WriteHeaderOnceOnly garantit que les appels
// multiples à WriteHeader ne changent pas le code (cohérent avec
// net/http standard).
func TestVeridianResponseCapturer_WriteHeaderOnceOnly(t *testing.T) {
	c := newVeridianResponseCapturer()
	c.WriteHeader(http.StatusCreated)
	c.WriteHeader(http.StatusInternalServerError) // ignoré
	assert.Equal(t, http.StatusCreated, c.statusCode)
}

// TestItoaPositive vérifie le helper d'entier→string interne.
func TestItoaPositive(t *testing.T) {
	cases := map[int]string{
		0:     "0",
		1:     "1",
		9:     "9",
		10:    "10",
		99:    "99",
		1234:  "1234",
		99999: "99999",
	}
	for n, want := range cases {
		assert.Equal(t, want, itoaPositive(n), "itoaPositive(%d)", n)
	}
}

// TestVeridianSetGetHubURL : setter + getter idempotents pour la config Hub.
func TestVeridianSetGetHubURL(t *testing.T) {
	original := VeridianGetHubURL()
	t.Cleanup(func() { VeridianSetHubURL(original) })

	VeridianSetHubURL("https://custom.veridian.test/")
	assert.Equal(t, "https://custom.veridian.test", VeridianGetHubURL(),
		"trailing slash must be trimmed")

	VeridianSetHubURL("") // no-op : ne doit pas écraser
	assert.Equal(t, "https://custom.veridian.test", VeridianGetHubURL())
}
