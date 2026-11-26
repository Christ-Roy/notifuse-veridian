package domauth

import (
	"context"
	"crypto/rsa"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Revérification à l'usage (VER-166 suite) : base réelle (testpg), DNS simulé, signataire de production.

type recheckFixture struct {
	store  *Store
	db     *sql.DB
	dns    *fakeDNS
	d      *Domain
	signer *Signer
}

func newRecheckFixture(t *testing.T) *recheckFixture {
	t.Helper()
	s, db := pgStore(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "wsa", "client.fr", "humain:u1")
	require.NoError(t, err)
	f := goodDNS(*d)
	f.txt[DKIMName(*d)] = []string{DKIMValue(*d)} // DKIM publié avec la VRAIE clé du domaine
	v := &Verifier{Resolver: f, Include: inc}
	checks := v.Verify(ctx, *d)
	d, err = s.SaveCheck(ctx, "wsa", d.ID, StatusOf(d.Status, checks), checks)
	require.NoError(t, err)
	require.Equal(t, StatusVerified, d.Status)
	f.asked = nil
	sg := &Signer{Keys: s, Recheck: (&Rechecker{Store: s, DNS: v}).Run}
	return &recheckFixture{store: s, db: db, dns: f, d: d, signer: sg}
}

// age : le dernier contrôle remonte à plus de 24 h (le temps qui passe, sans attendre).
func (x *recheckFixture) age(t *testing.T) {
	_, err := x.db.Exec(`UPDATE veridian_sending_domain SET last_checked_at = NOW() - INTERVAL '25 hours' WHERE id = $1`, x.d.ID)
	require.NoError(t, err)
	x.signer.Forget("wsa", "client.fr")
}

func (x *recheckFixture) send(t *testing.T) bool {
	_, ok := x.signer.Sign(context.Background(), "wsa", "bonjour@client.fr", []byte(sampleMsg))
	return ok
}

func (x *recheckFixture) dkimLookups() int {
	x.dns.mu.Lock()
	defer x.dns.mu.Unlock()
	n := 0
	for _, a := range x.dns.asked {
		if a == DKIMName(*x.d) {
			n++
		}
	}
	return n
}

func (x *recheckFixture) state(t *testing.T) *Domain {
	d, err := x.store.Get(context.Background(), "wsa", x.d.ID)
	require.NoError(t, err)
	return d
}

func TestRecheck_CleAbsenteCoupeLaSignatureEtLeClientLeVoit(t *testing.T) {
	x := newRecheckFixture(t)
	x.age(t)
	delete(x.dns.txt, DKIMName(*x.d)) // NXDOMAIN : le client a retiré le TXT

	assert.True(t, x.send(t), "l'envoi qui déclenche le contrôle part avec l'état connu, sans attendre le DNS")
	x.signer.Wait()
	d := x.state(t)
	assert.Equal(t, StatusPending, d.Status)
	require.NotNil(t, d.LostAt, "la date du constat est gardée")
	assert.Equal(t, "missing", d.LostReason)
	assert.Nil(t, d.VerifiedAt)
	assert.False(t, x.send(t), "dès le contrôle fait, plus aucune signature avec une clé que personne ne peut vérifier")

	// Le client republie et clique sur Vérifier : retour à la normale, le constat est effacé.
	x.dns.txt[DKIMName(*x.d)] = []string{DKIMValue(*x.d)}
	v := &Verifier{Resolver: x.dns, Include: inc}
	checks := v.Verify(context.Background(), *d)
	d, err := x.store.SaveCheck(context.Background(), "wsa", d.ID, StatusOf(d.Status, checks), checks)
	require.NoError(t, err)
	assert.Equal(t, StatusVerified, d.Status)
	assert.Nil(t, d.LostAt)
	assert.Empty(t, d.LostReason)
	x.signer.Forget("wsa", "client.fr")
	assert.True(t, x.send(t))
}

func TestRecheck_CleDifferenteCoupeLaSignature(t *testing.T) {
	x := newRecheckFixture(t)
	x.age(t)
	x.dns.txt[DKIMName(*x.d)] = []string{"v=DKIM1; k=rsa; p=UNEAUTRECLE"}
	x.send(t)
	x.signer.Wait()
	d := x.state(t)
	assert.Equal(t, StatusPending, d.Status)
	assert.Equal(t, "mismatch", d.LostReason)
	assert.False(t, x.send(t))
}

func TestRecheck_PanneDNSPassagereNeCoupeRien(t *testing.T) {
	x := newRecheckFixture(t)
	x.age(t)
	x.dns.fail[DKIMName(*x.d)] = true // SERVFAIL ou délai dépassé
	before := x.state(t).LastCheckedAt
	x.send(t)
	x.signer.Wait()
	d := x.state(t)
	assert.Equal(t, StatusVerified, d.Status)
	assert.Nil(t, d.LostAt)
	require.NotNil(t, d.LastCheckedAt)
	assert.True(t, d.LastCheckedAt.After(*before), "le contrôle est daté : pas de nouvel essai avant 24 h")
	assert.True(t, x.send(t), "la signature continue")
	assert.Equal(t, 1, x.dkimLookups())
}

func TestRecheck_MoinsDe24hAucunAppelEtUnSeulALaFois(t *testing.T) {
	x := newRecheckFixture(t)
	for i := 0; i < 5; i++ {
		assert.True(t, x.send(t))
	}
	x.signer.Wait()
	assert.Zero(t, x.dkimLookups(), "contrôlé il y a moins de 24 h : aucun appel DNS")

	// Vieux de 25 h : vingt envois simultanés, un seul contrôle.
	x.age(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); x.send(t) }()
	}
	wg.Wait()
	x.signer.Wait()
	assert.Equal(t, 1, x.dkimLookups())

	// Même si la base redevient vieille (écriture ratée, autre cause), ce processus ne recontrôle pas avant 24 h.
	x.age(t)
	x.send(t)
	x.signer.Wait()
	assert.Equal(t, 1, x.dkimLookups())
}

func TestSigner_RevérificationNeBloquePasLesEnvois(t *testing.T) {
	k, _ := testKey(t)
	release := make(chan struct{})
	var started int32
	s := &Signer{
		Keys: &oldKeys{key: k},
		Recheck: func(ctx context.Context, ws, id string) {
			atomic.AddInt32(&started, 1)
			<-release
		},
	}
	done := make(chan bool)
	go func() {
		for i := 0; i < 10; i++ {
			_, ok := s.Sign(context.Background(), "wsa", "bonjour@client.fr", []byte(sampleMsg))
			if !ok {
				done <- false
				return
			}
		}
		done <- true
	}()
	select {
	case ok := <-done:
		assert.True(t, ok)
	case <-time.After(5 * time.Second):
		t.Fatal("les envois attendent le contrôle DNS")
	}
	close(release)
	s.Wait()
	assert.EqualValues(t, 1, atomic.LoadInt32(&started), "un seul contrôle à la fois par domaine")
}

type oldKeys struct{ key *rsa.PrivateKey }

// Un domaine vérifié dont le dernier contrôle a 48 h.
func (o *oldKeys) SigningKey(context.Context, string, string) (*SigningKey, error) {
	return &SigningKey{ID: "d1", Selector: "vd1", Key: o.key, LastChecked: time.Now().Add(-48 * time.Hour)}, nil
}
