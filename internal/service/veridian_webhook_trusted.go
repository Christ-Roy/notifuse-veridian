package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	webhookDeliveryTimeout = 30 * time.Second
	webhookDialTimeout     = 10 * time.Second
)

// Hôtes internes admis pour la LIVRAISON des webhooks (fiche 61), par l'opérateur seul. Vide par défaut : rien d'interne n'est joignable.
// Noms exacts, https (443) seulement, jamais une boucle locale ni une adresse de lien local (métadonnées). Une redirection n'y mène jamais, et
// un client ne peut pas y créer d'abonnement : validateWebhookURL ignore cette liste.
const webhookTrustedHostsEnv = "NOTIFUSE_WEBHOOK_TRUSTED_HOSTS"

var webhookHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// parseWebhookTrustedHosts : une entrée illisible vide toute la liste, une exception mal écrite n'ouvre jamais plus.
func parseWebhookTrustedHosts(spec string) map[string]bool {
	out := map[string]bool{}
	for _, h := range strings.Split(spec, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if !webhookHostRe.MatchString(h) || net.ParseIP(h) != nil {
			return map[string]bool{}
		}
		out[h] = true
	}
	return out
}

// newWebhookDeliveryClient : le client de toute livraison réelle. Il est construit ICI, jamais reçu de l'assemblage de l'app : le 25/11/2026, app.go
// passait un client ordinaire et la garde à la connexion n'avait jamais servi en production.
func newWebhookDeliveryClient() *http.Client {
	return guardedWebhookClient(parseWebhookTrustedHosts(os.Getenv(webhookTrustedHostsEnv)))
}

func guardedWebhookClient(trusted map[string]bool) *http.Client {
	client := newSSRFSafeHTTPClient(webhookDeliveryTimeout)
	if len(trusted) == 0 {
		return client
	}
	transport := client.Transport.(*http.Transport)
	guarded := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if err != nil || port != "443" || !trusted[host] {
			return guarded(ctx, network, addr)
		}
		ips, err := resolveWebhookHost(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			return ssrfFinalDial(ctx, &net.Dialer{Timeout: webhookDialTimeout}, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, errors.New("trusted webhook host resolves to no usable address")
	}
	limit := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if trusted[strings.ToLower(strings.TrimSuffix(req.URL.Hostname(), "."))] {
			return errors.New("redirect to an internal trusted host refused")
		}
		return limit(req, via)
	}
	return client
}
