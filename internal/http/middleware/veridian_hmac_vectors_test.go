package middleware

import (
	"flag"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Vecteurs de signature v2 PRODUITS PAR LE GO (veridianHubMACv2), consommés par l'aide TypeScript des E2E
// (tests/e2e-veridian/hub-signature.ts, test hub-signature.test.mjs). Le fichier est la source commune : ce test
// échoue si le Go change (ordre des champs, encodage) sans que le fichier soit régénéré, et le test TS échoue si
// l'aide diverge du Go. Régénérer : go test -run TestVeridianHMAC_VecteursV2 ./internal/http/middleware/ -args -ecrire-vecteurs-hmac
var ecrireVecteursHMAC = flag.Bool("ecrire-vecteurs-hmac", false, "imprime (et écrit si possible) les vecteurs v2")

const veridianHMACVectorsFile = "../../../tests/e2e-veridian/fixtures/hub-hmac-v2-vectors.json"

type hmacV2Vector struct {
	Name      string `json:"name"`
	Secret    string `json:"secret"`
	Timestamp string `json:"timestamp"`
	Method    string `json:"method"`
	URL       string `json:"url"`
	Body      string `json:"body"`
	Signature string `json:"signature"`
}

func hmacV2VectorInputs() []hmacV2Vector {
	return []hmacV2Vector{
		{Name: "post-json", Secret: "vector-secret", Timestamp: "1790000000000", Method: "POST", URL: "https://notifuse.example/api/tenants/provision", Body: `{"tenant_id":"e2e1","owner_email":"a@b.test"}`},
		{Name: "get-query-encodee", Secret: "vector-secret", Timestamp: "1790000000001", Method: "GET", URL: "https://notifuse.example/api/users/by-email?email=a%2Bb%40x.test&z=1"},
		{Name: "delete-chemin-encode", Secret: "vector-secret", Timestamp: "1790000000002", Method: "DELETE", URL: "https://notifuse.example/api/tenants/a%20b%C3%A9/x"},
		{Name: "methode-minuscule-corps-accentue", Secret: "autre-secret", Timestamp: "1790000000003", Method: "post", URL: "http://127.0.0.1:8080/api/veridian/admin/cache/invalidate?a=1", Body: `{"n":"é"}`},
	}
}

func TestVeridianHMAC_VecteursV2(t *testing.T) {
	vs := hmacV2VectorInputs()
	for i := range vs {
		u, err := url.Parse(vs[i].URL)
		require.NoError(t, err)
		vs[i].Signature = hex.EncodeToString(veridianHubMACv2(vs[i].Secret, vs[i].Timestamp, vs[i].Method, u.EscapedPath(), u.RawQuery, []byte(vs[i].Body)))
	}
	out, err := json.MarshalIndent(vs, "", "  ")
	require.NoError(t, err)
	out = append(out, '\n')
	if *ecrireVecteursHMAC {
		// lourd ne rapatrie rien : la sortie est aussi imprimée (go test -v) pour la copier dans le fichier.
		_ = os.WriteFile(veridianHMACVectorsFile, out, 0o644)
		t.Logf("VECTEURS_BEGIN\n%sVECTEURS_END", out)
		return
	}
	got, err := os.ReadFile(veridianHMACVectorsFile)
	require.NoError(t, err)
	assert.Equal(t, string(out), string(got), "vecteurs périmés : régénérer avec -ecrire-vecteurs-hmac")
}

// SignVeridianHubRequest (appel réel) pose la même signature que veridianHubMACv2 sur le même ts.
func TestSignVeridianHubRequest_CoherentAvecLeVecteur(t *testing.T) {
	body := []byte(`{"k":"v"}`)
	req, err := http.NewRequest("post", "https://h.example/api/a%20b?x=1&y=%2B", strings.NewReader(string(body)))
	require.NoError(t, err)
	SignVeridianHubRequest(req, "s", body)
	ts := req.Header.Get("X-Veridian-Timestamp")
	want := hex.EncodeToString(veridianHubMACv2("s", ts, "post", "/api/a%20b", "x=1&y=%2B", body))
	assert.Equal(t, want, req.Header.Get("X-Veridian-Hub-Signature"))
}
