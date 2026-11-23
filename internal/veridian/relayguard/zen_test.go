package relayguard

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/internal/service/queue"
)

var nx = &net.DNSError{Err: "no such host", IsNotFound: true}

// fakeDNS : réponses par serveur puis par nom ; un nom absent = NXDOMAIN.
type fakeDNS map[string]map[string][]string

func (f fakeDNS) query(_ context.Context, srv, name string) ([]string, error) {
	s, ok := f[srv]
	if !ok {
		return nil, errors.New("i/o timeout")
	}
	if a, ok := s[name]; ok {
		return a, nil
	}
	return nil, nx
}

func healthy(extra map[string][]string) map[string][]string {
	m := map[string][]string{"2.0.0.127.zen.spamhaus.org.": {"127.0.0.2", "127.0.0.10", "127.0.0.4"}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func newZ(dns fakeDNS, servers ...string) *ZEN {
	return &ZEN{
		ResolveHost:    func(context.Context, string) ([]string, error) { return []string{"51.210.7.44"}, nil },
		ResolveServers: func(context.Context) ([]string, error) { return servers, nil },
		Query:          dns.query,
	}
}

func TestZEN_ListedCleanAndControls(t *testing.T) {
	ip := "44.7.210.51.zen.spamhaus.org."
	listedSBL := healthy(map[string][]string{ip: {"127.0.0.2"}})
	refused := healthy(map[string][]string{ip: {"127.255.255.254"}})
	for _, tc := range []struct {
		name string
		dns  fakeDNS
		srv  []string
		want queue.RelayState
		warn bool
	}{
		{"listée (SBL), dès le premier serveur", fakeDNS{"a": listedSBL}, []string{"a", "b"}, queue.RelayListed, false},
		{"listée XBL", fakeDNS{"a": healthy(map[string][]string{ip: {"127.0.0.4"}})}, []string{"a"}, queue.RelayListed, false},
		{"propre sur deux serveurs", fakeDNS{"a": healthy(nil), "b": healthy(nil)}, []string{"a", "b"}, queue.RelayClean, false},
		{"propre sur un seul serveur valide : non abouti", fakeDNS{"a": healthy(nil)}, []string{"a", "b"}, queue.RelayUnverified, false},
		{"désaccord : le plus sévère l'emporte", fakeDNS{"a": healthy(nil), "b": listedSBL}, []string{"a", "b"}, queue.RelayListed, false},
		{"PBL seulement : avertissement, l'envoi continue", fakeDNS{"a": healthy(map[string][]string{ip: {"127.0.0.11"}}), "b": healthy(map[string][]string{ip: {"127.0.0.10"}})}, []string{"a", "b"}, queue.RelayClean, true},
		{"refus (résolveur ouvert), jamais lu comme propre", fakeDNS{"a": refused, "b": refused}, []string{"a", "b"}, queue.RelayUnverified, false},
		{"refus sur l'un, deux autres propres", fakeDNS{"a": refused, "b": healthy(nil), "c": healthy(nil)}, []string{"a", "b", "c"}, queue.RelayClean, false},
		{"témoin positif faux", fakeDNS{"a": {}, "b": {}}, []string{"a", "b"}, queue.RelayUnverified, false},
		{"témoin négatif répond (résolveur ouvert en IPv6)", fakeDNS{"a": healthy(map[string][]string{"1.0.0.127.zen.spamhaus.org.": {"127.255.255.254"}}), "b": healthy(nil)}, []string{"a", "b"}, queue.RelayUnverified, false},
		{"serveurs muets", fakeDNS{}, []string{"a", "b"}, queue.RelayUnverified, false},
		{"réponse inconnue", fakeDNS{"a": healthy(map[string][]string{ip: {"10.0.0.1"}}), "b": healthy(nil)}, []string{"a", "b"}, queue.RelayUnverified, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newZ(tc.dns, tc.srv...).CheckRelay(context.Background(), "smtp.messagerie-nord-776.fr")
			if got.State != tc.want || (got.Warning != "") != tc.warn {
				t.Fatalf("état %v avertissement %q, attendu %v/%v (%s)", got.State, got.Warning, tc.want, tc.warn, got.Detail)
			}
		})
	}
}

func TestZEN_UnresolvableHostOrServersIsUnverified(t *testing.T) {
	z := newZ(fakeDNS{"a": healthy(nil), "b": healthy(nil)}, "a", "b")
	z.ResolveHost = func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	if c := z.CheckRelay(context.Background(), "x"); c.State != queue.RelayUnverified {
		t.Fatalf("hôte introuvable : %v", c.State)
	}
	z = newZ(fakeDNS{"a": healthy(nil)})
	if c := z.CheckRelay(context.Background(), "x"); c.State != queue.RelayUnverified {
		t.Fatalf("aucun serveur : %v", c.State)
	}
}

func TestReverseAndClassify(t *testing.T) {
	if reverse("51.210.7.44") != "44.7.210.51" {
		t.Fatal(reverse("51.210.7.44"))
	}
	if classify([]string{"127.0.0.4"}) != "listed" || classify([]string{"127.0.0.10", "127.0.0.11"}) != "pbl" ||
		classify([]string{"127.0.0.2", "127.0.0.10"}) != "listed" || classify([]string{"127.255.255.252"}) != "refused" || classify([]string{"127.0.0.1"}) != "unknown" {
		t.Fatal("classement des réponses ZEN")
	}
}

// Contre le vrai Spamhaus (VERIDIAN_TEST_NETWORK=1) : la méthode complète, avec ses témoins.
// 127.0.0.2 est l'adresse d'épreuve officielle, toujours listée ; elle doit sortir « listée ».
func TestZEN_LiveAgainstSpamhaus(t *testing.T) {
	if os.Getenv("VERIDIAN_TEST_NETWORK") != "1" {
		t.Skip("VERIDIAN_TEST_NETWORK=1 pour interroger Spamhaus")
	}
	z := NewZEN()
	if c := z.CheckRelay(context.Background(), "127.0.0.2"); c.State != queue.RelayListed {
		t.Fatalf("l'adresse d'épreuve 127.0.0.2 doit sortir listée : %v %s", c.State, c.Detail)
	}
	c := z.CheckRelay(context.Background(), "smtp.messagerie-nord-776.fr")
	t.Logf("relais nord : état %v, %s, %s", c.State, strings.Join(c.IPs, ","), c.Detail)
	if c.State == queue.RelayUnverified {
		t.Fatalf("mesure non aboutie depuis ce poste : %s", c.Detail)
	}
	// Même mesure avec un résolveur qui refuse les noms de Spamhaus (cas du conteneur prod) :
	// seules les adresses épinglées servent, et la méthode aboutit quand même.
	z.ResolveServers = func(ctx context.Context) ([]string, error) {
		return serverList(ctx, func(context.Context) ([]string, error) { return nil, errors.New("no such host") },
			func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") })
	}
	if c := z.CheckRelay(context.Background(), "127.0.0.2"); c.State != queue.RelayListed {
		t.Fatalf("adresses épinglées : l'adresse d'épreuve doit sortir listée : %v %s", c.State, c.Detail)
	}
}

// Prod, 23/11 : le résolveur du conteneur refuse les noms de Spamhaus. La liste des serveurs ne
// dépend pas de lui : noms connus, puis adresses épinglées (que les témoins valideront).
func TestServerList_SurvivesARefusingResolver(t *testing.T) {
	refused := func(context.Context) ([]string, error) {
		return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
	}
	noResolve := func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	got, err := serverList(context.Background(), refused, noResolve)
	if err != nil || len(got) != len(pinnedServers) || got[0] != pinnedServers[0] {
		t.Fatalf("résolveur qui refuse tout : on doit retomber sur les adresses épinglées, %v %v", got, err)
	}
	resolveSome := func(_ context.Context, h string) ([]string, error) {
		if h == "a.gns.spamhaus.org" {
			return []string{"9.9.9.9"}, nil
		}
		return nil, errors.New("no such host")
	}
	got, _ = serverList(context.Background(), refused, resolveSome)
	if got[0] != "9.9.9.9" || len(got) != len(pinnedServers)+1 {
		t.Fatalf("un nom résolu passe d'abord, les épinglées suivent sans doublon : %v", got)
	}
}
