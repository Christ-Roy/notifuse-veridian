package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runOneClick(t *testing.T, req *http.Request) (map[string]interface{}, http.Header, bool) {
	t.Helper()
	var got map[string]interface{}
	var hdr http.Header
	reached := false
	h := VeridianOneClickUnsubscribe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		hdr = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
	}))
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got, hdr, reached
}

// RFC 8058 : un client mail POSTe un formulaire, les paramètres restent dans l'URL (mesuré en prod : 400 avant ce correctif).
func TestVeridianOneClickUnsubscribe_FormulaireRFC8058(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/unsubscribe-oneclick?email=a%40b.fr&email_hmac=abc&lids=l1&wid=ws&mid=m1", strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Googlebot-Unsubscribe")
	got, hdr, reached := runOneClick(t, req)
	require.True(t, reached)
	assert.Equal(t, map[string]interface{}{"wid": "ws", "email": "a@b.fr", "email_hmac": "abc", "lids": []interface{}{"l1"}, "mid": "m1"}, got)
	assert.Equal(t, "application/json", hdr.Get("Content-Type"))
	assert.NotContains(t, strings.ToLower(hdr.Get("User-Agent")), "bot", "le POST explicite RFC 8058 n'est pas un robot de liens")
}

func TestVeridianOneClickUnsubscribe_ListesMultiples(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/unsubscribe-oneclick?email=a%40b.fr&email_hmac=abc&lids=l1,l2&lids=l3&wid=ws&mid=m1", strings.NewReader("List-Unsubscribe=One-Click"))
	got, _, _ := runOneClick(t, req)
	assert.Equal(t, []interface{}{"l1", "l2", "l3"}, got["lids"])
}

func TestVeridianOneClickUnsubscribe_JSONEtAutresInchanges(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/unsubscribe-oneclick", strings.NewReader(`{"wid":"ws","email":"a@b.fr"}`))
	req.Header.Set("Content-Type", "application/json")
	got, _, reached := runOneClick(t, req)
	assert.True(t, reached)
	assert.Equal(t, "ws", got["wid"], "le JSON du centre de notification passe tel quel")

	req = httptest.NewRequest(http.MethodPost, "/unsubscribe-oneclick?wid=ws", strings.NewReader("autre=chose"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	got, _, _ = runOneClick(t, req)
	assert.Nil(t, got, "sans le corps RFC 8058, aucune conversion")
}
