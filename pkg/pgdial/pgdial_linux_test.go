//go:build linux

package pgdial

import (
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertKeepAlive : le noyau porte bien TCP_USER_TIMEOUT = 20 s et le keepalive actif sur la socket.
func assertKeepAlive(t *testing.T, c *net.TCPConn) {
	t.Helper()
	raw, err := c.SyscallConn()
	require.NoError(t, err)
	var userTimeout, keepAlive int
	require.NoError(t, raw.Control(func(fd uintptr) {
		userTimeout, _ = syscall.GetsockoptInt(int(fd), syscall.IPPROTO_TCP, tcpUserTimeout)
		keepAlive, _ = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_KEEPALIVE)
	}))
	assert.Equal(t, int(UserTimeout.Milliseconds()), userTimeout)
	assert.Equal(t, 1, keepAlive)
}
