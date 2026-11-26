package imapgate

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"time"
)

// En-tête PROXY v2 : l'entrée publique (HAProxy, Traefik) annonce la vraie adresse du client. Sans lui, la limite d'essais par IP
// compterait tout le monde sous l'adresse de l'entrée : un attaquant fermerait la porte à tous les clients.

var proxySig = []byte("\r\n\r\n\x00\r\nQUIT\n")

const proxyTimeout = 5 * time.Second

// ErrProxyHeader : en-tête absent ou illisible. La connexion est coupée, jamais servie avec une adresse devinée.
var ErrProxyHeader = errors.New("en-tête PROXY v2 absent ou invalide")

type proxyConn struct {
	net.Conn
	remote net.Addr
}

func (c *proxyConn) RemoteAddr() net.Addr {
	if c.remote != nil {
		return c.remote
	}
	return c.Conn.RemoteAddr()
}

// readProxyHeader lit l'en-tête et rend l'adresse du client (nil : contrôle de santé de l'entrée, commande LOCAL).
func readProxyHeader(c net.Conn) (net.Addr, error) {
	_ = c.SetReadDeadline(time.Now().Add(proxyTimeout))
	defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	h := make([]byte, 16)
	if _, err := io.ReadFull(c, h); err != nil {
		return nil, ErrProxyHeader
	}
	for i := range proxySig {
		if h[i] != proxySig[i] {
			return nil, ErrProxyHeader
		}
	}
	if h[12]>>4 != 2 || h[12]&0xF > 1 {
		return nil, ErrProxyHeader
	}
	body := make([]byte, binary.BigEndian.Uint16(h[14:16]))
	if len(body) > 2048 {
		return nil, ErrProxyHeader
	}
	if _, err := io.ReadFull(c, body); err != nil {
		return nil, ErrProxyHeader
	}
	if h[12]&0xF == 0 { // LOCAL
		return nil, nil
	}
	switch {
	case h[13]>>4 == 1 && len(body) >= 12:
		return &net.TCPAddr{IP: net.IP(body[0:4]), Port: int(binary.BigEndian.Uint16(body[8:10]))}, nil
	case h[13]>>4 == 2 && len(body) >= 36:
		return &net.TCPAddr{IP: net.IP(body[0:16]), Port: int(binary.BigEndian.Uint16(body[32:34]))}, nil
	}
	return nil, ErrProxyHeader
}

// proxyListener lit l'en-tête de chaque connexion hors de la boucle d'acceptation : un client lent ne bloque pas les autres.
type proxyListener struct {
	net.Listener
	out     chan net.Conn
	err     chan error
	done    chan struct{}
	trusted []netip.Prefix // sources admises à annoncer une adresse ; vide : toutes (historique)
}

func newProxyListener(ln net.Listener, trusted []netip.Prefix) *proxyListener {
	l := &proxyListener{Listener: ln, out: make(chan net.Conn), err: make(chan error, 1), done: make(chan struct{}), trusted: trusted}
	go l.loop()
	return l
}

func (l *proxyListener) loop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			l.err <- err
			close(l.done)
			return
		}
		go func() {
			// Un en-tête PROXY ne vaut que venant de l'entrée publique : de toute autre source il est une adresse choisie par le
			// client, donc un compteur d'essais neuf à volonté. La connexion est coupée avant de lire un octet.
			if !trusted(l.trusted, c.RemoteAddr()) {
				_ = c.Close()
				return
			}
			a, err := readProxyHeader(c)
			if err != nil {
				_ = c.Close()
				return
			}
			select {
			case l.out <- &proxyConn{Conn: c, remote: a}:
			case <-l.done:
				_ = c.Close()
			}
		}()
	}
}

func (l *proxyListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.out:
		return c, nil
	case err := <-l.err:
		return nil, err
	}
}

// ProxyListener lit l'en-tête PROXY v2 de chaque connexion, de n'importe quelle source (partagé avec le récepteur de courrier).
func ProxyListener(ln net.Listener) net.Listener { return newProxyListener(ln, nil) }

// ProxyListenerTrusted : comme ProxyListener, mais l'en-tête n'est admis que des sources listées ; les autres sont coupées. Liste vide :
// toutes les sources, comme ProxyListener (l'appelant le journalise au démarrage).
func ProxyListenerTrusted(ln net.Listener, trustedProxies []netip.Prefix) net.Listener {
	return newProxyListener(ln, trustedProxies)
}
