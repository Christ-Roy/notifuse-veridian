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
	for _, tc := range []struct {
		name string
		dns  fakeDNS
		srv  []string
		want queue.RelayState
	}{
		{"listée", fakeDNS{"a": healthy(map[string][]string{"44.7.210.51.zen.spamhaus.org.": {"127.0.0.2"}})}, []string{"a"}, queue.RelayListed},
		{"PBL listée aussi", fakeDNS{"a": healthy(map[string][]string{"44.7.210.51.zen.spamhaus.org.": {"127.0.0.11"}})}, []string{"a"}, queue.RelayListed},
		{"propre", fakeDNS{"a": healthy(nil)}, []string{"a"}, queue.RelayClean},
		{"refus résolveur public, jamais lu comme propre", fakeDNS{"a": healthy(map[string][]string{"44.7.210.51.zen.spamhaus.org.": {"127.255.255.254"}})}, []string{"a"}, queue.RelayUnverified},
		{"témoin positif faux", fakeDNS{"a": {}}, []string{"a"}, queue.RelayUnverified},
		{"témoin négatif répond", fakeDNS{"a": healthy(map[string][]string{"1.0.0.127.zen.spamhaus.org.": {"127.0.0.2"}})}, []string{"a"}, queue.RelayUnverified},
		{"serveur muet", fakeDNS{}, []string{"a"}, queue.RelayUnverified},
		{"premier serveur muet, second répond", fakeDNS{"b": healthy(map[string][]string{"44.7.210.51.zen.spamhaus.org.": {"127.0.0.3"}})}, []string{"a", "b"}, queue.RelayListed},
		{"premier serveur refuse, second propre", fakeDNS{
			"a": healthy(map[string][]string{"44.7.210.51.zen.spamhaus.org.": {"127.255.255.254"}}),
			"b": healthy(nil)}, []string{"a", "b"}, queue.RelayClean},
		{"réponse inconnue", fakeDNS{"a": healthy(map[string][]string{"44.7.210.51.zen.spamhaus.org.": {"10.0.0.1"}})}, []string{"a"}, queue.RelayUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newZ(tc.dns, tc.srv...).CheckRelay(context.Background(), "smtp.messagerie-nord-776.fr")
			if got.State != tc.want {
				t.Fatalf("état %v, attendu %v (%s)", got.State, tc.want, got.Detail)
			}
		})
	}
}

func TestZEN_UnresolvableHostOrServersIsUnverified(t *testing.T) {
	z := newZ(fakeDNS{"a": healthy(nil)}, "a")
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
	if classify([]string{"127.0.0.4"}) != "listed" || classify([]string{"127.255.255.252"}) != "refused" || classify([]string{"127.0.0.1"}) != "unknown" {
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
}
