package queue

import (
	"net"
	"strconv"
	"sync/atomic"

	"context"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/netguard"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEmersionIMAPDialer(t *testing.T) {
	d := newEmersionIMAPDialer()
	require.NotNil(t, d)
	var _ veridianIMAPDialer = d
}

// TestEmersionIMAPDialer_Dial_Guards couvre les garde-fous AVANT toute IO
// réseau (settings nil, password non déchiffré). On ne teste pas le dial réseau
// réel ici (pas de serveur IMAP en CI) — c'est le poller_test, via le fake
// dialer, qui couvre le comportement best-effort sur échec de dial.
func TestEmersionIMAPDialer_Dial_Guards(t *testing.T) {
	d := &emersionIMAPDialer{}
	ctx := context.Background()

	t.Run("nil settings", func(t *testing.T) {
		c, err := d.Dial(ctx, nil)
		require.Error(t, err)
		assert.Nil(t, c)
		assert.Contains(t, err.Error(), "imap settings required")
	})

	t.Run("empty (undecrypted) password", func(t *testing.T) {
		c, err := d.Dial(ctx, &domain.IMAPSettings{Host: "h", Port: 993, Username: "u"})
		require.Error(t, err)
		assert.Nil(t, c)
		assert.Contains(t, err.Error(), "password not decrypted")
	})
}

// TestEmersionIMAPClient_UIDValidity vérifie l'exposition de l'UIDVALIDITY
// capturé au SELECT (clé d'idempotence).
func TestEmersionIMAPClient_UIDValidity(t *testing.T) {
	c := &emersionIMAPClient{uidValidity: 12345, folder: "INBOX"}
	assert.Equal(t, uint32(12345), c.UIDValidity())
}

// TestEmersionIMAPClient_ToDomainMessage vérifie la conversion buffer go-imap
// -> DTO domain neutre (le contrat transmis aux lots 2/3).
func TestEmersionIMAPClient_ToDomainMessage(t *testing.T) {
	c := &emersionIMAPClient{uidValidity: 99, folder: "Bounces"}

	sentAt := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	buf := &imapclient.FetchMessageBuffer{
		UID: imap.UID(77),
		Envelope: &imap.Envelope{
			MessageID: "<msg-1@example.com>",
			Subject:   "Mail delivery failed",
			Date:      sentAt,
			InReplyTo: []string{"<orig@example.com>"},
			From:      []imap.Address{{Mailbox: "mailer-daemon", Host: "relay.example.com"}},
			To: []imap.Address{
				{Mailbox: "bounce", Host: "agences-veridian.fr"},
				{Mailbox: "", Host: ""}, // group marker / empty → skipped
			},
		},
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: []byte("Delivery to the following recipient failed permanently")},
		},
	}

	msg := c.toDomainMessage(buf)

	assert.Equal(t, uint32(77), msg.UID)
	assert.Equal(t, uint32(99), msg.UIDValidity)
	assert.Equal(t, "Bounces", msg.Folder)
	assert.Equal(t, "<msg-1@example.com>", msg.MessageID)
	assert.Equal(t, "Mail delivery failed", msg.Subject)
	assert.Equal(t, sentAt, msg.Date)
	assert.Equal(t, "<orig@example.com>", msg.InReplyTo)
	assert.Equal(t, "mailer-daemon@relay.example.com", msg.From)
	assert.Equal(t, []string{"bounce@agences-veridian.fr"}, msg.To)
	assert.Equal(t, []byte("Delivery to the following recipient failed permanently"), msg.RawBody)
}

// TestEmersionIMAPClient_ToDomainMessage_NilEnvelope : un buffer sans envelope
// (fetch partiel / message malformé) ne doit pas paniquer.
func TestEmersionIMAPClient_ToDomainMessage_NilEnvelope(t *testing.T) {
	c := &emersionIMAPClient{uidValidity: 1, folder: "INBOX"}
	buf := &imapclient.FetchMessageBuffer{UID: imap.UID(5)}

	var msg *domain.VeridianIMAPMessage
	assert.NotPanics(t, func() { msg = c.toDomainMessage(buf) })
	require.NotNil(t, msg)
	assert.Equal(t, uint32(5), msg.UID)
	assert.Empty(t, msg.From)
	assert.Empty(t, msg.RawBody)
}

// TestEmersionIMAPClient_ToDomainMessage_EmptyBodyTakesFirstNonEmpty vérifie
// qu'on prend la première section de corps NON vide.
func TestEmersionIMAPClient_ToDomainMessage_EmptyBodyTakesFirstNonEmpty(t *testing.T) {
	c := &emersionIMAPClient{uidValidity: 1, folder: "INBOX"}
	buf := &imapclient.FetchMessageBuffer{
		UID: imap.UID(5),
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: nil},
			{Bytes: []byte("real body")},
		},
	}
	msg := c.toDomainMessage(buf)
	assert.Equal(t, []byte("real body"), msg.RawBody)
}

