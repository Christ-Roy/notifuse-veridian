package queue

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/netguard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeIMAPListener(t *testing.T) (int, *int32) {
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

func TestIMAPDial_RefusesInternalHosts(t *testing.T) {
	port, conns := fakeIMAPListener(t)
	d := &emersionIMAPDialer{}
	for _, c := range []struct {
		name, host string
		port       int
	}{
		{"loopback joignable", "127.0.0.1", port},
		{"localhost", "localhost", port},
		{"metadonnees", "169.254.169.254", 143},
		{"rfc1918", "10.1.1.1", 993},
		{"tailscale hors liste", "100.64.9.9", 993},
		{"relai connu mais autre port", "100.88.202.29", 143},
		{"ipv6 loopback", "[::1]", port},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := d.Dial(context.Background(), &domain.IMAPSettings{Host: c.host, Port: c.port, Username: "u", Password: "p", UseTLS: true})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "refusing to connect")
		})
	}
	assert.EqualValues(t, 0, atomic.LoadInt32(conns))
}

// Contrôle positif : l'exception exacte laisse passer la connexion (le faux
// serveur raccroche ensuite, l'erreur n'est plus un refus de la garde).
func TestIMAPDial_AllowlistedRelayIsReached(t *testing.T) {
	port, conns := fakeIMAPListener(t)
	old := imapDialAllowlist
	imapDialAllowlist = func() netguard.Allowlist {
		return netguard.Allowlist{{IP: net.ParseIP("127.0.0.1"), Port: port}}
	}
	t.Cleanup(func() { imapDialAllowlist = old })
	_, err := (&emersionIMAPDialer{}).Dial(context.Background(), &domain.IMAPSettings{Host: "127.0.0.1", Port: port, Username: "u", Password: "p", UseTLS: true})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "refusing to connect")
	assert.EqualValues(t, 1, atomic.LoadInt32(conns), "host="+strconv.Itoa(port))
}
