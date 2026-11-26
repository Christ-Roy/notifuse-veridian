package imapgate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/ratelimiter"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// ---------- doublures ----------

type fakeAuth struct {
	mu      sync.Mutex
	accs    map[string]*boxaccess.Access // "adresse|secret"
	revoked map[string]bool
	// revokeAfterVerify : le retrait tombe entre la lecture du mot de passe et l'inscription de la connexion.
	revokeAfterVerify bool
}

func (f *fakeAuth) Verify(_ context.Context, a, s string) (*boxaccess.Access, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if x := f.accs[a+"|"+s]; x != nil && !f.revoked[x.ID] {
		f.revoked[x.ID] = f.revokeAfterVerify
		return x, nil
	}
	return nil, boxaccess.ErrAuth
}
func (f *fakeAuth) Alive(_ context.Context, id string) (*boxaccess.Access, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revoked[id] {
		return nil, boxaccess.ErrAuth
	}
	return &boxaccess.Access{ID: id}, nil
}
func (f *fakeAuth) Touch(context.Context, string) error { return nil }

type fakeMsg struct {
	m      *mailbox.Message
	folder mailbox.BoxFolder
	flags  []string
	hidden bool
	purged bool
}

// fakeBoxes reproduit le contrat du dépôt, cloisonnement par profil compris.
type fakeBoxes struct {
	mu    sync.Mutex
	msgs  map[string]*fakeMsg
	calls int // lectures et écritures de la boîte
}

func (b *fakeBoxes) reset() { b.mu.Lock(); defer b.mu.Unlock(); b.calls = 0 }
func (b *fakeBoxes) touched() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func (b *fakeBoxes) add(seq int64, profile string, folder mailbox.BoxFolder, subject, body string, raw bool) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := fmt.Sprintf("m%d", seq)
	m := &mailbox.Message{ID: id, Seq: seq, ProfileID: profile, Subject: subject, From: "prospect@client.fr", FromName: "Élodie",
		To: []string{"jc@boite.fr"}, BodyText: body, BodySource: "captured", MessageID: "<" + id + "@boite.fr>", OccurredAt: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC),
		Headers: map[string][]string{"Subject": {subject}, "From": {"prospect@client.fr"}}}
	if raw {
		m.RawEML = []byte("From: prospect@client.fr\r\nTo: jc@boite.fr\r\nSubject: " + subject + "\r\nMessage-Id: <" + id + "@client.fr>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n")
	} else {
		m.BodyHTMLSanitized = "<p>" + body + "</p>"
	}
	b.msgs[id] = &fakeMsg{m: m, folder: folder}
	return id
}

func (b *fakeBoxes) BoxIndex(_ context.Context, _, profile string, folder mailbox.BoxFolder) ([]mailbox.BoxEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	var out []mailbox.BoxEntry
	for _, x := range b.msgs {
		in := x.folder == folder && !x.hidden
		if folder == mailbox.BoxTrash {
			in = x.hidden && !x.purged
		}
		if x.m.ProfileID != profile || !in {
			continue
		}
		out = append(out, mailbox.BoxEntry{Seq: x.m.Seq, ID: x.m.ID, At: x.m.OccurredAt, Size: int64(len(x.m.RawEML)), Flags: append([]string{}, x.flags...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (b *fakeBoxes) BoxMessages(_ context.Context, _, profile string, ids []string) (map[string]*mailbox.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	out := map[string]*mailbox.Message{}
	for _, id := range ids {
		if x := b.msgs[id]; x != nil && x.m.ProfileID == profile {
			out[id] = x.m
		}
	}
	return out, nil
}

func (b *fakeBoxes) BoxSetFlags(_ context.Context, _, profile, id string, flags []string, hidden *bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if x := b.msgs[id]; x != nil && x.m.ProfileID == profile {
		x.flags = append([]string{}, flags...)
		if hidden != nil {
			x.hidden = *hidden
		}
	}
	return nil
}

func (b *fakeBoxes) BoxPurge(_ context.Context, _, profile, id string, flags []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if x := b.msgs[id]; x != nil && x.m.ProfileID == profile {
		x.purged, x.hidden = true, true
	}
	return nil
}

func (b *fakeBoxes) BoxSentKnown(_ context.Context, _, profile string, ids []string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	for _, x := range b.msgs {
		for _, id := range ids {
			if x.m.ProfileID == profile && x.folder == mailbox.BoxSent && x.m.MessageID == id {
				return true, nil
			}
		}
	}
	return false, nil
}

// ---------- banc ----------

func selfSigned(t *testing.T) *tls.Config {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "boite.test"}, NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"boite.test"}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: k}}}
}

