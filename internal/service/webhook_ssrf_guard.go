package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Veridian fork — durcissement 2026-10-04 (audit sécurité, "SSRF sur les
// webhooks sortants"). AVANT ce correctif, validateURL() ne vérifiait que le
// schéma (http/https) et la présence d'un host : n'importe quel workspace
// pouvait enregistrer un webhook vers 127.0.0.1, 169.254.169.254 (métadonnées
// cloud), une IP RFC1918, le CGNAT Tailscale 100.64.0.0/10, ou un nom de
// domaine public dont le DNS pointe vers l'une de ces plages — et le worker
// de livraison s'y connectait sans aucun filtre.
//
// Deux couches, défense en profondeur :
//  1. validateWebhookURL (création/update) résout le host et rejette si
//     UNE SEULE des IP résolues tombe dans une plage bloquée.
//  2. ssrfSafeDialContext (chaque envoi réel) refait la résolution ET
//     vérifie l'IP sur laquelle la connexion TCP va RÉELLEMENT s'établir,
//     puis épingle cette IP précise pour le Dial — ce qui neutralise le DNS
//     rebinding (la réponse DNS peut changer entre la validation à la
//     création et l'envoi, des heures ou des mois plus tard).
//
// cgnatBlock est posé une fois (100.64.0.0/10, RFC 6598 — c'est aussi la
// plage Tailscale de la flotte Veridian, cf CLAUDE.md §8).
var cgnatBlock = func() *net.IPNet {
	_, n, err := net.ParseCIDR("100.64.0.0/10")
	if err != nil {
		panic(err) // literal constant, cannot fail
	}
	return n
}()

var thisNetworkBlock = func() *net.IPNet {
	_, n, err := net.ParseCIDR("0.0.0.0/8")
	if err != nil {
		panic(err)
	}
	return n
}()

// Veridian fork — lot 0 (2026-10-10, location de Notifuse à des clients) :
// plages réservées supplémentaires, refusées à leur tour. Une plage oubliée
// ici est un trou SSRF, donc la liste est explicite et testée adresse par
// adresse (webhook_ssrf_guard_test.go).
var extraBlockedCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"240.0.0.0/4",     // réservé + broadcast 255.255.255.255
		"::/96",           // IPv4-compatible déprécié (::7f00:1 = 127.0.0.1)
		"fec0::/10",       // site-local IPv6 déprécié
		"100::/64",        // discard-only
		"2001:db8::/32",   // documentation IPv6
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err) // literal constant, cannot fail
		}
		out = append(out, n)
	}
	return out
}()

// embeddedIPv4 extrait l'IPv4 cachée dans une adresse IPv6 de transition
// (NAT64 64:ff9b::/96 et 6to4 2002::/16) : sans ça, 64:ff9b::7f00:1 ou
// 2002:7f00:1:: passeraient pour des adresses publiques alors qu'elles
// aboutissent sur 127.0.0.1 derrière une passerelle de transition.
func embeddedIPv4(ip net.IP) net.IP {
	if len(ip) != net.IPv6len {
		return nil
	}
	if ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b {
		allZero := true
		for _, b := range ip[4:12] {
			if b != 0 {
				allZero = false
			}
		}
		if allZero {
			return net.IPv4(ip[12], ip[13], ip[14], ip[15])
		}
	}
	if ip[0] == 0x20 && ip[1] == 0x02 {
		return net.IPv4(ip[2], ip[3], ip[4], ip[5])
	}
	return nil
}

// isBlockedWebhookIP reports whether ip must never be reached by an
// outbound webhook: loopback, link-local (v4 169.254.0.0/16 and v6
// fe80::/10), RFC1918 + IPv6 ULA (net.IP.IsPrivate covers both since Go
// 1.17), CGNAT/Tailscale 100.64.0.0/10, "this network" 0.0.0.0/8,
// unspecified (0.0.0.0 / ::) and multicast. IPv4-mapped IPv6 addresses
// (::ffff:127.0.0.1) are normalized via To4() first so they cannot be used
// to smuggle a blocked IPv4 address past the v6-shaped checks.
func isBlockedWebhookIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsPrivate() ||
		ip.IsMulticast() {
		return true
	}
	if cgnatBlock.Contains(ip) || thisNetworkBlock.Contains(ip) {
		return true
	}
	for _, n := range extraBlockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	if inner := embeddedIPv4(ip); inner != nil {
		return isBlockedWebhookIP(inner)
	}
	return false
}

