package domain

import "net"

// Veridian fork — garde-fou du flag SMTPSettings.SkipTLSVerify (cf.
// email_provider_smtp.go). On n'autorise la désactivation de la vérification
// du certificat TLS sortant que vers un host PRIVÉ : un relai interne à cert
// self-signed sur réseau privé chiffré (Tailscale 100.64/10, RFC1918,
// loopback) est un cas légitime ; un host public ne doit JAMAIS pouvoir
// court-circuiter la vérif (MITM, réputation). Un hostname non-IP non
// résolvable ici (ex. nom de service Docker "smtp-sink") est traité comme
// privé : il n'est de toute façon joignable que depuis un réseau interne.

// veridianIsPrivateSMTPHost retourne true si host est une adresse privée
// (Tailscale CGNAT, RFC1918, loopback, link-local) OU un hostname non public
// (résout vers du privé, ou ne résout pas = nom interne Docker/compose).
func veridianIsPrivateSMTPHost(host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return veridianIsPrivateIP(ip)
	}
	// Hostname : si toutes les IP résolues sont privées → privé. Si la
	// résolution échoue (nom de service interne non DNS-résoluble depuis ici),
	// on considère privé : ce n'est joignable que sur un réseau interne.
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		return true
	}
	for _, ip := range addrs {
		if !veridianIsPrivateIP(ip) {
			return false
		}
	}
	return true
}

func veridianIsPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
		return true
	}
	// net.IP.IsPrivate() ne couvre pas le CGNAT 100.64.0.0/10 (plage Tailscale).
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
	}
	return false
}
