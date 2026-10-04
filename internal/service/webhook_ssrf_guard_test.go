package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain overrides lookupIPAddrFn for the WHOLE service package test
// binary so no test here ever depends on live DNS/network egress (CI runs
// on self-hosted runners; a real lookup would be slow, flaky, and might
// have no egress at all). Any hostname not explicitly mapped below
// resolves to a fixed PUBLIC IP, so every pre-existing test using
// "example.com"-style URLs keeps passing unmodified. Tests that need a
// malicious/private resolution install their own mapping and restore the
// default via t.Cleanup.
func TestMain(m *testing.M) {
	lookupIPAddrFn = fakeLookupIPAddr(nil)
	os.Exit(m.Run())
}

// fakeLookupIPAddr builds a deterministic resolver for tests: hosts present
// in overrides resolve to the given IPs, everything else resolves to a
// fixed public IP (93.184.216.34, example.com real stable address).
func fakeLookupIPAddr(overrides map[string][]net.IP) func(ctx context.Context, host string) ([]net.IP, error) {
	defaultPublic := []net.IP{net.ParseIP("93.184.216.34")}
	return func(_ context.Context, host string) ([]net.IP, error) {
		if ips, ok := overrides[host]; ok {
			return ips, nil
		}
		return defaultPublic, nil
	}
}

// withResolverOverride temporarily swaps the package resolver for a single
// test, restoring the package-wide default (set by TestMain) afterwards.
func withResolverOverride(t *testing.T, overrides map[string][]net.IP) {
	t.Helper()
	orig := lookupIPAddrFn
	lookupIPAddrFn = fakeLookupIPAddr(overrides)
	t.Cleanup(func() { lookupIPAddrFn = orig })
}

// TestIsBlockedWebhookIP reproduces, address by address, the exact SSRF
// matrix from the 2026-10-04 audit: loopback, cloud metadata link-local,
// RFC1918, Tailscale/CGNAT, IPv6 loopback/ULA/link-local must ALL be
// blocked; a normal public IP must NOT be.
func TestIsBlockedWebhookIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",         // loopback
		"127.0.0.53",        // loopback range, not just .1
		"169.254.169.254",   // cloud metadata endpoint (AWS/GCP/Azure)
		"169.254.1.1",       // link-local
		"10.0.0.5",          // RFC1918
		"172.16.5.4",        // RFC1918
		"192.168.1.1",       // RFC1918
		"100.64.0.1",        // CGNAT / Tailscale (brief explicitly names this range)
		"100.100.100.100",   // still within 100.64.0.0/10
		"0.0.0.0",           // unspecified
		"0.5.5.5",           // "this network" 0.0.0.0/8
		"::1",               // IPv6 loopback
		"fe80::1",           // IPv6 link-local
		"fc00::1",           // IPv6 ULA
		"fd12:3456:789a::1", // IPv6 ULA
		"::ffff:127.0.0.1",  // IPv4-mapped IPv6 loopback (smuggling attempt)
		"::ffff:10.0.0.5",   // IPv4-mapped IPv6 RFC1918 (smuggling attempt)
		"224.0.0.1",         // multicast
	}
	for _, raw := range blocked {
		t.Run(raw, func(t *testing.T) {
			ip := net.ParseIP(raw)
			require.NotNil(t, ip, "test IP must parse")
			assert.True(t, isBlockedWebhookIP(ip), "%s must be blocked", raw)
		})
	}

	allowed := []string{
		"93.184.216.34",  // example.com
		"1.1.1.1",        // Cloudflare DNS
		"8.8.8.8",        // Google DNS
		"100.63.255.255", // just OUTSIDE the CGNAT block (100.64.0.0/10 starts at 100.64.0.0)
		"100.128.0.1",    // just outside the CGNAT block on the other side
	}
	for _, raw := range allowed {
		t.Run(raw, func(t *testing.T) {
			ip := net.ParseIP(raw)
			require.NotNil(t, ip)
			assert.False(t, isBlockedWebhookIP(ip), "%s must NOT be blocked", raw)
		})
	}
}

