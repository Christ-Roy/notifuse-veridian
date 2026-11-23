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
	return &ZEN{ResolveHost: resolveIPv4, ResolveServers: authoritativeServers, Query: directQuery(3 * time.Second), MaxServers: 4}
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
	// Méthode de l'infra (23/11) : le plus sévère des serveurs l'emporte. On recueille DEUX verdicts
	// valides (témoins justes) : listé si l'un dit listé ; propre seulement si deux le disent ; moins
	// de deux serveurs valides = non abouti. Un serveur qui rate un témoin ou se tait est écarté.
	var why []string
	var valid []queue.RelayCheck
	for _, srv := range servers[:max] {
		c, reason := z.checkOn(ctx, srv, ips)
		if reason != "" {
			why = append(why, srv+": "+reason)
			continue
		}
		if c.State == queue.RelayListed {
			return c
		}
		valid = append(valid, c)
		if len(valid) == 2 {
			out := valid[0]
			if out.Warning == "" {
				out.Warning = valid[1].Warning
			}
			out.Detail += " ; confirmé par un second serveur"
			return out
		}
	}
	return queue.RelayCheck{State: queue.RelayUnverified, IPs: ips, Detail: fmt.Sprintf("%d serveur(s) valide(s) sur 2 requis (%s)", len(valid), strings.Join(why, " ; "))}
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
	var listed, pbl []string
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
		if state == "pbl" {
			pbl = append(pbl, ip+"="+strings.Join(ans, "+"))
			continue
		}
		listed = append(listed, ip+"="+strings.Join(ans, "+"))
	}
	if len(listed) > 0 {
		return queue.RelayCheck{State: queue.RelayListed, IPs: ips, Detail: "listée ZEN via " + srv + ": " + strings.Join(listed, ", ")}, ""
	}
	c := queue.RelayCheck{State: queue.RelayClean, IPs: ips, Detail: "non listée ZEN via " + srv}
	if len(pbl) > 0 {
		c.Warning = "PBL seulement : " + strings.Join(pbl, ", ")
	}
	return c, ""
}

// classify : 127.0.0.2 à 127.0.0.9 = listé (SBL 2-3, CSS 3, XBL 4-7) ; 127.0.0.10 et .11 seuls =
// PBL (plages « grand public », ne vise pas un relais légitime : avertissement, l'envoi continue) ;
// 127.255.255.x = refus de Spamhaus (erreur de requête, résolveur ouvert, limite) ; le reste est inconnu.
func classify(ans []string) string {
	listed, pbl := false, false
	for _, a := range ans {
		ip := net.ParseIP(a).To4()
		switch {
		case ip == nil:
			return "unknown"
		case ip[0] == 127 && ip[1] == 255 && ip[2] == 255:
			return "refused"
		case ip[0] == 127 && ip[1] == 0 && ip[2] == 0 && ip[3] >= 2 && ip[3] <= 9:
			listed = true
		case ip[0] == 127 && ip[1] == 0 && ip[2] == 0 && (ip[3] == 10 || ip[3] == 11):
			pbl = true
		default:
			return "unknown"
		}
	}
	switch {
	case listed:
		return "listed"
	case pbl:
		return "pbl"
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

// gnsHosts : les serveurs faisant autorité de zen.spamhaus.org (NS relevés le 23/11/2026).
var gnsHosts = []string{"a.gns.spamhaus.org", "b.gns.spamhaus.org", "c.gns.spamhaus.org", "d.gns.spamhaus.org", "e.gns.spamhaus.org"}

// pinnedServers : IPv4 de ces serveurs relevées le 23/11/2026, en dernier recours. Prod : le
// résolveur du conteneur (OVH, 213.186.33.99) refuse les noms de Spamhaus (« no such host »), comme
// tout gros résolveur public. Une adresse périmée n'est jamais crue : les témoins l'écartent.
var pinnedServers = []string{"185.82.216.38", "147.102.226.131", "88.198.122.239", "45.90.57.113", "208.85.20.147", "85.94.194.12"}

// authoritativeServers : NS de la zone, sinon les noms connus, puis les adresses épinglées ; sans
// doublon, dans cet ordre. Ne rend une erreur que si la liste est vide (jamais en pratique).
func authoritativeServers(ctx context.Context) ([]string, error) {
	return serverList(ctx, func(ctx context.Context) ([]string, error) {
		nss, err := net.DefaultResolver.LookupNS(ctx, zone)
		if err != nil {
			return nil, err
		}
		var hosts []string
		for _, ns := range nss {
			hosts = append(hosts, strings.TrimSuffix(ns.Host, "."))
		}
		return hosts, nil
	}, resolveIPv4)
}

func serverList(ctx context.Context, lookupNS func(context.Context) ([]string, error), resolve func(context.Context, string) ([]string, error)) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(ip string) {
		if ip != "" && !seen[ip] {
			seen[ip] = true
			out = append(out, ip)
		}
	}
	hosts, _ := lookupNS(ctx)
	if len(hosts) == 0 {
		hosts = gnsHosts
	}
	for _, h := range hosts {
		if ips, err := resolve(ctx, h); err == nil && len(ips) > 0 {
			add(ips[0])
		}
	}
	for _, ip := range pinnedServers {
		add(ip)
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