type bench struct {
	addr  string
	auth  *fakeAuth
	boxes *fakeBoxes
	srv   *Server
}

// startedListener annonce le premier Accept : Serve a fini son démarrage.
type startedListener struct {
	net.Listener
	once  sync.Once
	ready chan struct{}
}

func (l *startedListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept()
}

func newBench(t *testing.T) *bench {
	t.Helper()
	auth := &fakeAuth{accs: map[string]*boxaccess.Access{
		"jc@boite.fr|bon":    {ID: "a1", WorkspaceID: "ws", ProfileID: "pJC", Address: "jc@boite.fr"},
		"autre@boite.fr|bon": {ID: "a2", WorkspaceID: "ws", ProfileID: "pAutre", Address: "autre@boite.fr"},
	}, revoked: map[string]bool{}}
	boxes := &fakeBoxes{msgs: map[string]*fakeMsg{}}
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy(LimitIP, TriesPerIP, LimitWindow)
	rl.SetPolicy(LimitAddress, TriesPerAddr, LimitWindow)
	t.Cleanup(rl.Stop)
	srv, err := New(Config{TLSConfig: selfSigned(t), Auth: auth, Boxes: boxes, Limiter: rl, Logger: logger.NewLogger()})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := &startedListener{Listener: ln, ready: make(chan struct{})}
	go func() { _ = srv.Serve(started) }()
	<-started.ready // Serve a pris la main : fermer avant serait une course
	t.Cleanup(func() { _ = srv.Close() })
	return &bench{addr: ln.Addr().String(), auth: auth, boxes: boxes, srv: srv}
}

