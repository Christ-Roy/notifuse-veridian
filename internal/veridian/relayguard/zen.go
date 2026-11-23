// Package relayguard vérifie, avant d'envoyer du commercial, que l'IP du relais d'un profil n'est
// pas en liste noire Spamhaus ZEN, et suspend le profil si elle l'est (VER-225, 23/11/2026).
//
// Méthode (la même que l'infra pour VER-223) : requête DIRECTE aux serveurs faisant autorité de
// zen.spamhaus.org, en IPv4. Un résolveur public est refusé par Spamhaus (127.255.255.254) : ce
// refus n'est jamais lu comme « non listé ». Chaque serveur est d'abord éprouvé par deux témoins :
// 127.0.0.2 doit répondre listé, 127.0.0.1 doit répondre absent. Un serveur qui rate un témoin,
// refuse ou se tait est écarté ; si aucun n'aboutit, la mesure est NON ABOUTIE et rien ne part.
package relayguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/service/queue"
)

const zone = "zen.spamhaus.org"

// Query interroge UN serveur DNS pour les enregistrements A d'un nom. NXDOMAIN rend une
// *net.DNSError avec IsNotFound.
type Query func(ctx context.Context, server, name string) ([]string, error)

// ZEN est le vérificateur branché sur la porte du worker (queue.RelayChecker).
type ZEN struct {
	ResolveHost    func(ctx context.Context, host string) ([]string, error) // IPv4 du relais
	ResolveServers func(ctx context.Context) ([]string, error)              // IPv4 des serveurs faisant autorité
	Query          Query
	MaxServers     int
}

var _ queue.RelayChecker = (*ZEN)(nil)

// NewZEN : le vérificateur réel (résolution système pour l'hôte et les serveurs, requête directe
// en UDP IPv4 sur le port 53 du serveur faisant autorité, 4 s par question).
func NewZEN() *ZEN {
	return &ZEN{ResolveHost: resolveIPv4, ResolveServers: authoritativeServers, Query: directQuery(4 * time.Second), MaxServers: 3}
}

// CheckRelay mesure toutes les IPv4 de l'hôte du relais.
func (z *ZEN) CheckRelay(ctx context.Context, host string) queue.RelayCheck {
	ips, err := z.ResolveHost(ctx, host)
	if err != nil || len(ips) == 0 {
		return queue.RelayCheck{State: queue.RelayUnverified, Detail: fmt.Sprintf("IP du relais %s introuvable: %v", host, err)}
	}
	servers, err := z.ResolveServers(ctx)
	if err != nil || len(servers) == 0 {
		return queue.RelayCheck{State: queue.RelayUnverified, IPs: ips, Detail: fmt.Sprintf("serveurs de %s introuvables: %v", zone, err)}
	}
	max := z.MaxServers
	if max <= 0 || max > len(servers) {
		max = len(servers)
	}
	var why []string
	for _, srv := range servers[:max] {
		c, reason := z.checkOn(ctx, srv, ips)
		if reason == "" {
			return c
		}
		why = append(why, srv+": "+reason)
	}
	return queue.RelayCheck{State: queue.RelayUnverified, IPs: ips, Detail: "aucun serveur n'a abouti (" + strings.Join(why, " ; ") + ")"}
}

// checkOn : verdict sur un serveur, ou la raison pour laquelle ce serveur ne compte pas.
func (z *ZEN) checkOn(ctx context.Context, srv string, ips []string) (queue.RelayCheck, string) {
	pos, err := z.Query(ctx, srv, "2.0.0.127."+zone+".")
	if err != nil || !contains(pos, "127.0.0.2") {
		return queue.RelayCheck{}, fmt.Sprintf("témoin 127.0.0.2 non listé (%v %v)", pos, err)
	}
	neg, err := z.Query(ctx, srv, "1.0.0.127."+zone+".")
	if !(isNotFound(err) || (err == nil && len(neg) == 0)) {
		return queue.RelayCheck{}, fmt.Sprintf("témoin 127.0.0.1 répond (%v %v)", neg, err)
	}
	var listed []string
	for _, ip := range ips {
		ans, err := z.Query(ctx, srv, reverse(ip)+"."+zone+".")
		if isNotFound(err) || (err == nil && len(ans) == 0) {
			continue
		}
		if err != nil {
			return queue.RelayCheck{}, fmt.Sprintf("%s: %v", ip, err)
		}
		state := classify(ans)
		if state == "refused" {
			return queue.RelayCheck{}, fmt.Sprintf("%s: requête refusée %v", ip, ans)
		}
		if state == "unknown" {
			return queue.RelayCheck{}, fmt.Sprintf("%s: réponse inconnue %v", ip, ans)
		}
		listed = append(listed, ip+"="+strings.Join(ans, "+"))
	}
	if len(listed) > 0 {
		return queue.RelayCheck{State: queue.RelayListed, IPs: ips, Detail: "listée ZEN via " + srv + ": " + strings.Join(listed, ", ")}, ""
	}
	return queue.RelayCheck{State: queue.RelayClean, IPs: ips, Detail: "non listée ZEN via " + srv}, ""
}

// classify : 127.0.0.2 à 127.0.0.11 = listé (SBL, CSS, XBL, PBL) ; 127.255.255.x = refus de
// Spamhaus (résolveur public, erreur de nom, trop de requêtes) ; le reste est inconnu.
func classify(ans []string) string {
	listed := false
	for _, a := range ans {
		ip := net.ParseIP(a).To4()
		switch {
		case ip == nil:
			return "unknown"
		case ip[0] == 127 && ip[1] == 255 && ip[2] == 255:
			return "refused"
		case ip[0] == 127 && ip[1] == 0 && ip[2] == 0 && ip[3] >= 2 && ip[3] <= 11:
			listed = true
		default:
			return "unknown"
		}
	}
	if listed {
		return "listed"
	}
	return "unknown"
}

func reverse(ip string) string {
	p := strings.Split(ip, ".")
	if len(p) != 4 {
		return ip
	}
	return p[3] + "." + p[2] + "." + p[1] + "." + p[0]
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func isNotFound(err error) bool {
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
}

func resolveIPv4(ctx context.Context, host string) ([]string, error) {
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		return []string{ip.String()}, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out, nil
}

func authoritativeServers(ctx context.Context) ([]string, error) {
	nss, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ns := range nss {
		ips, err := resolveIPv4(ctx, strings.TrimSuffix(ns.Host, "."))
		if err == nil && len(ips) > 0 {
			out = append(out, ips[0])
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("aucune IPv4 pour les serveurs de %s", zone)
	}
	return out, nil
}

// directQuery : le résolveur Go envoie la question au serveur faisant autorité lui-même (jamais à
// un résolveur intermédiaire), en UDP IPv4.
func directQuery(timeout time.Duration) Query {
	return func(ctx context.Context, server, name string) ([]string, error) {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, "udp4", net.JoinHostPort(server, "53"))
		}}
		qctx, cancel := context.WithTimeout(ctx, 2*timeout)
		defer cancel()
		ips, err := r.LookupIP(qctx, "ip4", name)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(ips))
		for _, ip := range ips {
			out = append(out, ip.String())
		}
		return out, nil
	}
}