// lookupIPAddrFn resolves a hostname to its IP addresses. It is a package
// variable (not a hardcoded net.DefaultResolver call) purely so unit tests
// can substitute a deterministic fake resolver instead of depending on live
// DNS/network egress — production always uses the real implementation below.
var lookupIPAddrFn = func(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// resolveWebhookHost returns every IP a host resolves to. A literal IP
// (e.g. "127.0.0.1" or "::1") is returned as-is without a DNS round trip.
func resolveWebhookHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	ips, err := lookupIPAddrFn(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed for %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("host %q did not resolve to any address", host)
	}
	return ips, nil
}

// validateWebhookURL validates a webhook URL for both shape (http/https,
// has a host) and destination safety (SSRF): every IP the host resolves to
// — or the literal IP itself — must be a routable, non-internal address.
// Called at subscription Create/Update time (durcissement 2026-10-04); the
// dial-time check in ssrfSafeDialContext is the second layer that protects
// against the DNS answer changing after this check ran.
func validateWebhookURL(ctx context.Context, rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("URL is required")
	}

	parsed, err := parseWebhookURL(rawURL)
	if err != nil {
		return err
	}

	host := parsed.Hostname()
	ips, err := resolveWebhookHost(ctx, host)
	if err != nil {
		return fmt.Errorf("could not resolve webhook host %q: %w", host, err)
	}
	for _, ip := range ips {
		if isBlockedWebhookIP(ip) {
			return fmt.Errorf("webhook URL resolves to a disallowed address (%s): internal/private/link-local destinations are not permitted", ip.String())
		}
	}

	return nil
}

// ssrfSafeDialContext returns a DialContext that re-validates the
// destination IP on every single connection attempt — the production
// webhook HTTP client's Transport uses nothing else. It resolves the host
// exactly once, picks the first non-blocked IP, and dials that EXACT IP
// (never the hostname again): this is what defeats DNS rebinding, since a
// second internal lookup by net.Dialer itself could otherwise return a
// different (attacker-controlled) answer than the one just validated.
func ssrfSafeDialContext(base *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid dial address %q: %w", addr, err)
		}

		ips, err := resolveWebhookHost(ctx, host)
		if err != nil {
			return nil, err
		}

		var allowed net.IP
		for _, ip := range ips {
			if !isBlockedWebhookIP(ip) {
				allowed = ip
				break
			}
		}
		if allowed == nil {
			return nil, fmt.Errorf("refusing to connect to %q: resolves only to internal/private/link-local addresses", host)
		}

		return ssrfFinalDial(ctx, base, network, net.JoinHostPort(allowed.String(), port))
	}
}

// ssrfFinalDial ouvre la connexion TCP vers l'adresse DÉJÀ validée et
// épinglée. Variable de paquet uniquement pour que les tests puissent
// rediriger la connexion vers un serveur local APRÈS que la garde a fait son
// travail (résolution, refus, épinglage) : la production ne la change jamais.
var ssrfFinalDial = func(ctx context.Context, base *net.Dialer, network, addr string) (net.Conn, error) {
	return base.DialContext(ctx, network, addr)
}

// maxWebhookRedirects caps how many redirects the outbound webhook client
// will follow (Go's http.Client default is 10). Each hop still goes through
// ssrfSafeDialContext — a redirect to a new host triggers a fresh dial,
// which re-resolves and re-checks that host — so this cap is a belt-and-
// suspenders bound on complexity/latency, not the only protection.
const maxWebhookRedirects = 3

// newSSRFSafeHTTPClient builds the http.Client used for every REAL outbound
// webhook request (delivery worker + "send test webhook"). Its Transport's
// DialContext is ssrfSafeDialContext, so every TCP connection — including
// ones made while following a redirect — is checked against the blocklist
// at the moment it is actually opened.
func newSSRFSafeHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		DialContext:           ssrfSafeDialContext(dialer),
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxWebhookRedirects {
				return fmt.Errorf("stopped after %d redirects", maxWebhookRedirects)
			}
			return nil
		},
	}
}

// parseWebhookURL validates the basic shape of a webhook URL (parseable,
// http/https scheme, non-empty host) ahead of the SSRF destination check.
func parseWebhookURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("URL must use http or https scheme")
	}

	if parsed.Host == "" {
		return nil, fmt.Errorf("URL must have a host")
	}

	return parsed, nil
}