// TestValidateWebhookURL_SSRF is the service-level (Create/Update path)
// reproduction of the audit: every one of these must be rejected at
// subscription creation time, before anything is ever persisted.
func TestValidateWebhookURL_SSRF(t *testing.T) {
	ctx := context.Background()

	literalIPCases := []string{
		"http://127.0.0.1:18423/",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/",
		"http://100.64.0.1/",
		"http://[::1]/",
	}
	for _, u := range literalIPCases {
		t.Run(u, func(t *testing.T) {
			err := validateWebhookURL(ctx, u)
			require.Error(t, err, "SSRF target must be rejected: %s", u)
		})
	}

	t.Run("hostname resolving to a private IP", func(t *testing.T) {
		withResolverOverride(t, map[string][]net.IP{
			"internal.evil.test": {net.ParseIP("10.0.0.5")},
		})
		err := validateWebhookURL(ctx, "https://internal.evil.test/webhook")
		require.Error(t, err)
	})

	t.Run("legitimate public URL still works", func(t *testing.T) {
		err := validateWebhookURL(ctx, "https://example.com/webhook")
		require.NoError(t, err)
	})
}

// TestSsrfSafeDialContext_RefusesConnection proves the DIAL-TIME layer
// (defense against DNS rebinding) actually refuses to open a TCP connection
// to a blocked address, not just that validateWebhookURL rejects the URL
// string. We point a real listener at loopback (impossible to avoid in a
// unit test) but make the guard believe that hostname resolves there via
// the fake resolver, then assert the dial is refused and nothing was ever
// sent to the listener.
func TestSsrfSafeDialContext_RefusesConnection(t *testing.T) {
	var hit bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	serverAddr := server.Listener.Addr().(*net.TCPAddr)

	withResolverOverride(t, map[string][]net.IP{
		"internal.evil.test": {net.ParseIP("127.0.0.1")},
	})

	client := newSSRFSafeHTTPClient(2000000000) // 2s, avoid importing time just for this
	targetURL := fmt.Sprintf("http://internal.evil.test:%d/", serverAddr.Port)
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	require.Error(t, err, "dial to a hostname resolving to loopback must be refused")
	assert.False(t, hit, "the blocked destination must never actually receive the request")
}

// TestSsrfSafeDialContext_AllowsPublicDestination is the companion
// "doesnt break legitimate traffic" proof: we verify the guard lets a
// destination through whose resolved IP is NOT in any blocked range, using
// a direct unit check on the dialer decision rather than a real external
// network call (unit tests must not depend on internet egress).
func TestSsrfSafeDialContext_AllowsPublicDestination(t *testing.T) {
	dialer := &net.Dialer{}
	dial := ssrfSafeDialContext(dialer)

	withResolverOverride(t, map[string][]net.IP{
		"public.example.test": {net.ParseIP("93.184.216.34")},
	})

	// The TCP handshake itself does not need to succeed, and this test must
	// never depend on real network egress (self-hosted CI runner, possibly
	// sandboxed) - a short context deadline bounds how long an actual
	// connect attempt (or its absence of a fast RST) can take. We only need
	// to prove the guard did NOT reject the destination for being blocked:
	// a refusal from our own guard carries this exact text; a deadline
	// exceeded or connection-refused error proves the IP check passed and
	// dialing was actually attempted (the guard never got the chance to
	// say no).
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := dial(ctx, "tcp", "public.example.test:9")
	if err != nil {
		assert.NotContains(t, err.Error(), "resolves only to internal/private/link-local addresses")
	}
}

// TestNewWebhookDeliveryWorker_DefaultClientIsSSRFSafe proves the
// PRODUCTION default (nil passed in) is wired to the guarded transport, not
// a plain client, i.e. this is not just a standalone helper nobody calls.
func TestNewWebhookDeliveryWorker_DefaultClientIsSSRFSafe(t *testing.T) {
	worker := NewWebhookDeliveryWorker(nil, nil, nil, nil, nil)
	require.NotNil(t, worker.httpClient)
	require.NotNil(t, worker.httpClient.Transport)
	transport, ok := worker.httpClient.Transport.(*http.Transport)
	require.True(t, ok, "default client must use *http.Transport so DialContext applies")
	require.NotNil(t, transport.DialContext, "default client must carry the SSRF-guarded DialContext")
}