func (b *bench) dial(t *testing.T) *imapclient.Client {
	t.Helper()
	c, err := imapclient.DialTLS(b.addr, &imapclient.Options{TLSConfig: &tls.Config{InsecureSkipVerify: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (b *bench) login(t *testing.T, user string) *imapclient.Client {
	t.Helper()
	c := b.dial(t)
	if err := c.Login(user, "bon").Wait(); err != nil {
		t.Fatalf("connexion: %v", err)
	}
	return c
}

// ---------- épreuves ----------

func TestNew_RefusesCleartext(t *testing.T) {
	if _, err := New(Config{Auth: &fakeAuth{}, Boxes: &fakeBoxes{}, Logger: logger.NewLogger()}); err == nil {
		t.Fatal("un serveur sans TLS ni entrée TLS devant doit être refusé")
	}
}

func TestListenAndServe_AddressTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	b := newBench(t)
	srv, err := New(Config{Addr: ln.Addr().String(), TLSConfig: selfSigned(t), Auth: b.auth, Boxes: b.boxes, Limiter: b.srv.cfg.Limiter, Logger: logger.NewLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ListenAndServe(); err == nil || !strings.Contains(err.Error(), "écoute") {
		t.Fatalf("port pris : %v", err)
	}
}

func TestLogin_WrongPasswordAndRateLimit(t *testing.T) {
	b := newBench(t)
	var last error
	for i := 0; i < TriesPerAddr+1; i++ {
		c := b.dial(t)
		last = c.Login("jc@boite.fr", "faux").Wait()
		if last == nil {
			t.Fatal("mauvais mot de passe accepté")
		}
		_ = c.Close()
	}
	if strings.ContainsAny(last.Error(), "éèàê") {
		t.Fatalf("une reponse IMAP est en ASCII : %v", last)
	}
	if !strings.Contains(last.Error(), "trop d'essais") {
		t.Fatalf("essais répétés non bornés: %v", last)
	}
	// Le bon mot de passe ne passe plus depuis cette adresse IP pour cette boîte, les autres boîtes passent.
	if err := b.dial(t).Login("jc@boite.fr", "bon").Wait(); err == nil {
		t.Fatal("la limite doit tenir même avec le bon mot de passe")
	}
	b.login(t, "autre@boite.fr")
}

func TestFolders_ScopedToProfile(t *testing.T) {
	b := newBench(t)
	b.boxes.add(10, "pJC", mailbox.BoxInbox, "Réponse à JC", "oui", true)
	secret := b.boxes.add(11, "pAutre", mailbox.BoxInbox, "Secret autre profil", "non", true)
	b.boxes.add(12, "pJC", mailbox.BoxRejects, "Undelivered", "dsn", true)
	b.boxes.add(13, "pJC", mailbox.BoxSent, "Notre envoi", "bonjour", false)
	c := b.login(t, "jc@boite.fr")

	ls, err := c.List("", "*", nil).Collect()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range ls {
		names = append(names, l.Mailbox)
	}
	if strings.Join(names, ",") != "INBOX,Rejets,Sent,Trash" {
		t.Fatalf("dossiers: %v", names)
	}
	for folder, want := range map[string]uint32{"INBOX": 1, "Rejets": 1, "Sent": 1, "Trash": 0} {
		d, err := c.Select(folder, nil).Wait()
		if err != nil || d.NumMessages != want {
			t.Fatalf("%s: %v %v", folder, d, err)
		}
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	// L'UID d'un message d'un autre profil ne rend rien.
	got, err := c.Fetch(imap.UIDSetNum(11), &imap.FetchOptions{Envelope: true, BodySection: []*imap.FetchItemBodySection{{}}}).Collect()
	if err != nil || len(got) != 0 {
		t.Fatalf("fuite entre profils: %v %v (%s)", got, err, secret)
	}
	if _, err := c.Select("Inconnu", nil).Wait(); err == nil {
		t.Fatal("dossier inconnu accepté")
	}
	if err := c.Create("Nouveau", nil).Wait(); err == nil {
		t.Fatal("créer un dossier doit être refusé")
	}
}

func TestFetch_BodySizeAndSeen(t *testing.T) {
	b := newBench(t)
	id := b.boxes.add(10, "pJC", mailbox.BoxInbox, "Re: Gravure", "Oui, rappelez-moi", true)
	sent := b.boxes.add(13, "pJC", mailbox.BoxSent, "Gravure laser", "bonjour", false)
	c := b.login(t, "jc@boite.fr")
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	sec := &imap.FetchItemBodySection{}
	got, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{UID: true, Flags: true, RFC822Size: true, Envelope: true, BodyStructure: &imap.FetchItemBodyStructure{}, BodySection: []*imap.FetchItemBodySection{{Peek: true}}}).Collect()
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	m := got[0]
	body := m.FindBodySection(&imap.FetchItemBodySection{})
	if body == nil || !strings.Contains(string(body), "rappelez-moi") || int64(len(body)) != m.RFC822Size || m.UID != 10 || m.Envelope.Subject != "Re: Gravure" {
		t.Fatalf("message mal rendu: taille %d, octets %d, %+v", m.RFC822Size, len(body), m.Envelope)
	}
	if len(b.boxes.msgs[id].flags) != 0 {
		t.Fatal("une lecture PEEK ne marque pas lu")
	}
	if _, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{sec}}).Collect(); err != nil {
		t.Fatal(err)
	}
	if f := b.boxes.msgs[id].flags; len(f) != 1 || f[0] != string(imap.FlagSeen) {
		t.Fatalf("lecture non marquée: %v", f)
	}
	// Un envoi reconstitué a la même taille annoncée et lue, deux fois de suite.
	if _, err := c.Select("Sent", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		sz, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{RFC822Size: true}).Collect()
		if err != nil || len(sz) != 1 {
			t.Fatal(err)
		}
		full, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{{Peek: true}}}).Collect()
		if err != nil || int64(len(full[0].FindBodySection(&imap.FetchItemBodySection{}))) != sz[0].RFC822Size || sz[0].RFC822Size == 0 {
			t.Fatalf("taille instable pour %s: %v %v", sent, sz, err)
		}
	}
}

