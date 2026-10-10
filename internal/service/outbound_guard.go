package service

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Veridian fork — lot 0 (2026-10-10). Notifuse va être loué à des clients :
// tout appel HTTP sortant dont la destination est choisie par un locataire
// (nœud webhook des automations, flux de données des diffusions, URL
// personnalisée de Firecrawl, confirmation d'abonnement SNS) doit passer par
// CE client, jamais par un http.Client nu.
//
// Garanties :
//   - HTTPS uniquement, aussi sur chaque redirection (httpsOnlyTransport) ;
//   - la destination est résolue UNE fois par connexion, refusée si elle tombe
//     dans une plage interne (ssrfSafeDialContext), et la connexion est
//     épinglée sur cette IP : pas de DNS rebinding entre contrôle et connexion ;
//   - chaque redirection ouvre une nouvelle connexion, donc est revalidée ;
//     3 sauts au plus ;
//   - aucun proxy d'environnement (HTTP_PROXY ne peut pas contourner la garde) ;
//   - délai global et lecture de réponse bornés par l'appelant (readBounded).

const (
	// outboundMaxRedirects borne les sauts suivis par un appel locataire.
	outboundMaxRedirects = 3
	// TenantOutboundTimeout est le délai maximal d'un appel sortant locataire.
	TenantOutboundTimeout = 10 * time.Second
)

// outboundTLSConfig est nil en production (racines système). Les tests y
// posent le certificat de leur serveur HTTPS local.
var outboundTLSConfig *tls.Config

// httpsOnlyTransport refuse toute requête non HTTPS, y compris une
// redirection https -> http, avant d'ouvrir une connexion.
type httpsOnlyTransport struct {
	base http.RoundTripper
}

func (t httpsOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Scheme != "https" {
		scheme := ""
		if req.URL != nil {
			scheme = req.URL.Scheme
		}
		return nil, fmt.Errorf("outbound request refused: https is required (got %q)", scheme)
	}
	return t.base.RoundTrip(req)
}

// NewTenantOutboundClient construit le client HTTP des appels sortants pilotés
// par un locataire. Exporté pour le câblage (app.go) des paquets qui ne
// peuvent pas importer service.
func NewTenantOutboundClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = TenantOutboundTimeout
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil, // jamais de proxy d'environnement
		DialContext:           ssrfSafeDialContext(dialer),
		TLSClientConfig:       outboundTLSConfig,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          20,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: httpsOnlyTransport{base: transport},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= outboundMaxRedirects {
				return fmt.Errorf("stopped after %d redirects", outboundMaxRedirects)
			}
			return nil
		},
	}
}

// ValidateTenantOutboundURL contrôle une URL locataire à l'enregistrement :
// HTTPS, hôte présent, pas d'identifiants dans l'URL, et une IP littérale ne
// doit pas être interne. La résolution DNS n'est PAS faite ici (elle serait
// périmée au moment de l'envoi) : c'est la connexion qui tranche.
func ValidateTenantOutboundURL(raw string) (*url.URL, error) {
	u, err := validateTenantURLShape(raw)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && isBlockedWebhookIP(ip) {
		return nil, fmt.Errorf("url targets a disallowed address (%s): internal/private/link-local destinations are not permitted", ip.String())
	}
	return u, nil
}

// validateTenantURLShape contrôle la forme seule (https, hôte, pas
// d'identifiants). À l'exécution, la destination est tranchée par la
// connexion gardée, pas ici.
func validateTenantURLShape(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("url must use https")
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("url must have a host")
	}
	if u.User != nil {
		return nil, fmt.Errorf("url must not contain credentials")
	}
	return u, nil
}

// readBounded lit au plus max octets d'un corps de réponse.
func readBounded(r io.Reader, max int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, max))
}
