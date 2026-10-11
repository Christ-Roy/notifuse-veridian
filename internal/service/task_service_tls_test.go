package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fiche 64 : le client de dispatch des tâches vérifie le certificat. Un
// serveur HTTPS à certificat auto-signé (donc non fiable) doit être refusé ;
// l'ancien InsecureSkipVerify l'acceptait.
func TestDispatchClient_VerifiesTLSCertificate(t *testing.T) {
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()

	_, err := newDispatchHTTPClient().Post(srv.URL, "application/json", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate")
	assert.Equal(t, 0, hits)

	// Contrôle positif : un serveur HTTP clair (INTERNAL_API_ENDPOINT) passe.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer plain.Close()
	resp, err := newDispatchHTTPClient().Post(plain.URL, "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
}