func TestStoreMoveExpunge_NothingIsDeleted(t *testing.T) {
	b := newBench(t)
	one := b.boxes.add(10, "pJC", mailbox.BoxInbox, "Un", "a", true)
	two := b.boxes.add(11, "pJC", mailbox.BoxInbox, "Deux", "b", true)
	c := b.login(t, "jc@boite.fr")
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(imap.SeqSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagFlagged}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if f := b.boxes.msgs[one].flags; len(f) != 1 || f[0] != string(imap.FlagFlagged) {
		t.Fatalf("drapeau: %v", f)
	}
	if _, err := c.Move(imap.SeqSetNum(1), "Trash").Wait(); err != nil {
		t.Fatal(err)
	}
	if !b.boxes.msgs[one].hidden {
		t.Fatal("déplacement vers la corbeille sans effet")
	}
	if err := c.Store(imap.SeqSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Expunge().Collect(); err != nil {
		t.Fatal(err)
	}
	if !b.boxes.msgs[two].hidden || len(b.boxes.msgs) != 2 {
		t.Fatal("la purge masque, elle ne supprime jamais")
	}
	d, err := c.Select("Trash", nil).Wait()
	if err != nil || d.NumMessages != 2 {
		t.Fatalf("corbeille: %v %v", d, err)
	}
	if _, err := c.Move(imap.UIDSetNum(10), "INBOX").Wait(); err != nil {
		t.Fatal(err)
	}
	if b.boxes.msgs[one].hidden {
		t.Fatal("restauration sans effet")
	}
	// Lecture seule : aucune modification.
	if _, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(imap.SeqSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err == nil {
		t.Fatal("écriture acceptée en lecture seule")
	}
}

func TestSearch(t *testing.T) {
	b := newBench(t)
	b.boxes.add(10, "pJC", mailbox.BoxInbox, "Re: Gravure", "Très intéressé par l'été", true)
	b.boxes.add(11, "pJC", mailbox.BoxInbox, "Autre sujet", "rien", true)
	b.boxes.msgs["m11"].flags = []string{string(imap.FlagSeen)}
	c := b.login(t, "jc@boite.fr")
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		c    imap.SearchCriteria
		want string
	}{
		"non lus":       {imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, "[10]"},
		"texte accents": {imap.SearchCriteria{Body: []string{"INTÉRESSÉ"}}, "[10]"},
		"objet":         {imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "autre"}}}, "[11]"},
		"date":          {imap.SearchCriteria{Since: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)}, "[]"},
		"uid":           {imap.SearchCriteria{UID: []imap.UIDSet{{imap.UIDRange{Start: 11, Stop: 0}}}}, "[11]"},
		"ou":            {imap.SearchCriteria{Or: [][2]imap.SearchCriteria{{{Text: []string{"rien"}}, {Text: []string{"été"}}}}}, "[10 11]"},
		"non":           {imap.SearchCriteria{Not: []imap.SearchCriteria{{Text: []string{"rien"}}}}, "[10]"},
	} {
		d, err := c.UIDSearch(&tc.c, nil).Wait()
		if err != nil || fmt.Sprint(d.AllUIDs()) != tc.want {
			t.Errorf("%s: %v %v, attendu %s", name, d.AllUIDs(), err, tc.want)
		}
	}
}

