// Package netguard est le garde-fou RÉSEAU partagé des connexions sortantes
// dont la destination est choisie par un locataire (Veridian, fiche 61 puis
// fiche 64) : nœud webhook, abonnements webhook, SMTP, IMAP, endpoints des
// fournisseurs d'envoi.
//
// Une seule définition de « adresse interdite » (IsBlockedIP) et une seule
// liste d'exceptions EXPLICITE (Allowlist) : pas de copie par protocole.
//
// Règle : le contrôle porte sur l'IP APRÈS résolution, au moment où la
// connexion TCP s'ouvre (Control), donc pas de rebinding possible entre un
// contrôle et la connexion.
package netguard

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
)

var mustCIDR = func(c string) *net.IPNet {
	_, n, err := net.ParseCIDR(c)
	if err != nil {
		panic(err) // constante littérale, ne peut pas échouer
	}
	return n
}

var blockedCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"100.64.0.0/10",   // CGNAT RFC 6598 = plage Tailscale de la flotte
		"0.0.0.0/8",       // « ce réseau »
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"240.0.0.0/4",     // réservé + broadcast 255.255.255.255
		"::/96",           // IPv4-compatible déprécié (::7f00:1 = 127.0.0.1)
		"fec0::/10",       // site-local IPv6 déprécié
		"100::/64",        // discard-only
		"2001:db8::/32",   // documentation IPv6
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, mustCIDR(c))
	}
	return out
}()

// EmbeddedIPv4 extrait l'IPv4 cachée dans une adresse IPv6 de transition
// (NAT64 64:ff9b::/96 et 6to4 2002::/16) : 64:ff9b::7f00:1 aboutit sur
// 127.0.0.1 derrière une passerelle de transition.
func EmbeddedIPv4(ip net.IP) net.IP {
	if len(ip) != net.IPv6len {
		return nil
	}
	if ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b {
		allZero := true
		for _, b := range ip[4:12] {
			if b != 0 {
				allZero = false
			}
		}
		if allZero {
			return net.IPv4(ip[12], ip[13], ip[14], ip[15])
		}
	}
	if ip[0] == 0x20 && ip[1] == 0x02 {
		return net.IPv4(ip[2], ip[3], ip[4], ip[5])
	}
	return nil
}

// IsBlockedIP indique si ip ne doit jamais être jointe par un appel sortant
// piloté par un locataire : loopback, link-local (169.254/16 dont la
// métadonnée cloud 169.254.169.254, fe80::/10), RFC1918, ULA IPv6, CGNAT /
// Tailscale, non spécifiée, multicast, plages réservées, et l'IPv4 cachée dans
// IPv4-mappé / NAT64 / 6to4. nil est refusé.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsPrivate() || ip.IsMulticast() {
		return true
	}
	for _, n := range blockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	if inner := EmbeddedIPv4(ip); inner != nil {
		return IsBlockedIP(inner)
	}
	return false
}

// AllowEntry est une exception explicite : UNE IP et UN port. Port 0 = tous
// les ports de cette IP, réservé au code (les tests) : ParseAllowlist le refuse,
// donc ni l'environnement ni un locataire ne peuvent l'écrire.
type AllowEntry struct {
	IP   net.IP
	Port int
	// Net, si non nil, autorise tout le CIDR (tous ports). Réservé à
	// TrustedHostsEnv (messagerie seulement) : ParseAllowlist n'en produit pas.
	Net *net.IPNet
}

// Allowlist est la liste d'exceptions pour NOS propres relais. Une adresse
// interdite n'est autorisée que si (IP, port) y figure exactement : pas de
// plage, pas de joker.
type Allowlist []AllowEntry

