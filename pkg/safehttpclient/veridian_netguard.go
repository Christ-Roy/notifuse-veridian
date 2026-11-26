package safehttpclient

import "net"

// Veridian (26/11, chasse sécu lot C) : la liste upstream laissait passer des adresses que pkg/netguard bloque déjà ailleurs :
// IPv4 cachée dans IPv6 (IPv4-compatible ::7f00:1 = 127.0.0.1, NAT64 64:ff9b::/96, 6to4 2002::/16), plages réservées et
// multicast. Ce client sert /api/detect-favicon, route PUBLIQUE sans authentification. Les plages sont ajoutées par ce fichier
// (initialisé après safehttpclient.go, ordre des noms) sans toucher au code upstream.
var veridianExtraBlocked = []string{
	"::/96",          // IPv4-compatible déprécié
	"64:ff9b::/96",   // NAT64 bien connu
	"64:ff9b:1::/48", // NAT64 local
	"2002::/16",      // 6to4
	"fec0::/10",      // site-local déprécié
	"100::/64",       // discard-only
	"192.0.0.0/24",   // IETF
	"198.18.0.0/15",  // benchmarking
	"224.0.0.0/4",    // multicast IPv4
	"240.0.0.0/4",    // réservé + broadcast
}

func init() {
	for _, c := range veridianExtraBlocked {
		if _, n, err := net.ParseCIDR(c); err == nil {
			privateNetworks = append(privateNetworks, n)
		}
	}
}
