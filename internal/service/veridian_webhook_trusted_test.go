package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseWebhookTrustedHosts(t *testing.T) {
	assert.Equal(t, map[string]bool{"bridge.exemple.fr": true, "hub.exemple.fr": true}, parseWebhookTrustedHosts(" Bridge.exemple.fr, hub.exemple.fr ,"))
	for _, bad := range []string{"", "*", "*.exemple.fr", "10.0.0.1", "localhost", "a.fr,https://b.fr", "a.fr,b.fr:8443", "a.fr/x", "::1"} {
		assert.Empty(t, parseWebhookTrustedHosts(bad), bad)
	}
}

// recordDial remplace la connexion finale : on lit l'adresse que la garde a laissée passer.
func recordDial(t *testing.T) *[]string {
	t.Helper()
	var got []string
	orig := ssrfFinalDial
	ssrfFinalDial = func(_ context.Context, _ *net.Dialer, _, addr string) (net.Conn, error) {
		got = append(got, addr)
		return nil, errors.New("connexion coupée par le test")
	}
	t.Cleanup(func() { ssrfFinalDial = orig })
	return &got
}

func TestGuardedWebhookClient_ExceptionEtroite(t *testing.T) {
	withResolverOverride(t, map[string][]net.IP{
		"bridge.exemple.fr": {net.ParseIP("100.81.160.83")}, "autre.exemple.fr": {net.ParseIP("100.81.160.83")},
		"piege.exemple.fr": {net.ParseIP("169.254.169.254")}, "boucle.exemple.fr": {net.ParseIP("127.0.0.1")},
	})
	got := recordDial(t)
	c := guardedWebhookClient(map[string]bool{"bridge.exemple.fr": true, "piege.exemple.fr": true, "boucle.exemple.fr": true})
	try := func(u string) { _, _ = c.Post(u, "application/json", nil) } //nolint:bodyclose // aucune réponse
	for _, u := range []string{"https://autre.exemple.fr/", "http://bridge.exemple.fr/", "https://bridge.exemple.fr:8443/", "https://piege.exemple.fr/", "https://boucle.exemple.fr/", "https://100.81.160.83/"} {
		try(u)
		assert.Empty(t, *got, "%s ne doit joindre personne", u)
	}
	try("https://BRIDGE.exemple.fr/webhooks")
	assert.Equal(t, []string{"100.81.160.83:443"}, *got, "l'hôte nommé, en https, passe")

	*got = nil
	plain := guardedWebhookClient(nil)
	_, _ = plain.Post("https://bridge.exemple.fr/", "application/json", nil) //nolint:bodyclose // aucune réponse
	assert.Empty(t, *got, "sans liste, rien d'interne")
}

// Une redirection publique ne mène jamais à un hôte interne admis.
func TestGuardedWebhookClient_PasDeRedirectionVersUnHoteAdmis(t *testing.T) {
	c := guardedWebhookClient(map[string]bool{"bridge.exemple.fr": true})
	u, _ := url.Parse("https://bridge.exemple.fr/x")
	assert.Error(t, c.CheckRedirect(&http.Request{URL: u}, nil))
	u, _ = url.Parse("https://public.exemple.com/x")
	assert.NoError(t, c.CheckRedirect(&http.Request{URL: u}, nil))
	assert.Error(t, c.CheckRedirect(&http.Request{URL: u}, make([]*http.Request, maxWebhookRedirects)))
}

// La liste ne sert qu'à livrer : un client ne crée pas d'abonnement vers un hôte interne, même admis.
func TestWebhookTrustedHosts_NePermetPasLaCreation(t *testing.T) {
	t.Setenv(webhookTrustedHostsEnv, "bridge.exemple.fr")
	withResolverOverride(t, map[string][]net.IP{"bridge.exemple.fr": {net.ParseIP("100.81.160.83")}})
	assert.Error(t, validateWebhookURL(context.Background(), "https://bridge.exemple.fr/webhooks"))
}
