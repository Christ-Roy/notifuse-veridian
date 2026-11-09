//go:build linux

package pgdial

import (
	"syscall"
)

const tcpUserTimeout = 0x12 // TCP_USER_TIMEOUT (linux/tcp.h)

func setUserTimeout(_, _ string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, tcpUserTimeout, int(UserTimeout.Milliseconds()))
	}); err != nil {
		return err
	}
	return serr
}