func TestAppend_OnlyWhatNotifuseSent(t *testing.T) {
	b := newBench(t)
	b.boxes.add(13, "pJC", mailbox.BoxSent, "Réponse", "ok", false) // <m13@boite.fr>
	c := b.login(t, "jc@boite.fr")
	put := func(folder, id string) error {
		raw := "From: jc@boite.fr\r\nMessage-Id: <" + id + ">\r\nSubject: x\r\n\r\nx\r\n"
		a := c.Append(folder, int64(len(raw)), nil)
		if _, err := a.Write([]byte(raw)); err != nil {
			return err
		}
		if err := a.Close(); err != nil {
			return err
		}
		_, err := a.Wait()
		return err
	}
	if err := put("Sent", "m13@poste-client"); err != nil {
		t.Fatalf("copie d'un envoi connu refusée: %v", err)
	}
	if err := put("Sent", "inconnu@poste"); err == nil {
		t.Fatal("un message jamais envoyé par Notifuse ne doit pas être avalé en silence")
	}
	if err := put("INBOX", "m13@boite.fr"); err == nil {
		t.Fatal("dépôt dans INBOX accepté")
	}
	if len(b.boxes.msgs) != 1 {
		t.Fatal("aucun doublon attendu")
	}
}

func TestPoll_NewMailFlagsAndRevocation(t *testing.T) {
	b := newBench(t)
	b.boxes.add(10, "pJC", mailbox.BoxInbox, "Un", "a", true)
	s := b.srv.newSession("203.0.113.9", nil)
	if err := s.Login("jc@boite.fr", "bon"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Select("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	b.auth.mu.Lock()
	b.auth.revoked["a1"] = true
	b.auth.mu.Unlock()
	s.checked = time.Now().Add(-time.Hour)
	if err := s.Poll(nil, true); err == nil || !strings.Contains(err.Error(), "revoque") {
		t.Fatalf("une session révoquée doit être coupée: %v", err)
	}
	_ = s.Close()
	if len(b.srv.perAcc) != 0 {
		t.Fatal("la connexion fermée doit libérer sa place")
	}

	// Nouveau courrier annoncé en cours de session.
	c := b.login(t, "autre@boite.fr")
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	b.boxes.add(20, "pAutre", mailbox.BoxInbox, "Neuf", "b", true)
	time.Sleep(refreshEvery + 200*time.Millisecond)
	if err := c.Noop().Wait(); err != nil {
		t.Fatal(err)
	}
	if n := c.Mailbox().NumMessages; n != 1 {
		t.Fatalf("courrier neuf non annoncé: %d", n)
	}
}

func TestAcquire_BoundsConnectionsPerAccess(t *testing.T) {
	b := newBench(t)
	var held []*session
	for i := 0; i < MaxConnsPerAccess; i++ {
		x := b.srv.newSession("203.0.113.9", nil)
		if !b.srv.acquire("a1", x) {
			t.Fatal("refus trop tôt")
		}
		held = append(held, x)
	}
	if b.srv.acquire("a1", b.srv.newSession("203.0.113.9", nil)) {
		t.Fatal("le nombre de connexions par accès doit être borné")
	}
	b.srv.release("a1", held[0])
	if !b.srv.acquire("a1", held[0]) {
		t.Fatal("une place libérée doit resservir")
	}
}

// En IPv6 un client dispose d'un /64 entier : changer d'adresse dedans ne rend pas un compteur neuf.
func TestLogin_IPv6LimitedByPrefix(t *testing.T) {
	b := newBench(t)
	var last error
	for i := 0; i <= TriesPerAddr; i++ {
		last = b.srv.newSession(fmt.Sprintf("2a02:c207:1:2::%x", i+1), nil).Login("jc@boite.fr", "faux")
	}
	if !errors.Is(last, errLimited) {
		t.Fatalf("même /64 : %v", last)
	}
	if err := b.srv.newSession("2a02:c207:1:3::1", nil).Login("jc@boite.fr", "faux"); errors.Is(err, errLimited) {
		t.Fatal("autre /64 : compteur à part")
	}
}

// Une session DÉJÀ ouverte se coupe en quelques secondes quand l'accès ne vaut plus (révoqué, auteur retiré de l'espace) ; celle d'un autre vit.
func TestSession_AlreadyOpenIsCutQuickly(t *testing.T) {
	b := newBench(t)
	mine, other := b.login(t, "jc@boite.fr"), b.login(t, "autre@boite.fr")
	for _, c := range []*imapclient.Client{mine, other} {
		if _, err := c.Select("INBOX", nil).Wait(); err != nil {
			t.Fatal(err)
		}
	}
	b.auth.mu.Lock()
	b.auth.revoked["a1"] = true
	b.auth.mu.Unlock()
	end := time.Now().Add(3 * time.Second)
	for mine.Noop().Wait() == nil {
		if time.Now().After(end) {
			t.Fatal("session encore ouverte 3 s après la révocation")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := other.Noop().Wait(); err != nil {
		t.Fatalf("session d'un autre coupée à tort : %v", err)
	}
}

// Sans attendre la relecture : les connexions en veille (IDLE) d'un accès sont coupées tout de suite, celles des autres non.
func TestCloseAccesses_CutsIdleConnectionsNow(t *testing.T) {
	b := newBench(t)
	mine, mine2, other := b.login(t, "jc@boite.fr"), b.login(t, "jc@boite.fr"), b.login(t, "autre@boite.fr")
	for _, c := range []*imapclient.Client{mine, mine2, other} {
		if _, err := c.Select("INBOX", nil).Wait(); err != nil {
			t.Fatal(err)
		}
	}
	idle, err := mine.Idle()
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { ended <- idle.Wait() }()
	if n := b.srv.CloseAccesses("inconnu"); n != 0 {
		t.Fatal(n)
	}
	if n := b.srv.CloseAccesses("a1"); n != 2 {
		t.Fatalf("deux connexions attendues, %d coupées", n)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("IDLE encore ouvert")
	}
	if mine2.Noop().Wait() == nil {
		t.Fatal("seconde connexion encore ouverte")
	}
	if err := other.Noop().Wait(); err != nil {
		t.Fatalf("connexion d'un autre coupée à tort : %v", err)
	}
	// Les places de l'accès sont rendues : il peut se reconnecter autant qu'avant (son titulaire est revenu).
	end := time.Now().Add(3 * time.Second)
	for {
		b.srv.mu.Lock()
		n, o := b.srv.perAcc["a1"], len(b.srv.open["a1"])
		b.srv.mu.Unlock()
		if n == 0 && o == 0 {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("places non rendues : %d %d", n, o)
		}
		time.Sleep(50 * time.Millisecond)
	}
	b.login(t, "jc@boite.fr")
}

// Toute commande qui lit ou écrit relit l'accès : une connexion que la coupure n'aurait pas atteinte n'obtient plus rien.
func TestSession_EveryCommandChecksTheAccess(t *testing.T) {
	one := imap.SeqSetNum(1)
	given := func(err error) int { // 1 : la commande a abouti
		if err == nil {
			return 1
		}
		return 0
	}
	cmds := map[string]func(c *imapclient.Client) int{
		"SELECT": func(c *imapclient.Client) int { _, err := c.Select("Sent", nil).Wait(); return given(err) },
		"STATUS": func(c *imapclient.Client) int {
			_, err := c.Status("INBOX", &imap.StatusOptions{NumMessages: true}).Wait()
			return given(err)
		},
		"FETCH": func(c *imapclient.Client) int {
			l, _ := c.Fetch(one, &imap.FetchOptions{Flags: true, UID: true}).Collect()
			return len(l)
		},
		"FETCH corps": func(c *imapclient.Client) int {
			l, _ := c.Fetch(one, &imap.FetchOptions{Envelope: true}).Collect()
			return len(l)
		},
		"SEARCH": func(c *imapclient.Client) int {
			_, err := c.Search(&imap.SearchCriteria{Body: []string{"corps"}}, nil).Wait()
			return given(err)
		},
		"STORE": func(c *imapclient.Client) int {
			return given(c.Store(one, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close())
		},
		"EXPUNGE": func(c *imapclient.Client) int { return given(c.Expunge().Close()) },
		"MOVE":    func(c *imapclient.Client) int { _, err := c.Move(one, "Trash").Wait(); return given(err) },
	}
	for name, do := range cmds {
		t.Run(name, func(t *testing.T) {
			b := newBench(t)
			id := b.boxes.add(1, "pJC", mailbox.BoxInbox, "sujet", "corps", true)
			b.boxes.mu.Lock()
			b.boxes.msgs[id].flags = []string{`\Deleted`} // EXPUNGE a quelque chose à retirer
			b.boxes.mu.Unlock()
			open := func() *imapclient.Client {
				c := b.login(t, "jc@boite.fr")
				if _, err := c.Select("INBOX", nil).Wait(); err != nil {
					t.Fatal(err)
				}
				return c
			}
			cut := open()
			b.auth.mu.Lock()
			b.auth.revoked["a1"] = true
			b.auth.mu.Unlock()
			time.Sleep(aliveEvery + 50*time.Millisecond)
			b.boxes.reset()
			if n := do(cut); n != 0 || b.boxes.touched() != 0 {
				t.Fatalf("après la coupure : %d rendu, boîte touchée %d fois", n, b.boxes.touched())
			}
			b.auth.mu.Lock()
			b.auth.revoked["a1"] = false
			b.auth.mu.Unlock()
			if do(open()) != 1 { // présence à côté : la commande aboutit quand l'accès vaut
				t.Fatal("la commande doit aboutir sur un accès vivant")
			}
		})
	}
}

// Retrait pendant la connexion : l'accès vu vivant à la lecture du mot de passe est relu une fois la connexion inscrite.
func TestLogin_RevokedDuringLogin(t *testing.T) {
	b := newBench(t)
	b.auth.mu.Lock()
	b.auth.revokeAfterVerify = true
	b.auth.mu.Unlock()
	c := b.dial(t)
	if err := c.Login("jc@boite.fr", "bon").Wait(); err == nil {
		t.Fatal("connexion ouverte sur un accès coupé")
	}
	b.srv.mu.Lock()
	defer b.srv.mu.Unlock()
	if len(b.srv.open) != 0 || len(b.srv.perAcc) != 0 {
		t.Fatal("place non rendue")
	}
}

// Un login réussi ne rend pas son budget à l'adresse : sinon un attaquant qui détient un accès valide (ou un seul) remet son
// compteur d'essais à zéro à volonté et devine sans fin les autres boîtes.
func TestLogin_SuccessDoesNotResetIPBudget(t *testing.T) {
	b := newBench(t)
	ip := "203.0.113.9"
	for i := 0; i < TriesPerIP-10; i++ {
		_ = b.srv.newSession(ip, nil).Login(fmt.Sprintf("x%d@boite.fr", i), "faux")
	}
	ok := b.srv.newSession(ip, nil)
	if err := ok.Login("jc@boite.fr", "bon"); err != nil {
		t.Fatalf("connexion valide : %v", err)
	}
	_ = ok.Close()
	limited := false
	for i := 0; i < 15; i++ {
		if err := b.srv.newSession(ip, nil).Login(fmt.Sprintf("y%d@boite.fr", i), "faux"); errors.Is(err, errLimited) {
			limited = true
		}
	}
	if !limited {
		t.Fatal("le budget de l'adresse a été remis à zéro par un login réussi")
	}
}
