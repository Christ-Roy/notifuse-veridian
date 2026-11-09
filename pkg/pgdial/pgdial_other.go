//go:build !linux

package pgdial

import "syscall"

// Hors Linux (poste de dev) : le keepalive seul s'applique.
func setUserTimeout(_, _ string, _ syscall.RawConn) error { return nil }
