package imapgate

import (
	"context"
	"net"
	"net/netip"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/emersion/go-imap/v2"
)

type addrConn struct {
	net.Conn
	addr   net.Addr
	closed atomic.Bool
}

func (c *addrConn) RemoteAddr() net.Addr { return c.addr }
func (c *addrConn) Close() error         { c.closed.Store(true); return nil }

// fakeListener sert des connexions préparées puis bloque.
type fakeListener struct {
	conns chan net.Conn
}

func (l *fakeListener) Accept() (net.Conn, error) {
	c, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}
func (l *fakeListener) Close() error   { close(l.conns); return nil }
func (l *fakeListener) Addr() net.Addr { return &net.TCPAddr{} }

func tcp(ip string) *addrConn {
	return &addrConn{addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 40000}}
}

// Au-delà du plafond, une adresse voit ses connexions coupées ; les autres passent ; fermer une connexion rend sa place ;
// en IPv6 le /64 entier partage un seul plafond.
func TestLimitListener_PerIPCap(t *testing.T) {
	fl := &fakeListener{conns: make(chan net.Conn, 64)}
	ln := LimitListener(fl, 2)
	a1, a2, a3 := tcp("203.0.113.1"), tcp("203.0.113.1"), tcp("203.0.113.1")
	b1 := tcp("203.0.113.2")
	for _, c := range []*addrConn{a1, a2, a3, b1} {
		fl.conns <- c
	}
	got := []net.Conn{}
	for i := 0; i < 3; i++ {
		c, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, c)
	}
	if got[0].RemoteAddr().String() != a1.addr.String() || got[2].RemoteAddr().String() != b1.addr.String() {
		t.Fatalf("a1, a2 puis b1 attendus : %v", got)
	}
	if !a3.closed.Load() {
		t.Fatal("la troisième connexion d'une même adresse doit être coupée")
	}
	if a1.closed.Load() || b1.closed.Load() {
		t.Fatal("les connexions sous le plafond ne sont pas coupées")
	}
	_ = got[0].Close()
	a4 := tcp("203.0.113.1")
	fl.conns <- a4
	c, err := ln.Accept()
	if err != nil || a4.closed.Load() {
		t.Fatalf("une place rendue doit resservir : %v", err)
	}
	_ = c.Close()

	// IPv6 : changer d'adresse dans le /64 ne donne pas un compteur neuf.
	v6 := []*addrConn{tcp("2a02:c207:1:2::1"), tcp("2a02:c207:1:2::2"), tcp("2a02:c207:1:2::3"), tcp("2a02:c207:1:3::1")}
	for _, c := range v6 {
		fl.conns <- c
	}
	for i := 0; i < 3; i++ {
		if _, err := ln.Accept(); err != nil {
			t.Fatal(err)
		}
	}
	if !v6[2].closed.Load() || v6[3].closed.Load() {
		t.Fatalf("même /64 plafonné, autre /64 libre : %v %v", v6[2].closed.Load(), v6[3].closed.Load())
	}
	// perIP <= 0 : aucune borne.
	if LimitListener(fl, 0) != net.Listener(fl) {
		t.Fatal("sans plafond, l'écoute est rendue telle quelle")
	}
}

func TestParseCIDRs(t *testing.T) {
	p, err := ParseCIDRs(" 10.0.0.0/8, 172.26.64.1 ,2a02:c207::/32 ")
	if err != nil || len(p) != 3 || p[0] != netip.MustParsePrefix("10.0.0.0/8") || p[1] != netip.MustParsePrefix("172.26.64.1/32") || p[2] != netip.MustParsePrefix("2a02:c207::/32") {
		t.Fatalf("%v %v", p, err)
	}
	if p, err := ParseCIDRs(""); err != nil || len(p) != 0 {
		t.Fatalf("vide : %v %v", p, err)
	}
	if _, err := ParseCIDRs("10.0.0.0/33"); err == nil {
		t.Fatal("préfixe invalide accepté")
	}
	if _, err := ParseCIDRs("pas-une-adresse"); err == nil {
		t.Fatal("texte accepté")
	}
}

// copyBoxes rend des copies neuves des messages et mesure, à chaque lecture, combien des précédentes ont été libérées : une
// recherche qui garde tous les messages jusqu'à la fin n'en libère aucune.
type copyBoxes struct {
	Boxes
	made, freed atomic.Int64
	peakFreed   atomic.Int64
}

func (c *copyBoxes) BoxMessages(ctx context.Context, ws, profile string, ids []string) (map[string]*mailbox.Message, error) {
	runtime.GC()
	time.Sleep(3 * time.Millisecond)
	if f := c.freed.Load(); f > c.peakFreed.Load() {
		c.peakFreed.Store(f)
	}
	ms, err := c.Boxes.BoxMessages(ctx, ws, profile, ids)
	out := make(map[string]*mailbox.Message, len(ms))
	for id, m := range ms {
		cp := new(mailbox.Message)
		*cp = *m
		c.made.Add(1)
		runtime.SetFinalizer(cp, func(*mailbox.Message) { c.freed.Add(1) })
		out[id] = cp
	}
	return out, err
}

// SEARCH BODY traite par lots : les messages d'un lot déjà examiné ne sont pas gardés (1500 messages entiers en mémoire par
// recherche, et par connexion, étaient un levier de saturation).
func TestSearch_BodyDoesNotKeepEveryMessage(t *testing.T) {
	b := newBench(t)
	for i := 1; i <= searchBodyMax+100; i++ {
		body := "rien"
		if i == 1 || i == searchBodyMax+100 {
			body = "aiguille"
		}
		b.boxes.add(int64(i), "pJC", mailbox.BoxInbox, "Sujet", body, true)
	}
	cb := &copyBoxes{Boxes: b.boxes}
	b.srv.cfg.Boxes = cb
	c := b.login(t, "jc@boite.fr")
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	d, err := c.UIDSearch(&imap.SearchCriteria{Body: []string{"aiguille"}}, nil).Wait()
	if err != nil {
		t.Fatal(err)
	}
	// Le plus ancien (hors fenêtre de recherche dans le texte) n'est pas cherché ; le plus récent l'est.
	if got := d.AllUIDs(); len(got) != 1 || got[0] != imap.UID(searchBodyMax+100) {
		t.Fatalf("résultat : %v", got)
	}
	if cb.made.Load() != searchBodyMax {
		t.Fatalf("messages lus : %d, attendu %d", cb.made.Load(), searchBodyMax)
	}
	if cb.peakFreed.Load() < searchBodyMax/2 {
		t.Fatalf("les lots examinés sont gardés en mémoire : %d libérés en cours de recherche", cb.peakFreed.Load())
	}
}
