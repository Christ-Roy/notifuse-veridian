package service

import (
	"net"
	"sync/atomic"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/netguard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withProductionSMTPAllowlist remet la liste d'exceptions de production
// (aucune exception loopback) le temps du test.
func withProductionSMTPAllowlist(t *testing.T) {
	t.Helper()
	old := smtpDialAllowlist
	smtpDialAllowlist = netguard.DefaultAllowlist
	t.Cleanup(func() { smtpDialAllowlist = old })
}

// listenCounting ouvre un faux SMTP sur 127.0.0.1 et compte les connexions
// reçues : un refus correct = zéro connexion.
func listenCounting(t *testing.T) (port int, conns *int32) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })
	var n int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&n, 1)
			c.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, &n
}

func TestSMTPSend_RefusesInternalHosts(t *testing.T) {
	withProductionSMTPAllowlist(t)
	port, conns := listenCounting(t)

	cases := []struct {
		name string
		host string
		port int
	}{
		{"loopback joignable", "127.0.0.1", port},
		{"localhost (nom)", "localhost", port},
		{"loopback port 25", "127.0.0.1", 25},
		{"metadonnees cloud", "169.254.169.254", 25},
		{"rfc1918", "10.0.0.5", 25},
		{"tailscale hors liste", "100.64.1.1", 25},
		{"tailscale relais IMAP mais mauvais port (SMTP 25)", "100.88.202.29", 25},
		{"ipv6 loopback", "::1", port},
		{"ipv4 mappe", "::ffff:127.0.0.1", port},
		{"ipv6 ula", "fd00::1", 25},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			settings := &domain.SMTPSettings{Host: c.host, Port: c.port, UseTLS: false, AuthType: "basic"}
			err := sendRawEmailWithSettings(settings, "a@example.com", []string{"b@example.com"}, []byte("x"), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "refusing to connect")
		})
	}
	assert.EqualValues(t, 0, atomic.LoadInt32(conns), "aucune connexion ne doit atteindre le faux serveur")
}

// Contrôle positif de la méthode : avec l'exception explicite (ip:port exact),
// le même serveur est joint, donc le test ci-dessus mesure bien la garde.
func TestSMTPSend_AllowlistedRelayIsReached(t *testing.T) {
	port, conns := listenCounting(t)
	old := smtpDialAllowlist
	smtpDialAllowlist = func() netguard.Allowlist {
		return netguard.Allowlist{{IP: net.ParseIP("127.0.0.1"), Port: port}}
	}
	t.Cleanup(func() { smtpDialAllowlist = old })

	settings := &domain.SMTPSettings{Host: "127.0.0.1", Port: port, AuthType: "basic"}
	err := sendRawEmailWithSettings(settings, "a@example.com", []string{"b@example.com"}, []byte("x"), nil)
	require.Error(t, err) // le faux serveur raccroche, mais la connexion a eu lieu
	assert.NotContains(t, err.Error(), "refusing to connect")
	assert.EqualValues(t, 1, atomic.LoadInt32(conns))

	// Une exception sur un AUTRE port ne couvre pas ce port.
	smtpDialAllowlist = func() netguard.Allowlist {
		return netguard.Allowlist{{IP: net.ParseIP("127.0.0.1"), Port: port + 1}}
	}
	err = sendRawEmailWithSettings(settings, "a@example.com", []string{"b@example.com"}, []byte("x"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to connect")
}
