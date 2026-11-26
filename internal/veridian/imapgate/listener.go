package imapgate

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
)

// DefaultMaxConnsPerIP : connexions simultanées par adresse cliente (IPv6 regroupée au /64, comme LimitKey).
const DefaultMaxConnsPerIP = 20

type ipLimitListener struct {
	net.Listener
	max int
	mu  sync.Mutex
	n   map[string]int
}

// LimitListener borne les connexions simultanées par adresse cliente ; perIP <= 0 : aucune borne. Placé APRÈS le lecteur PROXY v2 :
// c'est l'adresse annoncée par l'entrée publique qui compte, pas celle de l'entrée. Une connexion en trop est coupée sans un mot.
func LimitListener(ln net.Listener, perIP int) net.Listener {
	if perIP <= 0 {
		return ln
	}
	return &ipLimitListener{Listener: ln, max: perIP, n: map[string]int{}}
}

type ipConn struct {
	net.Conn
	once sync.Once
	done func()
}

func (c *ipConn) Close() error {
	c.once.Do(c.done)
	return c.Conn.Close()
}

func (l *ipLimitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		key := boxaccess.LimitKey(remoteIP(c))
		l.mu.Lock()
		full := l.n[key] >= l.max
		if !full {
			l.n[key]++
		}
		l.mu.Unlock()
		if full {
			_ = c.Close()
			continue
		}
		return &ipConn{Conn: c, done: func() {
			l.mu.Lock()
			if l.n[key]--; l.n[key] <= 0 {
				delete(l.n, key)
			}
			l.mu.Unlock()
		}}, nil
	}
}

// ParseCIDRs lit une liste de préfixes séparés par des virgules ou des espaces ("10.0.0.0/8, 172.26.64.1"). Une adresse seule vaut /32 ou /128.
func ParseCIDRs(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("préfixe %q invalide", f)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// trusted dit si l'adresse source d'une connexion est dans la liste ; liste vide : toute source (comportement historique).
func trusted(list []netip.Prefix, a net.Addr) bool {
	if len(list) == 0 {
		return true
	}
	t, ok := a.(*net.TCPAddr)
	if !ok {
		return false
	}
	ip, ok := netip.AddrFromSlice(t.IP)
	if !ok {
		return false
	}
	ip = ip.Unmap()
	for _, p := range list {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
