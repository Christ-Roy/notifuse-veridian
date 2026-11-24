package boxaccess

import "net/netip"

// LimitKey : clé de limitation d'une adresse cliente. IPv4 entière ; IPv6 au /64, parce qu'un seul client en dispose de 2^64
// adresses, donc d'autant de compteurs neufs. Les journaux gardent l'adresse entière. Illisible : la valeur telle quelle.
func LimitKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap().WithZone("")
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}
