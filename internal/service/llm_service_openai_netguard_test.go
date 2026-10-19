package service

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Les tests de flux historiques parlent a un httptest local (http, 127.0.0.1) : ils
// remplacent le client garde. Les deux tests ci-dessous remettent le VRAI client.
// capture la valeur de PRODUCTION avant que init() ne la remplace
var prodOpenAIClient = openaiTenantHTTPClient

func init() {
	openaiTenantHTTPClient = func() *http.Client { return http.DefaultClient }
}

func TestOpenAITenantClientRefusesInternalEndpoints(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	c := prodOpenAIClient()
	for _, u := range []string{srv.URL, "http://169.254.169.254/latest", "https://127.0.0.1:1/"} {
		resp, err := c.Get(u)
		if resp != nil {
			_ = resp.Body.Close()
		}
		require.Error(t, err, u)
	}
	assert.Equal(t, int32(0), atomic.LoadInt32(&hits), "aucune requete n'atteint le serveur local")
}
