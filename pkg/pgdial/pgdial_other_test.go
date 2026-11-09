//go:build !linux

package pgdial

import (
	"net"
	"testing"
)

func assertKeepAlive(t *testing.T, _ *net.TCPConn) { t.Helper() }
