package imapgate

import (
	"net"
	"testing"
	"time"
)

func v4Header(cmd byte, ip string) []byte {
	h := append([]byte{}, proxySig...)
	h = append(h, 0x20|cmd, 0x11, 0, 12)
	h = append(h, net.ParseIP(ip).To4()...)
	return append(h, 10, 0, 0, 1, 0x30, 0x39, 0x07, 0xC9)
}

func TestProxyListener_RealClientAddressOrNothing(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newProxyListener(raw)
	got := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(got)
				return
			}
			got <- c
		}
	}()
	dial := func(b []byte) net.Conn {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = c.Write(b)
		return c
	}
	// Un client muet ne bloque pas le suivant, un client sans en-tête n'est jamais servi.
	slow := dial(nil)
	defer slow.Close()
	bad := dial([]byte("a LOGIN x y\r\nencore du texte\r\n"))
	defer bad.Close()
	good := dial(append(v4Header(1, "203.0.113.7"), []byte("suite")...))
	defer good.Close()
	select {
	case c := <-got:
		if remoteIP(c) != "203.0.113.7" {
			t.Fatalf("adresse du client : %s", remoteIP(c))
		}
		b := make([]byte, 5)
		if _, err := c.Read(b); err != nil || string(b) != "suite" {
			t.Fatalf("les octets suivants appartiennent à la session : %q %v", b, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client valable non servi")
	}
	_ = bad.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := bad.Read(make([]byte, 1)); err == nil {
		t.Fatal("connexion sans en-tête servie")
	}
	local := dial(v4Header(0, "203.0.113.8"))
	defer local.Close()
	select {
	case c := <-got:
		if remoteIP(c) != "127.0.0.1" {
			t.Fatalf("contrôle de santé : %s", remoteIP(c))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("contrôle de santé non servi")
	}
	_ = raw.Close()
	if _, ok := <-got; ok {
		t.Fatal("écoute fermée")
	}
}
