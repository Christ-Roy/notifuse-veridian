package module

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func TestCapabilityIsRegistered(t *testing.T) {
	for _, c := range platform.Default.List() {
		if c.Name == "mx" && c.Wire != nil && c.Start != nil && c.Stop != nil {
			return
		}
	}
	t.Fatal("la capacité mx n'est pas enregistrée")
}

func TestReadSettings(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if s, err := ReadSettings(env(nil)); err != nil || len(s.Domains) != 0 {
		t.Fatalf("éteint par défaut : %+v %v", s, err)
	}
	s, err := ReadSettings(env(map[string]string{"VERIDIAN_MX_DOMAINS": "a.fr, b.fr,", "VERIDIAN_MX_HOST": "mx.test", "VERIDIAN_MX_PORT": "3525", "VERIDIAN_MX_PROXY_PROTOCOL": "true"}))
	if err != nil || len(s.Domains) != 2 || s.Port != 3525 || !s.PROXYv2 {
		t.Fatalf("%+v %v", s, err)
	}
	for _, m := range []map[string]string{{"VERIDIAN_MX_DOMAINS": "a.fr"}, {"VERIDIAN_MX_PORT": "x"}} {
		if _, err := ReadSettings(env(m)); err == nil {
			t.Fatalf("%v accepté", m)
		}
	}
}

type checker struct {
	n int
	c queue.RelayCheck
}

func (c *checker) CheckRelay(context.Context, string) queue.RelayCheck { c.n++; return c.c }

func TestListed(t *testing.T) {
	for name, tc := range map[string]struct {
		ip   string
		c    queue.RelayCheck
		want bool
		n    int
	}{
		"listée":                 {"203.0.113.1", queue.RelayCheck{State: queue.RelayListed}, true, 1},
		"PBL":                    {"203.0.113.1", queue.RelayCheck{State: queue.RelayClean, Warning: "PBL"}, true, 1},
		"propre":                 {"203.0.113.1", queue.RelayCheck{State: queue.RelayClean}, false, 1},
		"non abouti : on reçoit": {"203.0.113.1", queue.RelayCheck{State: queue.RelayUnverified}, false, 2},
		"réseau privé":           {"10.0.0.1", queue.RelayCheck{State: queue.RelayListed}, false, 0},
	} {
		ck := &checker{c: tc.c}
		z := &zen{check: ck, cache: map[string]verdict{}}
		a, _ := z.Listed(context.Background(), tc.ip)
		b, _ := z.Listed(context.Background(), tc.ip)
		if a != tc.want || b != tc.want || ck.n != tc.n {
			t.Errorf("%s : %v %v, %d mesures", name, a, b, ck.n)
		}
	}
}

func TestStartStop_WithoutServer(t *testing.T) {
	server = nil
	if start(context.Background(), nil) != nil || stop(context.Background()) != nil {
		t.Fatal("sans domaine, rien à faire")
	}
}
