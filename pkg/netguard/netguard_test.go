package netguard

import (
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.8.8.8", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1",
		"169.254.169.254", "169.254.0.1", "fe80::1", "100.64.0.1", "100.127.255.254",
		"100.88.202.29", "0.0.0.0", "0.1.2.3", "::", "fc00::1", "fd12::1", "ff02::1",
		"224.0.0.1", "255.255.255.255", "240.0.0.1", "192.0.2.1", "198.18.0.1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::7f00:1", "2002:7f00:1::",
		"::7f00:1", "fec0::1", "2001:db8::1",
	}
	for _, s := range blocked {
		assert.True(t, IsBlockedIP(net.ParseIP(s)), s)
	}
	for _, s := range []string{"8.8.8.8", "37.187.199.185", "51.210.7.44", "142.251.168.108", "2a00:1450:400c:c06::6c", "100.63.255.255", "100.128.0.1"} {
		assert.False(t, IsBlockedIP(net.ParseIP(s)), s)
	}
	assert.True(t, IsBlockedIP(nil))
}

func TestAllowlistIsExact(t *testing.T) {
	a, err := ParseAllowlist("100.88.202.29:993, [fd00::5]:143")
	require.NoError(t, err)
	assert.True(t, a.Allows(net.ParseIP("100.88.202.29"), 993))
	assert.False(t, a.Allows(net.ParseIP("100.88.202.29"), 25), "autre port")
	assert.False(t, a.Allows(net.ParseIP("100.88.202.30"), 993), "autre IP du même /24")
	assert.True(t, a.Allows(net.ParseIP("fd00::5"), 143))
	for _, bad := range []string{"100.88.0.0/16", "100.88.202.29", "host:993", "1.2.3.4:0", "1.2.3.4:99999"} {
		_, err := ParseAllowlist(bad)
		assert.Error(t, err, bad)
	}
}

func TestDefaultAllowlistHasTheThreeIMAPRelaysOnly(t *testing.T) {
	t.Setenv(InternalRelayEnv, "")
	a := DefaultAllowlist()
	for _, ip := range []string{"100.88.202.29", "100.92.215.42", "100.85.160.17"} {
		assert.True(t, a.Allows(net.ParseIP(ip), 993), ip)
		assert.False(t, a.Allows(net.ParseIP(ip), 22), ip)
		assert.False(t, a.Allows(net.ParseIP(ip), 25), ip)
	}
	assert.False(t, a.Allows(net.ParseIP("127.0.0.1"), 993))
	t.Setenv(InternalRelayEnv, "100.1.1.1:587,garbage")
	assert.Len(t, DefaultAllowlist(), 3, "entrée invalide ignorée en bloc")
	t.Setenv(InternalRelayEnv, "100.1.1.1:587")
	assert.True(t, DefaultAllowlist().Allows(net.ParseIP("100.1.1.1"), 587))
}

func TestValidateHost(t *testing.T) {
	al := Allowlist{{IP: net.ParseIP("100.88.202.29"), Port: 993}}
	assert.Error(t, ValidateHost("127.0.0.1", 25, al))
	assert.Error(t, ValidateHost("169.254.169.254", 80, al))
	assert.Error(t, ValidateHost("100.88.202.29", 25, al))
	assert.NoError(t, ValidateHost("100.88.202.29", 993, al))
	assert.NoError(t, ValidateHost("smtp.example.com", 587, al), "un nom : la connexion tranche")
	assert.NoError(t, ValidateHost("8.8.8.8", 587, al))
}

// La garde de connexion (Control) refuse avant tout octet envoyé, sur une IP
// atteinte par RÉSOLUTION de nom, et laisse passer l'exception exacte.
func TestControlOnRealDialer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
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
	port := l.Addr().(*net.TCPAddr).Port
	addr := net.JoinHostPort("localhost", strconv.Itoa(port))

	d := GuardDialer(&net.Dialer{Timeout: 2 * time.Second}, nil)
	_, err = d.Dial("tcp", addr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to connect")
	assert.EqualValues(t, 0, atomic.LoadInt32(&n))

	d = GuardDialer(&net.Dialer{Timeout: 2 * time.Second}, Allowlist{{IP: net.ParseIP("127.0.0.1"), Port: port}})
	c, err := d.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	require.NoError(t, err)
	c.Close()
}

func TestTrustedHostsEnv(t *testing.T) {
	t.Setenv(InternalRelayEnv, "")
	t.Setenv(TrustedHostsEnv, "")
	// refusé par défaut
	d := DefaultAllowlist()
	assert.False(t, d.Allows(net.ParseIP("100.108.136.89"), 993))
	assert.Error(t, ValidateHost("100.108.136.89", 993, d))

	// IP seule : autorisée sur tous les ports, voisin toujours refusé
	t.Setenv(TrustedHostsEnv, "100.108.136.89")
	d = DefaultAllowlist()
	assert.NoError(t, ValidateHost("100.108.136.89", 993, d))
	assert.NoError(t, ValidateHost("100.108.136.89", 587, d))
	assert.NoError(t, CheckAddr("100.108.136.89:993", d))
	assert.Error(t, ValidateHost("100.108.136.90", 993, d), "voisin non listé")
	assert.Error(t, CheckAddr("100.108.136.90:993", d))
	assert.Error(t, ValidateHost("127.0.0.1", 993, d))
	assert.Error(t, CheckAddr("169.254.169.254:80", d))

	// CIDR + IPv6, avec espaces
	t.Setenv(TrustedHostsEnv, " 100.108.136.0/24 , fd7a:115c:a1e0::223b:885a")
	d = DefaultAllowlist()
	assert.NoError(t, CheckAddr("100.108.136.200:25", d))
	assert.Error(t, CheckAddr("100.108.137.1:25", d))
	assert.NoError(t, CheckAddr("[fd7a:115c:a1e0::223b:885a]:993", d))
	assert.Error(t, CheckAddr("[fd7a:115c:a1e0::223b:885b]:993", d))

	// entrée invalide : tout est ignoré, relais intégrés conservés
	t.Setenv(TrustedHostsEnv, "100.108.136.89,garbage")
	d = DefaultAllowlist()
	assert.Len(t, d, 3)
	assert.Error(t, CheckAddr("100.108.136.89:993", d))

	// la liste ne rend jamais joignable via IsBlockedIP (webhooks) : inchangé
	assert.True(t, IsBlockedIP(net.ParseIP("100.108.136.89")))
}

func TestControlHonoursTrustedHosts(t *testing.T) {
	t.Setenv(InternalRelayEnv, "")
	t.Setenv(TrustedHostsEnv, "")
	err := Control(DefaultAllowlist())("tcp", "127.0.0.1:993", nil)
	assert.Error(t, err)
	t.Setenv(TrustedHostsEnv, "127.0.0.0/8")
	assert.NoError(t, Control(DefaultAllowlist())("tcp", "127.0.0.1:993", nil), "revérifié à la connexion avec la même liste")
	assert.Error(t, Control(DefaultAllowlist())("tcp", "10.0.0.1:993", nil))
}