// TestVeridianIMAPFetchOptions_BodyIsBounded : invariant anti-OOM. Le corps du
// message DOIT être fetché de façon BORNÉE (FETCH partiel côté serveur), jamais
// en section non bornée. Ce test casse si un refactor retire le cap — c'est sa
// raison d'être (un fetch RFC822 complet non borné charge un mail/NDR géant en
// RAM × le batch et fait amplifier le parser NDR récursif derrière).
func TestVeridianIMAPFetchOptions_BodyIsBounded(t *testing.T) {
	opts := veridianIMAPFetchOptions()

	require.Len(t, opts.BodySection, 1, "une seule section de corps fetchée")
	section := opts.BodySection[0]

	require.NotNil(t, section.Partial,
		"le corps DOIT être fetché en partiel (borné) — pas de RFC822 complet non borné")
	assert.Equal(t, int64(0), section.Partial.Offset, "on lit depuis le début du message")
	assert.Equal(t, int64(veridianIMAPMaxBodyBytes), section.Partial.Size,
		"la taille fetchée doit être plafonnée à veridianIMAPMaxBodyBytes")

	// Le cap doit rester raisonnable : assez pour les headers de matching +
	// la structure DSN d'un NDR, sans rapatrier des Mo de pièces jointes.
	assert.LessOrEqual(t, int64(veridianIMAPMaxBodyBytes), int64(8*1024*1024),
		"le cap ne doit pas redevenir un quasi-illimité")
	assert.Positive(t, veridianIMAPMaxBodyBytes, "cap strictement positif")

	assert.True(t, opts.UID, "UID demandé (clé d'idempotence)")
	assert.True(t, opts.Envelope, "Envelope demandé (headers de matching)")
}

// L'enveloppe IMAP ne porte pas References : on la lit depuis les en-tetes du RFC822 brut.
func TestEmersionIMAPClient_ToDomainMessage_ReadsReferencesFromRawHeaders(t *testing.T) {
	c := &emersionIMAPClient{uidValidity: 1, folder: "INBOX"}
	raw := []byte("From: a@b.fr\r\nReferences: <root@x.fr> <sent-1@agence-veridian.fr>\r\nSubject: Re: q\r\n\r\ncorps")
	buf := &imapclient.FetchMessageBuffer{
		UID:         imap.UID(8),
		BodySection: []imapclient.FetchBodySectionBuffer{{Bytes: raw}},
	}
	msg := c.toDomainMessage(buf)
	assert.Equal(t, []string{"<root@x.fr> <sent-1@agence-veridian.fr>"}, msg.References)
	assert.Equal(t, []string{"root", "sent-1"}, domain.VeridianExtractMessageIDLocalParts("", msg.References))
}

func fakeIMAPListener(t *testing.T) (int, *int32) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })
	var n int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&n, 1)
			c.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, &n
}

func TestIMAPDial_RefusesInternalHosts(t *testing.T) {
	port, conns := fakeIMAPListener(t)
	d := &emersionIMAPDialer{}
	for _, c := range []struct {
		name, host string
		port       int
	}{
		{"loopback joignable", "127.0.0.1", port},
		{"localhost", "localhost", port},
		{"metadonnees", "169.254.169.254", 143},
		{"rfc1918", "10.1.1.1", 993},
		{"tailscale hors liste", "100.64.9.9", 993},
		{"relai connu mais autre port", "100.88.202.29", 143},
		{"ipv6 loopback", "[::1]", port},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := d.Dial(context.Background(), &domain.IMAPSettings{Host: c.host, Port: c.port, Username: "u", Password: "p", UseTLS: true})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "refusing to connect")
		})
	}
	assert.EqualValues(t, 0, atomic.LoadInt32(conns))
}

// Contrôle positif : l'exception exacte laisse passer la connexion (le faux
// serveur raccroche ensuite, l'erreur n'est plus un refus de la garde).
func TestIMAPDial_AllowlistedRelayIsReached(t *testing.T) {
	port, conns := fakeIMAPListener(t)
	old := imapDialAllowlist
	imapDialAllowlist = func() netguard.Allowlist {
		return netguard.Allowlist{{IP: net.ParseIP("127.0.0.1"), Port: port}}
	}
	t.Cleanup(func() { imapDialAllowlist = old })
	_, err := (&emersionIMAPDialer{}).Dial(context.Background(), &domain.IMAPSettings{Host: "127.0.0.1", Port: port, Username: "u", Password: "p", UseTLS: true})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "refusing to connect")
	assert.EqualValues(t, 1, atomic.LoadInt32(conns), "host="+strconv.Itoa(port))
}