// Allows indique si (ip, port) figure dans la liste.
func (a Allowlist) Allows(ip net.IP, port int) bool {
	for _, e := range a {
		if e.Net != nil {
			if e.Net.Contains(ip) {
				return true
			}
			continue
		}
		if (e.Port == port || e.Port == 0) && e.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// ParseAllowlist lit « ip:port,ip:port » (IPv6 : [::1]:993).
func ParseAllowlist(spec string) (Allowlist, error) {
	var out Allowlist
	for _, raw := range strings.Split(spec, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		h, p, err := net.SplitHostPort(raw)
		if err != nil {
			return nil, fmt.Errorf("allowlist entry %q: %w", raw, err)
		}
		ip := net.ParseIP(h)
		port, perr := strconv.Atoi(p)
		if ip == nil || perr != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("allowlist entry %q: need a literal ip:port", raw)
		}
		out = append(out, AllowEntry{IP: ip, Port: port})
	}
	return out, nil
}

// InternalRelayEnv permet d'ajouter un relais sans recompiler (liste ip:port).
const InternalRelayEnv = "NOTIFUSE_OUTBOUND_INTERNAL_ALLOW"

// internalRelays : les relais Veridian joints en Tailscale (100.x), seules
// exceptions de base. IMAP 993 des boîtes de rejet DSN (branchées le 07/10) :
// relais nord, relais agences (dev), relais node02. Source de vérité des IP :
// nomad-v state / tailscale status ; ajouter ici ou via InternalRelayEnv.
var internalRelays = Allowlist{
	{IP: net.ParseIP("100.88.202.29"), Port: 993},
	{IP: net.ParseIP("100.92.215.42"), Port: 993},
	{IP: net.ParseIP("100.85.160.17"), Port: 993},
}

// TrustedHostsEnv : liste d'IP et/ou CIDR séparés par des virgules (tous
// ports) autorisés pour les intégrations de MESSAGERIE (IMAP, SMTP) seulement.
// Vide par défaut : comportement inchangé (fail-closed). Les webhooks et les
// autres sorties n'utilisent jamais cette liste (ils appellent IsBlockedIP
// sans Allowlist ou NewTenantOutboundClient).
const TrustedHostsEnv = "NOTIFUSE_TRUSTED_INTERNAL_HOSTS"

// ParseTrustedHosts lit « ip,cidr,... ». Toute entrée invalide fait échouer
// l'ensemble (l'appelant ignore alors toute la liste).
func ParseTrustedHosts(spec string) (Allowlist, error) {
	var out Allowlist
	for _, raw := range strings.Split(spec, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			_, n, err := net.ParseCIDR(raw)
			if err != nil {
				return nil, fmt.Errorf("trusted host %q: %w", raw, err)
			}
			out = append(out, AllowEntry{Net: n})
			continue
		}
		ip := net.ParseIP(strings.Trim(raw, "[]"))
		if ip == nil {
			return nil, fmt.Errorf("trusted host %q: need a literal ip or cidr", raw)
		}
		bits := 8 * net.IPv6len
		if v4 := ip.To4(); v4 != nil {
			ip, bits = v4, 8*net.IPv4len
		}
		out = append(out, AllowEntry{Net: &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}})
	}
	return out, nil
}

// DefaultAllowlist = relais intégrés + InternalRelayEnv + TrustedHostsEnv
// (entrée invalide ignorée : une exception mal écrite ne doit jamais ouvrir
// plus). Utilisée UNIQUEMENT par SMTP et IMAP, à l'enregistrement ET à chaque
// connexion (anti DNS-rebinding).
func DefaultAllowlist() Allowlist {
	out := append(Allowlist{}, internalRelays...)
	if extra, err := ParseAllowlist(os.Getenv(InternalRelayEnv)); err == nil {
		out = append(out, extra...)
	}
	if extra, err := ParseTrustedHosts(os.Getenv(TrustedHostsEnv)); err == nil {
		out = append(out, extra...)
	}
	return out
}

// CheckAddr tranche une adresse « ip:port » DÉJÀ résolue.
func CheckAddr(addr string, allow Allowlist) error {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid dial address %q: %w", addr, err)
	}
	ip := net.ParseIP(h)
	port, _ := strconv.Atoi(p)
	if ip == nil {
		return fmt.Errorf("refusing to connect to %q: not a resolved IP address", h)
	}
	if IsBlockedIP(ip) && !allow.Allows(ip, port) {
		return fmt.Errorf("refusing to connect to %s: internal/private/link-local/metadata addresses are not permitted (not in the relay allowlist)", addr)
	}
	return nil
}

// Control s'installe dans net.Dialer.Control : appelé par le système pour
// CHAQUE adresse réellement tentée, après résolution. Une adresse interdite
// fait échouer cette tentative (le dialer passe à l'IP suivante : une réponse
// DNS mixte public + interne ne joint que le public).
func Control(allow Allowlist) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		return CheckAddr(address, allow)
	}
}

// GuardDialer pose Control sur d et le renvoie.
func GuardDialer(d *net.Dialer, allow Allowlist) *net.Dialer {
	d.Control = Control(allow)
	return d
}

// ValidateHost est le contrôle À L'ENREGISTREMENT : une IP littérale interdite
// est refusée avec une raison lisible. Pas de DNS ici (périmé à l'envoi) : la
// connexion tranche.
func ValidateHost(host string, port int, allow Allowlist) error {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return nil
	}
	if IsBlockedIP(ip) && !allow.Allows(ip, port) {
		return fmt.Errorf("host %s targets a disallowed address: internal/private/link-local/metadata destinations are not permitted", ip.String())
	}
	return nil
}
